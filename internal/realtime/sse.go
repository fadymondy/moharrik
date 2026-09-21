package realtime

import (
	"fmt"
	"net/http"
	"time"
)

// heartbeat keeps the connection warm. Proxies in front of this app (NPM, and
// Cloudflare beyond it) close an idle connection well before a quiet workspace
// would produce an event, and a comment line is the cheapest thing that counts
// as traffic without being an event the client has to understand.
const heartbeat = 25 * time.Second

// Handler streams one workspace's events as SSE.
//
// It is mounted under /api/w/, so the guard has already established a session
// AND membership of X-Workspace-ID before this runs — the workspace id it is
// handed is one the caller is a member of, resolved the same way as for every
// other endpoint. This handler does no authorization of its own, and must not:
// a second, parallel way to decide who may read a workspace is exactly the kind
// of thing that drifts out of step with the first one.
func Handler(hub *Hub, workspaceID func(*http.Request) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		wsID := workspaceID(r)
		if wsID == "" {
			http.Error(w, "workspace not found", http.StatusNotFound)
			return
		}

		h := w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-cache, no-transform")
		h.Set("Connection", "keep-alive")
		// Nginx buffers proxied responses by default, which holds every event
		// until the buffer fills — i.e. exactly the delay this replaces.
		h.Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, ": open\n\n")
		flusher.Flush()

		events, stop := hub.Subscribe(wsID)
		defer stop()

		ticker := time.NewTicker(heartbeat)
		defer ticker.Stop()

		for {
			select {
			case <-r.Context().Done():
				return
			case payload, open := <-events:
				if !open {
					return
				}
				fmt.Fprintf(w, "data: %s\n\n", payload)
				flusher.Flush()
			case <-ticker.C:
				fmt.Fprint(w, ": ping\n\n")
				flusher.Flush()
			}
		}
	}
}
