package automation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/pgtext"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
The element picker (MH-400): someone editing a workflow clicks the element on
the LIVE page, in their own paired browser, and gets back a selector that will
still find it tomorrow — instead of reading one out of DevTools.

It is a DIRECT COMMAND to one browser, not a run: nothing is recorded, nothing
is typed or clicked through (the extension swallows the picking click), and the
tab it opens is closed again. It still passes every guardrail a run does,
because it opens a page in a browser that is signed in to someone's accounts:

	KILL SWITCH   refused while automation is stopped; stopping drops the
	              socket, which ends a pick in flight
	REVOKED       a revoked browser picks nothing
	ALLOWED SITES the page must be on the browser's allowed sites, checked
	              here before anything is sent and again in the extension
	              where the page actually lands (a redirect off the list
	              stops the pick, as it stops a run)
	ONE AT A TIME a browser running something is not interrupted by a pick

A pick waits for a PERSON, which can take a minute. A request held open that
long dies in proxies (NPM's default read timeout is 60 seconds), so it is two
calls: POST starts it and answers at once with an id, GET reads how it went.
The state is in this process's memory, like the socket registry it depends
on, and is forgotten a few minutes after it finishes.
*/

// PickTimeout is how long the extension waits for the click.
const PickTimeout = 90 * time.Second

// How long a finished pick can still be read.
const pickKeep = 5 * time.Minute

// PickResult is what the browser sends back.
type PickResult struct {
	Selector string `json:"selector"`
	// Fragile marks a structural fallback (nth-of-type) — the page offered
	// nothing more durable, and the editor says so.
	Fragile bool   `json:"fragile,omitempty"`
	Tag     string `json:"tag,omitempty"`
	Text    string `json:"text,omitempty"`
	Host    string `json:"host,omitempty"`
}

// Pick is one pick's state.
type Pick struct {
	ID        string      `json:"id"`
	AgentID   string      `json:"agent_id"`
	Status    string      `json:"status" doc:"waiting | picked | failed"`
	Result    *PickResult `json:"result,omitempty"`
	Error     string      `json:"error,omitempty"`
	StartedAt time.Time   `json:"started_at"`

	workspaceID string
	doneAt      time.Time
}

type pickBook struct {
	mu    sync.Mutex
	picks map[string]*Pick
}

var picks = &pickBook{picks: map[string]*Pick{}}

func (b *pickBook) put(p *Pick) {
	b.mu.Lock()
	defer b.mu.Unlock()
	// Swept on write rather than by a goroutine: picks are rare, and a map that
	// only grows while someone is actively picking needs nothing cleverer.
	for id, old := range b.picks {
		if !old.doneAt.IsZero() && time.Since(old.doneAt) > pickKeep {
			delete(b.picks, id)
		}
	}
	b.picks[p.ID] = p
}

// get returns a copy, and only to the workspace that started it.
func (b *pickBook) get(workspaceID, id string) (Pick, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	p, ok := b.picks[id]
	if !ok || p.workspaceID != workspaceID {
		return Pick{}, false
	}
	cp := *p
	if p.Result != nil {
		r := *p.Result
		cp.Result = &r
	}
	return cp, true
}

func (b *pickBook) finish(id string, res *PickResult, errText string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	p, ok := b.picks[id]
	if !ok {
		return
	}
	p.doneAt = time.Now()
	if errText != "" {
		p.Status, p.Error = "failed", errText
		return
	}
	p.Status, p.Result = "picked", res
}

// ErrPickURL is a pick with nowhere to open.
var ErrPickURL = errors.New("the picker needs the address of a web page")

// StartPick checks the guardrails and sends the pick to the browser. It
// returns as soon as the browser has been asked; the answer is read with
// GetPick.
func (h *Hub) StartPick(ctx context.Context, a *app.App, workspaceID, userID, agentID, rawURL string) (*Pick, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" || strings.Contains(rawURL, "{{") {
		// A placeholder is only known at run time; the picker needs a real page.
		return nil, ErrPickURL
	}
	if !strings.Contains(rawURL, "://") {
		rawURL = "https://" + rawURL
	}
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		return nil, ErrPickURL
	}

	if paused, why, _ := IsPaused(ctx, a, workspaceID); paused {
		if why != "" {
			return nil, fmt.Errorf("%w: %s", ErrPaused, why)
		}
		return nil, ErrPaused
	}
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	var hosts pgtext.Array
	var revoked sql.NullTime
	err := a.SQLDB.QueryRowContext(sctx, `
		SELECT allowed_hosts, revoked_at FROM browser_agents WHERE workspace_id = $1 AND id = $2`,
		workspaceID, agentID).Scan(&hosts, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("no such browser")
	}
	if err != nil {
		return nil, err
	}
	if revoked.Valid {
		return nil, ErrRevoked
	}
	if !h.Online(agentID) {
		return nil, ErrOffline
	}
	host, _ := StepHost("goto", map[string]any{"url": rawURL})
	if !HostAllowed(hosts, host) {
		return nil, &StepError{Position: -1, Host: host, Err: ErrHostNotAllowed}
	}
	var live int
	if err := a.SQLDB.QueryRowContext(sctx, `
		SELECT count(*) FROM automation_runs
		WHERE workspace_id = $1 AND agent_id = $2 AND status IN ('queued', 'running')`,
		workspaceID, agentID).Scan(&live); err != nil {
		return nil, err
	}
	if live > 0 {
		return nil, ErrBusy
	}

	p := &Pick{ID: newID(), AgentID: agentID, Status: "waiting", StartedAt: time.Now().UTC(), workspaceID: workspaceID}
	picks.put(p)
	// What the caller gets is a copy taken now: the book's own entry is
	// written by the goroutine below.
	answer := *p

	list := []string(hosts)
	if list == nil {
		list = []string{}
	}
	payload, _ := json.Marshal(map[string]any{
		"url":           rawURL,
		"allowed_hosts": list,
		"timeout_ms":    PickTimeout.Milliseconds(),
	})
	// Who opened a page in whose browser is worth a line: a pick leaves no run
	// record behind.
	a.Log.Info("pick requested", "workspace", workspaceID, "agent", agentID, "user", userID, "host", host)

	go func() {
		// Detached from the request, which has already been answered. The
		// deadline is the extension's own plus the round trip.
		cctx, cancel := context.WithTimeout(context.Background(), PickTimeout+20*time.Second)
		defer cancel()
		reply, err := h.Call(cctx, workspaceID, agentID, Envelope{Type: "pick", Payload: payload}, PickTimeout+15*time.Second)
		if err != nil {
			msg := err.Error()
			if errors.Is(err, ErrOffline) {
				msg = "the browser went away before anything was picked"
			}
			picks.finish(p.ID, nil, msg)
			return
		}
		var res PickResult
		if json.Unmarshal(reply.Payload, &res) != nil || strings.TrimSpace(res.Selector) == "" {
			picks.finish(p.ID, nil, "the browser could not make a selector for that element")
			return
		}
		res.Text = trim(strings.Join(strings.Fields(res.Text), " "), 80)
		picks.finish(p.ID, &res, "")
	}()
	return &answer, nil
}

// RegisterPick mounts the picker routes.
func RegisterPick(api huma.API, a *app.App, hub *Hub) {
	huma.Register(api, huma.Operation{
		OperationID: "w-browser-pick", Method: http.MethodPost, Path: "/api/w/browsers/{id}/pick",
		DefaultStatus: http.StatusAccepted,
		Summary:       "Ask a paired browser to let someone click an element on a live page (MH-400)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body struct {
			URL string `json:"url" minLength:"1" maxLength:"2000" doc:"The page to pick on. It must be on the browser's allowed sites."`
		}
	}) (*body[struct {
		Pick Pick `json:"pick"`
	}], error) {
		// automation:run, like editing a workflow: a pick opens a page in a
		// signed-in browser, which is not a lighter act than reading one.
		s, err := scope(ctx, authz.AutomationRun)
		if err != nil {
			return nil, err
		}
		p, err := hub.StartPick(ctx, a, s.WorkspaceID, s.UserID, in.ID, in.Body.URL)
		if err != nil {
			if errors.Is(err, ErrPickURL) {
				return nil, huma.Error422UnprocessableEntity(err.Error())
			}
			var se *StepError
			if errors.As(err, &se) {
				return nil, huma.Error422UnprocessableEntity(se.Host + " is not on this browser's allowed sites")
			}
			return nil, runError(err)
		}
		return out(struct {
			Pick Pick `json:"pick"`
		}{*p}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-browser-pick-status", Method: http.MethodGet, Path: "/api/w/browsers/{id}/pick/{pick}",
		Summary: "How a pick went: waiting, picked (with the selector) or failed", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID     string `path:"id"`
		PickID string `path:"pick"`
	}) (*body[struct {
		Pick Pick `json:"pick"`
	}], error) {
		s, err := scope(ctx, authz.AutomationRun)
		if err != nil {
			return nil, err
		}
		p, ok := picks.get(s.WorkspaceID, in.PickID)
		if !ok || p.AgentID != in.ID {
			return nil, huma.Error404NotFound("no such pick")
		}
		return out(struct {
			Pick Pick `json:"pick"`
		}{p}), nil
	})
}
