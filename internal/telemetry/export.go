package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// exporter posts spans as OTLP/HTTP JSON (the protobuf JSON mapping: hex
// ids, int64 as strings, lowerCamelCase keys).
type exporter struct {
	cfg  Config
	http *http.Client
	log  *slog.Logger

	mu       sync.Mutex
	lastWarn time.Time
}

func newExporter(cfg Config, log *slog.Logger) *exporter {
	return &exporter{cfg: cfg, http: &http.Client{Timeout: 10 * time.Second}, log: log}
}

func (e *exporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	if len(spans) == 0 {
		return nil
	}
	body, err := json.Marshal(encode(spans))
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range e.cfg.Headers {
		req.Header.Set(k, v)
	}
	res, err := e.http.Do(req)
	if err == nil {
		io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
		res.Body.Close()
		if res.StatusCode >= 300 {
			err = fmt.Errorf("otlp export: HTTP %d", res.StatusCode)
		}
	}
	if err != nil {
		// Telemetry must never be what takes the API down or floods its log:
		// the batch is dropped and the failure is said at most once a minute.
		e.mu.Lock()
		if time.Since(e.lastWarn) > time.Minute {
			e.lastWarn = time.Now()
			e.log.Warn("telemetry: export failed; spans dropped", "err", err, "spans", len(spans))
		}
		e.mu.Unlock()
	}
	return nil
}

func (e *exporter) Shutdown(context.Context) error { return nil }

type (
	anyValue struct {
		StringValue *string    `json:"stringValue,omitempty"`
		IntValue    *string    `json:"intValue,omitempty"`
		DoubleValue *float64   `json:"doubleValue,omitempty"`
		BoolValue   *bool      `json:"boolValue,omitempty"`
		ArrayValue  *arrayVals `json:"arrayValue,omitempty"`
	}
	arrayVals struct {
		Values []anyValue `json:"values"`
	}
	keyValue struct {
		Key   string   `json:"key"`
		Value anyValue `json:"value"`
	}
	status struct {
		Code    int    `json:"code,omitempty"`
		Message string `json:"message,omitempty"`
	}
	event struct {
		TimeUnixNano string     `json:"timeUnixNano"`
		Name         string     `json:"name"`
		Attributes   []keyValue `json:"attributes,omitempty"`
	}
	span struct {
		TraceID           string     `json:"traceId"`
		SpanID            string     `json:"spanId"`
		ParentSpanID      string     `json:"parentSpanId,omitempty"`
		Name              string     `json:"name"`
		Kind              int        `json:"kind"`
		StartTimeUnixNano string     `json:"startTimeUnixNano"`
		EndTimeUnixNano   string     `json:"endTimeUnixNano"`
		Attributes        []keyValue `json:"attributes,omitempty"`
		Status            *status    `json:"status,omitempty"`
		Events            []event    `json:"events,omitempty"`
	}
	scopeSpans struct {
		Scope struct {
			Name string `json:"name"`
		} `json:"scope"`
		Spans []span `json:"spans"`
	}
	resourceSpans struct {
		Resource struct {
			Attributes []keyValue `json:"attributes"`
		} `json:"resource"`
		ScopeSpans []scopeSpans `json:"scopeSpans"`
	}
	traceRequest struct {
		ResourceSpans []resourceSpans `json:"resourceSpans"`
	}
)

// encode groups spans by resource, then by instrumentation scope.
func encode(spans []sdktrace.ReadOnlySpan) traceRequest {
	var out traceRequest
	byRes := map[string]int{}
	byScope := map[[2]string]int{}
	for _, s := range spans {
		resKey := s.Resource().Encoded(attribute.DefaultEncoder())
		ri, ok := byRes[resKey]
		if !ok {
			ri = len(out.ResourceSpans)
			byRes[resKey] = ri
			var rs resourceSpans
			rs.Resource.Attributes = kvs(s.Resource().Attributes())
			out.ResourceSpans = append(out.ResourceSpans, rs)
		}
		scope := s.InstrumentationScope().Name
		si, ok := byScope[[2]string{resKey, scope}]
		if !ok {
			si = len(out.ResourceSpans[ri].ScopeSpans)
			byScope[[2]string{resKey, scope}] = si
			var ss scopeSpans
			ss.Scope.Name = scope
			out.ResourceSpans[ri].ScopeSpans = append(out.ResourceSpans[ri].ScopeSpans, ss)
		}
		out.ResourceSpans[ri].ScopeSpans[si].Spans = append(out.ResourceSpans[ri].ScopeSpans[si].Spans, encodeSpan(s))
	}
	return out
}

func encodeSpan(s sdktrace.ReadOnlySpan) span {
	sc := s.SpanContext()
	out := span{
		TraceID:           sc.TraceID().String(),
		SpanID:            sc.SpanID().String(),
		Name:              s.Name(),
		Kind:              int(s.SpanKind()), // the Go enum matches OTLP's: internal 1, server 2, client 3…
		StartTimeUnixNano: nanos(s.StartTime()),
		EndTimeUnixNano:   nanos(s.EndTime()),
		Attributes:        kvs(s.Attributes()),
	}
	if p := s.Parent(); p.IsValid() {
		out.ParentSpanID = p.SpanID().String()
	}
	// The Go enum is Unset 0, Error 1, Ok 2; OTLP's is Unset 0, Ok 1, Error 2.
	switch st := s.Status(); st.Code {
	case codes.Error:
		out.Status = &status{Code: 2, Message: st.Description}
	case codes.Ok:
		out.Status = &status{Code: 1}
	}
	for _, ev := range s.Events() {
		out.Events = append(out.Events, event{TimeUnixNano: nanos(ev.Time), Name: ev.Name, Attributes: kvs(ev.Attributes)})
	}
	return out
}

func nanos(t time.Time) string { return strconv.FormatInt(t.UnixNano(), 10) }

func kvs(attrs []attribute.KeyValue) []keyValue {
	out := make([]keyValue, 0, len(attrs))
	for _, a := range attrs {
		out = append(out, keyValue{Key: string(a.Key), Value: value(a.Value)})
	}
	return out
}

func value(v attribute.Value) anyValue {
	switch v.Type() {
	case attribute.BOOL:
		b := v.AsBool()
		return anyValue{BoolValue: &b}
	case attribute.INT64:
		s := strconv.FormatInt(v.AsInt64(), 10)
		return anyValue{IntValue: &s}
	case attribute.FLOAT64:
		f := v.AsFloat64()
		return anyValue{DoubleValue: &f}
	case attribute.STRINGSLICE:
		var vals []anyValue
		for _, s := range v.AsStringSlice() {
			vals = append(vals, anyValue{StringValue: &s})
		}
		return anyValue{ArrayValue: &arrayVals{Values: vals}}
	default:
		s := v.Emit()
		return anyValue{StringValue: &s}
	}
}
