package googletest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

/*
GA4 for conversions (MH-449): the Measurement Protocol and the Admin API's
data streams.

	POST /ga/mp/collect?measurement_id=G-…&api_secret=…         answers 204, records the hit
	POST /ga/debug/mp/collect?measurement_id=G-…&api_secret=…   the validation server: records
	                                                            nothing, answers validationMessages
	GET  /analyticsadmin/v1beta/properties/{id}/dataStreams     one web stream per property,
	                                                            measurement id "G-" + the property's digits

Like Google, collect accepts ANY payload with 2xx and says nothing; only the
validation server finds fault, and in the same shape Google's does. A secret
the fake has not been told about (MPSecrets) is accepted by collect too,
because Google's is: the fake records it so a test can assert what was sent.
MPStatus makes collect answer an error status instead (a Google outage).
*/

// MPHit is one Measurement Protocol request the fake received.
type MPHit struct {
	MeasurementID string
	APISecret     string
	Debug         bool
	Body          map[string]any
}

// MPHits returns every collect request received (the validation server's included).
func (s *Server) MPHits() []MPHit {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]MPHit(nil), s.mpHits...)
}

// MPStatus makes /mp/collect answer this status (0 restores 204).
func (s *Server) MPStatus(status int) {
	s.mu.Lock()
	s.mpStatus = status
	s.mu.Unlock()
}

var fakeMeasurementID = regexp.MustCompile(`^G-[A-Z0-9]{4,20}$`)

func (s *Server) measurementProtocol(w http.ResponseWriter, r *http.Request, p string) bool {
	if r.Method != http.MethodPost || (p != "/ga/mp/collect" && p != "/ga/debug/mp/collect") {
		return false
	}
	debug := p == "/ga/debug/mp/collect"
	s.count("mp")
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 130<<10))
	var body map[string]any
	parseErr := json.Unmarshal(raw, &body)
	q := r.URL.Query()
	hit := MPHit{MeasurementID: q.Get("measurement_id"), APISecret: q.Get("api_secret"), Debug: debug, Body: body}
	s.mu.Lock()
	s.mpHits = append(s.mpHits, hit)
	status := s.mpStatus
	s.mu.Unlock()

	if !debug {
		if status != 0 {
			w.WriteHeader(status)
			return true
		}
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	var msgs []map[string]string
	add := func(path, code, desc string) {
		msgs = append(msgs, map[string]string{"fieldPath": path, "description": desc, "validationCode": code})
	}
	switch {
	case parseErr != nil:
		add("", "VALUE_INVALID", "Unable to parse Measurement Protocol JSON payload.")
	default:
		if !fakeMeasurementID.MatchString(hit.MeasurementID) {
			add("measurement_id", "VALUE_INVALID", "Measurement ID "+hit.MeasurementID+" is not valid.")
		}
		if hit.APISecret == "" {
			add("api_secret", "VALUE_REQUIRED", "api_secret is required.")
		}
		if cid, _ := body["client_id"].(string); cid == "" {
			add("client_id", "VALUE_REQUIRED", "client_id is required.")
		}
		evs, _ := body["events"].([]any)
		if len(evs) == 0 {
			add("events", "VALUE_REQUIRED", "At least one event is required.")
		}
		for i, e := range evs {
			m, _ := e.(map[string]any)
			name, _ := m["name"].(string)
			if !regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,39}$`).MatchString(name) || strings.HasPrefix(name, "google_") {
				add(fmt.Sprintf("events[%d].name", i), "NAME_INVALID", "Event name "+name+" is invalid.")
			}
		}
	}
	if msgs == nil {
		msgs = []map[string]string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"validationMessages": msgs})
	return true
}

// dataStreams answers properties/{id}/dataStreams for a property the account can see.
func (s *Server) dataStreams(w http.ResponseWriter, r *http.Request, p string) bool {
	const prefix = "/analyticsadmin/v1beta/properties/"
	if r.Method != http.MethodGet || !strings.HasPrefix(p, prefix) || !strings.HasSuffix(p, "/dataStreams") {
		return false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(p, prefix), "/dataStreams")
	_, d, ok := s.begin(w, r, "admin")
	if !ok {
		return true
	}
	s.mu.Lock()
	var prop *GA4Property
	for i := range d.GA4 {
		if d.GA4[i].ID == "properties/"+id {
			prop = &d.GA4[i]
		}
	}
	s.mu.Unlock()
	if prop == nil {
		writeAPIError(w, "admin", http.StatusForbidden, "PERMISSION_DENIED", "User does not have sufficient permissions for this property.")
		return true
	}
	writeJSON(w, http.StatusOK, map[string]any{"dataStreams": []map[string]any{{
		"name": prop.ID + "/dataStreams/" + id + "1", "type": "WEB_DATA_STREAM", "displayName": prop.Name + " (web)",
		"webStreamData": map[string]any{"measurementId": FakeMeasurementID(prop.ID), "defaultUri": "https://demo.example"},
	}}})
	return true
}

// FakeMeasurementID is the measurement id of a fake property's web stream.
func FakeMeasurementID(property string) string {
	digits := strings.TrimPrefix(property, "properties/")
	if len(digits) > 10 {
		digits = digits[:10]
	}
	return "G-" + digits
}
