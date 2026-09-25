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
	"github.com/fadymondy/moharrik/internal/recency"
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
	// PairingCode is the alternative to scanning, when a number was given.
	PairingCode string     `json:"pairing_code,omitempty" doc:"Type this on the phone (Linked devices → Link with phone number) instead of scanning."`
	LastError   string     `json:"last_error"`
	LinkedAt    *time.Time `json:"linked_at"`
	Available   bool       `json:"available" doc:"false when this deployment has no WhatsApp server configured."`
	// Webhook is whether Evolution will deliver inbound messages here (MH-409).
	Webhook WebhookHealth `json:"webhook"`
	// SentToday / FailedToday count this number's sends since midnight in the
	// workspace's time zone: replies, broadcasts and agent messages alike.
	SentToday   int64 `json:"sent_today"`
	FailedToday int64 `json:"failed_today"`
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
		Summary: "The WhatsApp number linked to this workspace: state, webhook health and today's sends", Tags: tags,
	}, func(ctx context.Context, in *struct{ wsHeader }) (*body[LinkOut], error) {
		o, err := Status(ctx, a)
		if err != nil {
			return nil, err
		}
		return out(*o), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-whatsapp-link", Method: http.MethodPost, Path: "/api/w/whatsapp/link",
		Summary: "Link a WhatsApp number: returns a QR code to scan (or a pairing code, given the number)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body *struct {
			Number string `json:"number,omitempty" maxLength:"32" doc:"The number to link, with country code, to get a pairing code instead of a QR."`
		}
	}) (*body[LinkOut], error) {
		number := ""
		if in.Body != nil {
			number = in.Body.Number
		}
		o, err := StartLink(ctx, a, number)
		if err != nil {
			return nil, err
		}
		return out(*o), nil
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

		// A delivery with the right token is what "the webhook works" means.
		MarkHook(ctx, a, l.WorkspaceID)

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
			if !ok || in.FromMe || l.tooOld(m.Timestamp) {
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

// tooOld: a message sent before this number was linked (less the grace) is
// history Evolution replayed on a reconnect or first sync, not new mail —
// see internal/recency. Bringing history in is the import's job (MH-562).
func (l *Link) tooOld(unix int64) bool {
	if l.LinkedAt == nil || unix <= 0 {
		return false
	}
	return recency.TooOld(time.Unix(unix, 0), *l.LinkedAt)
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
