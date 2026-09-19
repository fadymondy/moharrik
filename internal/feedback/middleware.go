package feedback

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
)

// Middleware injects the script tag before </body> in text/html responses.
// Uncompressed HTML responses are buffered; everything else streams through.
func (c *Client) Middleware(next http.Handler) http.Handler {
	if !c.Enabled() {
		return next
	}
	tag := []byte(c.ScriptTag())
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		iw := &injectWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(iw, r)
		iw.finish(tag)
	})
}

type injectWriter struct {
	http.ResponseWriter
	status    int
	decided   bool
	buffering bool
	hijacked  bool
	buf       bytes.Buffer
}

func (w *injectWriter) decide() {
	if w.decided {
		return
	}
	w.decided = true
	h := w.Header()
	w.buffering = strings.HasPrefix(strings.ToLower(h.Get("Content-Type")), "text/html") && h.Get("Content-Encoding") == ""
	if !w.buffering {
		w.ResponseWriter.WriteHeader(w.status)
	}
}

func (w *injectWriter) WriteHeader(code int) {
	if w.decided {
		return
	}
	w.status = code
	w.decide()
}

func (w *injectWriter) Write(p []byte) (int, error) {
	if !w.decided {
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", http.DetectContentType(p))
		}
		w.decide()
	}
	if w.buffering {
		return w.buf.Write(p)
	}
	return w.ResponseWriter.Write(p)
}

func (w *injectWriter) Flush() {
	if w.buffering {
		return
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *injectWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("mahaam feedback: hijack not supported")
	}
	w.hijacked = true
	return h.Hijack()
}

func (w *injectWriter) finish(tag []byte) {
	if w.hijacked {
		return
	}
	if !w.decided {
		w.decide()
	}
	if !w.buffering {
		return
	}
	body := w.buf.Bytes()
	if i := bytes.LastIndex(bytes.ToLower(body), []byte("</body>")); i >= 0 {
		out := make([]byte, 0, len(body)+len(tag))
		out = append(out, body[:i]...)
		out = append(out, tag...)
		body = append(out, body[i:]...)
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.ResponseWriter.WriteHeader(w.status)
	_, _ = w.ResponseWriter.Write(body)
}

// Recover reports panics as "bug" issues, then responds 500.
// http.ErrAbortHandler is re-panicked untouched.
func (c *Client) Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if v == http.ErrAbortHandler {
				panic(v)
			}
			if c.Enabled() {
				_, _ = c.Report(context.WithoutCancel(r.Context()), Report{
					Title:    fmt.Sprintf("panic: %v", v),
					Body:     fmt.Sprintf("%s %s\n\n%s", r.Method, r.URL.RequestURI(), debug.Stack()),
					Type:     "bug",
					Priority: "high",
					Extra:    map[string]string{"route": r.URL.Path, "user_agent": r.UserAgent()},
				})
			}
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		}()
		next.ServeHTTP(w, r)
	})
}
