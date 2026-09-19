package server

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/togo-framework/togo"

	"github.com/fadymondy/moharrik/internal/feedback"
)

/*
installProblemReports files Moharrik's own backend failures as issues in
Mahaam (the vendored Mahaam Feedback SDK in internal/feedback): a panic is
reported with its stack by the SDK's Recover and answered 500; any other 5xx
response is reported once per method+route+status per window, so an outage
does not become a flood of issues.

Configured from MAHAAM_URL / MAHAAM_FEEDBACK_KEY / MAHAAM_APP_URL. With no
key it is a no-op apart from panic recovery.
*/
func installProblemReports(k *togo.Kernel) {
	c := feedback.New(feedback.Config{})
	dedupe := newOnceWindow(10 * time.Minute)

	k.UseMiddleware(func(next http.Handler) http.Handler {
		// Recover sits inside, so a panic is reported once (by the SDK) and
		// not a second time as a plain 500.
		inner := c.Recover(panicMark(next))
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var panicked bool
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			inner.ServeHTTP(sw, r.WithContext(context.WithValue(r.Context(), panicKey{}, &panicked)))
			if panicked || sw.status < 500 || !c.Enabled() {
				return
			}
			key := fmt.Sprintf("%s %s %d", r.Method, routePattern(r), sw.status)
			if !dedupe.first(key) {
				return
			}
			go func(ctx context.Context) {
				_, _ = c.Report(ctx, feedback.Report{
					Title:    fmt.Sprintf("%d on %s %s", sw.status, r.Method, routePattern(r)),
					Body:     fmt.Sprintf("%s %s answered %d.", r.Method, r.URL.Path, sw.status),
					Type:     "bug",
					Priority: "high",
					Extra:    map[string]string{"route": r.URL.Path, "user_agent": r.UserAgent()},
				})
			}(context.WithoutCancel(r.Context()))
		})
	})
}

type panicKey struct{}

// panicMark flags a panic without recovering it, so the SDK's Recover still
// sees the original stack.
func panicMark(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		done := false
		defer func() {
			if !done {
				if p, ok := r.Context().Value(panicKey{}).(*bool); ok {
					*p = true
				}
			}
		}()
		next.ServeHTTP(w, r)
		done = true
	})
}

// routePattern avoids putting ids into issue titles; the path is in the body.
func routePattern(r *http.Request) string {
	if p := r.Pattern; p != "" {
		return p
	}
	return r.URL.Path
}

type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.status, w.wroteHeader = code, true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	w.wroteHeader = true
	return w.ResponseWriter.Write(p)
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

type onceWindow struct {
	mu     sync.Mutex
	window time.Duration
	seen   map[string]time.Time
}

func newOnceWindow(d time.Duration) *onceWindow {
	return &onceWindow{window: d, seen: map[string]time.Time{}}
}

// first reports whether key has not been seen within the window.
func (o *onceWindow) first(key string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	now := time.Now()
	if t, ok := o.seen[key]; ok && now.Sub(t) < o.window {
		return false
	}
	if len(o.seen) > 1000 {
		o.seen = map[string]time.Time{}
	}
	o.seen[key] = now
	return true
}
