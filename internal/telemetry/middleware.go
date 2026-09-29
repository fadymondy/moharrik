package telemetry

import (
	"fmt"
	"net/http"
	"runtime/debug"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const scope = "github.com/fadymondy/moharrik/internal/telemetry"

// Middleware opens one server span per request, named by chi's route pattern
// (never the raw path, so ids do not explode the endpoint list). 5xx and
// panics mark the span as an error; a panic is recorded as an "exception"
// event with its stack — what Hosbah groups into Errors — and then re-raised
// for the problem-report middleware to answer as it always has.
//
// With no provider installed the global tracer is a no-op and this costs a
// few allocations per request.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if skip(r) {
			next.ServeHTTP(w, r)
			return
		}
		ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		ctx, sp := otel.Tracer(scope).Start(ctx, r.Method, trace.WithSpanKind(trace.SpanKindServer))
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

		defer func() {
			rec := recover()
			route := ""
			if rc := chi.RouteContext(r.Context()); rc != nil {
				route = rc.RoutePattern()
			}
			if route == "" {
				route = "unmatched"
			}
			status := ww.Status()
			if status == 0 {
				status = http.StatusOK
			}
			if rec != nil {
				status = http.StatusInternalServerError
				sp.AddEvent("exception", trace.WithAttributes(
					attribute.String("exception.type", fmt.Sprintf("panic: %T", rec)),
					attribute.String("exception.message", fmt.Sprint(rec)),
					attribute.String("exception.stacktrace", string(debug.Stack())),
				))
			}
			sp.SetName(r.Method + " " + route)
			sp.SetAttributes(
				attribute.String("http.request.method", r.Method),
				attribute.String("http.route", route),
				attribute.Int("http.response.status_code", status),
				attribute.String("url.scheme", scheme(r)),
			)
			if status >= 500 {
				sp.SetStatus(codes.Error, http.StatusText(status))
			}
			sp.End()
			if rec != nil {
				panic(rec)
			}
		}()
		next.ServeHTTP(ww, r.WithContext(ctx))
	})
}

// skip leaves out what would only be noise: the health probe, and long-lived
// streams (SSE and WebSockets) whose "duration" is how long a tab stayed open.
func skip(r *http.Request) bool {
	return r.URL.Path == "/api/health" ||
		strings.EqualFold(r.Header.Get("Upgrade"), "websocket") ||
		strings.Contains(r.Header.Get("Accept"), "text/event-stream")
}

func scheme(r *http.Request) string {
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		return p
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}
