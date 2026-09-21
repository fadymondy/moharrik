package whatsapp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/tenant"
)

var tags = []string{"WhatsApp"}

// LinkOut is what the settings page reads. The webhook token is never in it.
type LinkOut struct {
	Linked    bool       `json:"linked"`
	State     string     `json:"state" doc:"open | connecting | close — read live from the WhatsApp server."`
	Phone     string     `json:"phone"`
	QRBase64  string     `json:"qr_base64,omitempty" doc:"Present while a phone still has to scan."`
	QRCode    string     `json:"qr_code,omitempty"`
	LastError string     `json:"last_error"`
	LinkedAt  *time.Time `json:"linked_at"`
	Available bool       `json:"available" doc:"false when this deployment has no WhatsApp server configured."`
}

type wsHeader struct {
	WorkspaceID string `header:"X-Workspace-ID" doc:"Workspace id or slug. Non-members get 404."`
}

type body[T any] struct{ Body T }

func out[T any](v T) *body[T] { return &body[T]{Body: v} }

func scope(ctx context.Context, action string) (*tenant.Scope, error) {
	s, ok := tenant.From(ctx)
	if !ok {
		return nil, huma.Error404NotFound("workspace not found")
	}
	if !authz.Can(s.Role, action) {
		return nil, huma.Error403Forbidden("your role cannot do this in this workspace")
	}
	return s, nil
}

// Inbound delivery is a function the inbox provides, so this package never
// imports it: internal/inbox already imports nothing from here either, and one
// of the two had to stay unaware of the other.
type Filer func(ctx context.Context, workspaceID, phone, name, text, externalID string) error

var file Filer

// SetFiler wires the inbox's own "file this message" in at boot.
func SetFiler(f Filer) { file = f }

// Register mounts the workspace-facing endpoints. `a` may be nil (OpenAPI
// export).
func Register(api huma.API, a *app.App) {
	cfg := LoadConfig()

	huma.Register(api, huma.Operation{
		OperationID: "w-whatsapp", Method: http.MethodGet, Path: "/api/w/whatsapp",
		Summary: "The WhatsApp number linked to this workspace", Tags: tags,
	}, func(ctx context.Context, in *struct{ wsHeader }) (*body[LinkOut], error) {
		s, err := scope(ctx, authz.InboxRead)
		if err != nil {
			return nil, err
		}
		// "close" rather than "": the page renders this straight into a label
		// key, and an empty state showed the raw key instead of "Not linked".
		o := LinkOut{Available: cfg.Configured(), State: "close"}
		l, err := Load(ctx, a, s.WorkspaceID)
		if errors.Is(err, ErrNoLink) {
			return out(o), nil
		}
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the WhatsApp link")
		}
		o.Linked, o.Phone, o.LastError, o.LinkedAt = true, l.Phone, l.LastError, l.LinkedAt
		o.State = firstNonEmpty(l.State, "close")

		// The live state, not the stored one: a phone that was unlinked from
		// the handset shows as open here for ever otherwise.
		if cfg.Configured() {
			if state, err := ClientFor(cfg, l).State(ctx); err == nil {
				o.State = state
				if state != l.State {
					SetState(ctx, a, s.WorkspaceID, state, "")
				}
			}
		}
		return out(o), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-whatsapp-link", Method: http.MethodPost, Path: "/api/w/whatsapp/link",
		Summary: "Link a WhatsApp number: returns a QR code to scan", Tags: tags,
	}, func(ctx context.Context, in *struct{ wsHeader }) (*body[LinkOut], error) {
		// Linking a number is the same weight as creating a chat widget: it
		// decides what can write into this workspace's inbox.
		s, err := scope(ctx, authz.InboxManage)
		if err != nil {
			return nil, err
		}
		if !cfg.Configured() {
			return nil, huma.Error503ServiceUnavailable(ErrNotConfigured.Error())
		}
		if cfg.HookBase == "" {
			return nil, huma.Error503ServiceUnavailable("this deployment has no public URL set (APP_URL), so WhatsApp could not call back")
		}

		l, err := Ensure(ctx, a, s.WorkspaceID, s.UserID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not prepare the link")
		}
		c := ClientFor(cfg, l)

		// Create, or connect if the instance is already there. Evolution
		// answers 403 for an existing instance, which is not an error here.
		qr, err := c.Create(ctx)
		if err != nil {
			if qr, err = c.Connect(ctx); err != nil {
				SetError(ctx, a, s.WorkspaceID, err)
				return nil, evolutionErr(err)
			}
		}
		hookURL := cfg.HookBase + "/api/whatsapp/hook/" + l.HookID
		if err := c.SetWebhook(ctx, hookURL, l.Token); err != nil {
			// Without the webhook nothing would ever arrive, so this is a
			// failure of the whole link rather than a detail.
			SetError(ctx, a, s.WorkspaceID, err)
			return nil, evolutionErr(err)
		}

		state, _ := c.State(ctx)
		SetState(ctx, a, s.WorkspaceID, firstNonEmpty(state, "connecting"), "")
		return out(LinkOut{
			Linked: true, State: firstNonEmpty(state, "connecting"),
			QRBase64: qr.Base64, QRCode: qr.Code, Available: true,
		}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-whatsapp-unlink", Method: http.MethodDelete, Path: "/api/w/whatsapp",
		DefaultStatus: http.StatusNoContent,
		Summary:       "Unlink this workspace's WhatsApp number", Tags: tags,
	}, func(ctx context.Context, in *struct{ wsHeader }) (*struct{}, error) {
		s, err := scope(ctx, authz.InboxManage)
		if err != nil {
			return nil, err
		}
		l, err := Load(ctx, a, s.WorkspaceID)
		if errors.Is(err, ErrNoLink) {
			return nil, nil // already unlinked
		}
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the WhatsApp link")
		}
		if cfg.Configured() {
			// Delete the instance FIRST: dropping our row while Evolution still
			// holds the phone session would leave a linked number nobody can
			// see or unlink.
			if err := ClientFor(cfg, l).Delete(ctx); err != nil {
				return nil, evolutionErr(err)
			}
		}
		if err := Unlink(ctx, a, s.WorkspaceID); err != nil {
			return nil, huma.Error500InternalServerError("could not unlink")
		}
		return nil, nil
	})
}

// RegisterHook mounts the public webhook on the router.
//
// It is NOT a huma route: Evolution posts a body shape that varies by event,
// and modelling every variant as a schema would mean rejecting deliveries this
// app simply has no use for. The guard gives /api/whatsapp/hook/ the public
// tier; the unguessable id in the path plus the token header are the
// authorization, exactly like /api/hooks/ and /api/chat/.
func RegisterHook(r chi.Router, a *app.App) {
	r.Post("/api/whatsapp/hook/{id}", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		l, err := ByHookID(ctx, a, chi.URLParam(r, "id"))
		if err != nil {
			// An unknown id and a bad token answer the same way: whether a link
			// exists is not a caller's business.
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if !SameToken(r.Header.Get(TokenHeader), l.Token) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		raw, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		var p Payload
		if json.Unmarshal(raw, &p) != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		switch strings.ToLower(p.Event) {
		case "messages.upsert":
			var m Message
			if json.Unmarshal(p.Data, &m) != nil {
				break
			}
			in, ok := m.Read()
			if !ok || in.FromMe {
				// Our own echo, a group, or nothing to show. Accepted and
				// dropped: answering an error would make Evolution retry
				// something that will never be wanted.
				break
			}
			if file != nil {
				if err := file(ctx, l.WorkspaceID, in.Phone, in.Name, in.Text, in.ID); err != nil {
					// 500 so Evolution retries: the message is real and losing
					// it means losing a customer's words.
					http.Error(w, "could not file", http.StatusInternalServerError)
					return
				}
			}
		case "connection.update":
			var c Connection
			if json.Unmarshal(p.Data, &c) == nil {
				SetState(ctx, a, l.WorkspaceID, strings.ToLower(firstNonEmpty(c.State, "close")), c.WUID)
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
}

// Send delivers one reply as this workspace's linked number and returns
// WhatsApp's id for it.
func Send(ctx context.Context, a *app.App, workspaceID, phone, text string) (string, error) {
	cfg := LoadConfig()
	if !cfg.Configured() {
		return "", ErrNotConfigured
	}
	l, err := Load(ctx, a, workspaceID)
	if err != nil {
		return "", err
	}
	return ClientFor(cfg, l).SendText(ctx, phone, text)
}

func evolutionErr(err error) error {
	var e *Error
	switch {
	case errors.Is(err, ErrNotConfigured):
		return huma.Error503ServiceUnavailable(err.Error())
	case errors.As(err, &e):
		return huma.Error502BadGateway(e.Error())
	}
	return huma.Error500InternalServerError("the WhatsApp server could not be reached")
}
