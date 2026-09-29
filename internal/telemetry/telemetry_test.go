package telemetry

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel"
)

// A fake Hosbah ingest: what reaches it is exactly what production receives.
type ingest struct {
	mu     sync.Mutex
	key    string
	ctype  string
	bodies []map[string]any
}

func (in *ingest) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	in.mu.Lock()
	in.key, in.ctype = r.Header.Get("x-hosbah-key"), r.Header.Get("Content-Type")
	in.bodies = append(in.bodies, m)
	in.mu.Unlock()
	w.WriteHeader(http.StatusAccepted)
}

func TestFromEnv(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://hosbah.test/api/otlp/")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/json")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "x-hosbah-key=hbt_abc%3D, other = v ")
	t.Setenv("OTEL_SERVICE_NAME", "moharrik-app")
	cfg, ok := FromEnv(slog.Default())
	if !ok || cfg.Endpoint != "https://hosbah.test/api/otlp/v1/traces" || cfg.ServiceName != "moharrik-app" {
		t.Fatalf("config: %+v ok=%v", cfg, ok)
	}
	if cfg.Headers["x-hosbah-key"] != "hbt_abc=" || cfg.Headers["other"] != "v" {
		t.Fatalf("headers: %v", cfg.Headers)
	}

	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")
	if _, ok := FromEnv(slog.Default()); ok {
		t.Fatal("protobuf must turn traces off: Hosbah reads JSON only")
	}
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "")
	if _, ok := FromEnv(slog.Default()); ok {
		t.Fatal("no endpoint must mean no-op")
	}
}

func TestRequestsReachHosbahAsOTLPJSON(t *testing.T) {
	in := &ingest{}
	srv := httptest.NewServer(in)
	defer srv.Close()

	cfg := Config{Endpoint: srv.URL + "/api/otlp/v1/traces", Headers: map[string]string{"x-hosbah-key": "hbt_test"}, ServiceName: "moharrik-app"}
	tp := NewProvider(cfg, newExporter(cfg, slog.Default()))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	defer otel.SetTracerProvider(prev)

	r := chi.NewRouter()
	r.Use(Middleware)
	r.Use(func(next http.Handler) http.Handler { // stands in for the problem-report recoverer
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { next.ServeHTTP(w, r) })
	})
	r.Get("/api/w/contacts/{id}", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) })
	r.Get("/api/boom", func(http.ResponseWriter, *http.Request) { panic("kaboom") })
	r.Get("/api/health", func(w http.ResponseWriter, r *http.Request) {})

	get := func(path string) {
		func() {
			defer func() { _ = recover() }() // the re-raised panic
			r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
		}()
	}
	get("/api/w/contacts/4933c603")
	get("/api/boom")
	get("/api/health")
	if err := tp.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	in.mu.Lock()
	defer in.mu.Unlock()
	if in.key != "hbt_test" || in.ctype != "application/json" {
		t.Fatalf("key %q, content type %q", in.key, in.ctype)
	}
	var spans []map[string]any
	var service string
	for _, b := range in.bodies {
		for _, rs := range b["resourceSpans"].([]any) {
			rsm := rs.(map[string]any)
			for _, a := range rsm["resource"].(map[string]any)["attributes"].([]any) {
				if a.(map[string]any)["key"] == "service.name" {
					service = a.(map[string]any)["value"].(map[string]any)["stringValue"].(string)
				}
			}
			for _, ss := range rsm["scopeSpans"].([]any) {
				for _, s := range ss.(map[string]any)["spans"].([]any) {
					spans = append(spans, s.(map[string]any))
				}
			}
		}
	}
	if service != "moharrik-app" {
		t.Fatalf("service.name = %q", service)
	}
	if len(spans) != 2 {
		t.Fatalf("want 2 spans (health is skipped), got %d", len(spans))
	}
	byName := map[string]map[string]any{}
	for _, s := range spans {
		byName[s["name"].(string)] = s
		if len(s["traceId"].(string)) != 32 || len(s["spanId"].(string)) != 16 || s["kind"].(float64) != 2 {
			t.Fatalf("ids/kind not OTLP JSON: %v", s)
		}
	}
	nf := byName["GET /api/w/contacts/{id}"]
	if nf == nil {
		t.Fatalf("the span is named by route, not path: %v", byName)
	}
	if attr(nf, "http.response.status_code")["intValue"] != "404" || nf["status"] != nil {
		t.Fatalf("a 404 is recorded and is not an error: %v", nf)
	}
	boom := byName["GET /api/boom"]
	if boom == nil || boom["status"].(map[string]any)["code"].(float64) != 2 {
		t.Fatalf("a panic is an error span (OTLP code 2): %v", boom)
	}
	evs, _ := boom["events"].([]any)
	if len(evs) != 1 || evs[0].(map[string]any)["name"] != "exception" {
		t.Fatalf("a panic is an exception event: %v", evs)
	}
	var msg, stack string
	for _, a := range evs[0].(map[string]any)["attributes"].([]any) {
		kv := a.(map[string]any)
		v, _ := kv["value"].(map[string]any)["stringValue"].(string)
		switch kv["key"] {
		case "exception.message":
			msg = v
		case "exception.stacktrace":
			stack = v
		}
	}
	if msg != "kaboom" || stack == "" {
		t.Fatalf("exception message %q, stack %d bytes", msg, len(stack))
	}
}

func attr(span map[string]any, key string) map[string]any {
	for _, a := range span["attributes"].([]any) {
		kv := a.(map[string]any)
		if kv["key"] == key {
			return kv["value"].(map[string]any)
		}
	}
	return nil
}
