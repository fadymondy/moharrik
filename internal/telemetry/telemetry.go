// Package telemetry sends the API's traces and exceptions to Hosbah APM.
//
// Hosbah injects the standard OTEL_EXPORTER_OTLP_* variables into the app's
// env on every deploy once APM is on, and its ingest reads OTLP/HTTP **JSON**
// only. The OpenTelemetry Go exporter speaks protobuf only, so this package
// keeps the OpenTelemetry SDK (spans, context, sampling, batching) and swaps
// in a small JSON exporter (export.go). Unset endpoint means no-op: local
// development and tests send nothing.
package telemetry

import (
	"context"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Config is what Init reads from the environment.
type Config struct {
	Endpoint    string            // traces URL, e.g. https://app.hosbah.com/api/otlp/v1/traces
	Headers     map[string]string // x-hosbah-key=…
	ServiceName string
	Environment string
}

// FromEnv reads the standard OTEL_* variables. ok is false when there is
// nowhere to send traces, or the protocol asks for something other than JSON.
func FromEnv(log *slog.Logger) (Config, bool) {
	endpoint := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"))
	if endpoint == "" {
		base := strings.TrimRight(strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")), "/")
		if base == "" {
			return Config{}, false
		}
		endpoint = base + "/v1/traces"
	}
	if p := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL")); p != "" && p != "http/json" {
		log.Warn("telemetry: only OTLP http/json is supported; traces are off", "protocol", p)
		return Config{}, false
	}
	name := strings.TrimSpace(os.Getenv("OTEL_SERVICE_NAME"))
	if name == "" {
		name = "moharrik"
	}
	return Config{
		Endpoint:    endpoint,
		Headers:     parseHeaders(os.Getenv("OTEL_EXPORTER_OTLP_HEADERS")),
		ServiceName: name,
		Environment: strings.TrimSpace(os.Getenv("APP_ENV")),
	}, true
}

// parseHeaders reads the spec's "k1=v1,k2=v2" form, values percent-encoded.
func parseHeaders(s string) map[string]string {
	out := map[string]string{}
	for _, pair := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(pair, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			continue
		}
		if dec, err := url.PathUnescape(strings.TrimSpace(v)); err == nil {
			v = dec
		}
		out[k] = strings.TrimSpace(v)
	}
	return out
}

// Init installs the global tracer provider when the environment names an
// endpoint. The returned shutdown flushes what is buffered; it is always
// safe to call.
func Init(log *slog.Logger) (shutdown func(context.Context) error) {
	cfg, ok := FromEnv(log)
	if !ok {
		return func(context.Context) error { return nil }
	}
	tp := NewProvider(cfg, newExporter(cfg, log))
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	log.Info("telemetry: traces go to Hosbah APM", "service", cfg.ServiceName)
	return tp.Shutdown
}

// NewProvider builds a batching tracer provider over exp.
func NewProvider(cfg Config, exp sdktrace.SpanExporter) *sdktrace.TracerProvider {
	attrs := []attribute.KeyValue{attribute.String("service.name", cfg.ServiceName)}
	if cfg.Environment != "" {
		attrs = append(attrs, attribute.String("deployment.environment.name", cfg.Environment))
	}
	return sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp, sdktrace.WithBatchTimeout(5*time.Second)),
		sdktrace.WithResource(resource.NewSchemaless(attrs...)),
	)
}
