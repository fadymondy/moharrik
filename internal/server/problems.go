package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
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

Configured from MAHAAM_URL / MAHAAM_FEEDBACK_KEY / MAHAAM_APP_URL, and only
when APP_ENV=production. Otherwise, or with no key, it is a no-op apart from
panic recovery.
*/
func installProblemReports(k *togo.Kernel) {
	// Only production files to the board (MH-479). Worktrees copy the main
	// .env with its feedback key, so a half-migrated local database or a
	// branch mid-change used to land here as a high-priority production bug.
	// Disabled still recovers panics; it only stops the report.
	c := feedback.New(feedback.Config{Disabled: !problemReportsEnabled(os.Getenv("APP_ENV"))})
	dedupe := newOnceWindow(10 * time.Minute)
	// Which deployment answered. The report's page URL is MAHAAM_APP_URL, a
	// fixed origin the feedback key allows, so every report read "on
	// app.moharrik.com" even when a developer's local API filed it — MH-472..474
	// could not be reproduced on production for exactly that reason. The body
	// names the real one.
	servedBy := strings.TrimSpace(os.Getenv("APP_URL"))
	if servedBy == "" {
		servedBy = "unknown (APP_URL unset)"
	}

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
					Body:     fmt.Sprintf("%s %s answered %d.%s\n\nServed by %s (Host: %s).", r.Method, r.URL.Path, sw.status, problemDetail(sw.body), servedBy, r.Host),
					Type:     "bug",
					Priority: "high",
					Extra:    map[string]string{"route": r.URL.Path, "user_agent": r.UserAgent()},
				})
			}(context.WithoutCancel(r.Context()))
		})
	})
}

// problemReportsEnabled: only a production deployment files its failures.
func problemReportsEnabled(appEnv string) bool {
	return strings.EqualFold(strings.TrimSpace(appEnv), "production")
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
	// body keeps the start of a 5xx answer, so the report can say WHY (the
	// problem's detail) rather than only "answered 502" — MH-529/530 could not
	// be diagnosed from the status alone.
	body []byte
}

const maxProblemBody = 4 << 10

var longDigits = regexp.MustCompile(`\d{8,}`)

// problemDetail is the "detail" of a problem+json answer, one line, with long
// digit runs (phone numbers) masked. Empty when the answer carried none.
func problemDetail(raw []byte) string {
	var p struct {
		Detail string `json:"detail"`
	}
	if json.Unmarshal(raw, &p) != nil || strings.TrimSpace(p.Detail) == "" {
		return ""
	}
	d := longDigits.ReplaceAllString(strings.Join(strings.Fields(p.Detail), " "), "<number>")
	if len(d) > 300 {
		d = d[:300] + "…"
	}
	return "\n\nDetail: " + d
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.status, w.wroteHeader = code, true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	w.wroteHeader = true
	if w.status >= 500 && len(w.body) < maxProblemBody {
		w.body = append(w.body, p[:min(len(p), maxProblemBody-len(w.body))]...)
	}
	return w.ResponseWriter.Write(p)
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Hijack passes the connection through, which is what lets a WebSocket upgrade
// happen underneath this middleware. Without it the wrapper silently hides the
// underlying Hijacker and gorilla answers 500 — the browser socket
// (/api/agents/ws) failed exactly that way, and the failure looks like a server
// bug rather than a missing method.
func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	w.status, w.wroteHeader = http.StatusSwitchingProtocols, true
	return h.Hijack()
}

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
