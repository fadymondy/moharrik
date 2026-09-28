package whatsapp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
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
//
// media is nil for a plain text message; set (MH-658), it is the inbox's cue
// to download it (via a *Client this package hands it, since only this
// package knows the workspace's Evolution instance/link) and file it as a
// message_attachments row alongside the message.
type Filer func(ctx context.Context, workspaceID, phone, name, text, externalID string, media *Media, download MediaDownloader) error

// MediaDownloader downloads one message's media, bound to the workspace's own
// Evolution instance — the inbox package files attachments but has no
// business knowing an instance name or a webhook token.
type MediaDownloader func(ctx context.Context) (MediaFile, error)

var file Filer

// SetFiler wires the inbox's own "file this message" in at boot.
func SetFiler(f Filer) { file = f }

// OwnFiler files a message the linked number itself sent (MH-665): a reply
// typed on the phone, or the echo of something this app sent. The inbox
// tells the two apart by the WhatsApp message id.
type OwnFiler func(ctx context.Context, workspaceID, phone, text, externalID string, media *Media, download MediaDownloader) error

var fileOwn OwnFiler

// SetOwnFiler wires the inbox's "file our own message" in at boot.
func SetOwnFiler(f OwnFiler) { fileOwn = f }

// TypingSink hands a contact's presence (composing, recording, paused, …) to
// the inbox (MH-660). Never stored — the inbox turns it straight into a
// realtime event and nothing else.
type TypingSink func(ctx context.Context, workspaceID, phone, state string) error

var typingSink TypingSink

// SetTypingSink wires the inbox's typing handler in at boot.
func SetTypingSink(f TypingSink) { typingSink = f }

// ReactionSink files an emoji reaction against the message it reacts to
// (MH-664), found by WhatsApp's id for it. emoji "" removes the reaction.
// fromMe is the linked number reacting — from this app (the echo of its own
// sendReaction) or typed on the phone. A reaction is never a message row.
type ReactionSink func(ctx context.Context, workspaceID, phone, targetExternalID, emoji string, fromMe bool) error

var reactionSink ReactionSink

// SetReactionSink wires the inbox's reaction handler in at boot.
func SetReactionSink(f ReactionSink) { reactionSink = f }

// OwnDelay is how long a fromMe message waits before it is filed. The echo of
// a reply sent from the inbox can beat the reply's own row to the database —
// Evolution answers the send and fires the webhook at nearly the same moment
// — and waiting a beat lets the reply claim its id first, so the echo finds
// it and adds nothing. A variable so the tests need not wait.
var OwnDelay = 4 * time.Second

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
	cfg := LoadConfig()
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
		refreshEvents(a, l)

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
			if rx, ok := m.Reaction(); ok {
				// An emoji on an earlier message (MH-664), the customer's or
				// the linked number's own (from the phone, or the echo of this
				// app's send — setting the same emoji again changes nothing).
				// Filed against the target, never as a message; a target this
				// app never stored is dropped by the sink.
				if reactionSink != nil && !l.tooOld(m.Timestamp) {
					if err := reactionSink(ctx, l.WorkspaceID, rx.Phone, rx.TargetID, rx.Emoji, rx.FromMe); err != nil {
						http.Error(w, "could not file", http.StatusInternalServerError)
						return
					}
				}
				break
			}
			in, ok := m.Read()
			if !ok || l.tooOld(m.Timestamp) {
				// A group, or nothing to show. Accepted and dropped: answering
				// an error would make Evolution retry something that will
				// never be wanted.
				break
			}
			if in.FromMe {
				// Sent by the linked number (MH-665). Typed on the phone, it is
				// the team's reply and belongs in the thread; the echo of this
				// app's own send is already there and adds nothing. Filed after
				// a beat, off the request, so that echo can never beat the
				// reply's own row (see OwnDelay) — and never retried, since
				// nothing is lost that the phone does not still show.
				if fileOwn != nil {
					ws, bg := l.WorkspaceID, context.WithoutCancel(ctx)
					raw := in.Raw()
					download := MediaDownloader(func(dctx context.Context) (MediaFile, error) {
						return ClientFor(cfg, l).GetMediaBase64(dctx, raw)
					})
					go func() {
						time.Sleep(OwnDelay)
						if err := fileOwn(WithQuote(bg, in.Quote), ws, in.Phone, in.Text, in.ID, in.Media, download); err != nil {
							slog.Warn("whatsapp: hook: could not file a message sent from the phone", "workspace", ws, "err", err)
						}
					}()
				}
				break
			}
			if file != nil {
				raw := in.Raw()
				download := MediaDownloader(func(dctx context.Context) (MediaFile, error) {
					return ClientFor(cfg, l).GetMediaBase64(dctx, raw)
				})
				if err := file(WithQuote(ctx, in.Quote), l.WorkspaceID, in.Phone, in.Name, in.Text, in.ID, in.Media, download); err != nil {
					// 500 so Evolution retries: the message is real and losing
					// it means losing a customer's words.
					http.Error(w, "could not file", http.StatusInternalServerError)
					return
				}
			}
		case "messages.update":
			// Delivery receipts (MH-656). A receipt for a message this app
			// never stored (sent from the phone itself) updates nothing,
			// which is fine; a failed write is logged, not retried — a
			// missed tick is not worth Evolution redelivering.
			if statusSink == nil {
				break
			}
			for _, r := range receipts(p.Data) {
				if id, status, ok := r.Read(); ok {
					if err := statusSink(ctx, l.WorkspaceID, id, status); err != nil {
						slog.Warn("whatsapp: hook: could not record a receipt", "workspace", l.WorkspaceID, "err", err)
					}
				}
			}
		case "presence.update":
			// Typing indicators (MH-660). Ephemeral — never a retry-worthy
			// failure, so a bad or missing sink is just skipped.
			if typingSink != nil {
				var pr Presence
				if json.Unmarshal(p.Data, &pr) == nil {
					if phone, state, ok := pr.Read(); ok {
						if err := typingSink(ctx, l.WorkspaceID, phone, state); err != nil {
							slog.Warn("whatsapp: hook: could not record a presence update", "workspace", l.WorkspaceID, "err", err)
						}
					}
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
		return huma.NewError(http.StatusFailedDependency, e.Error())
	}
	return huma.Error500InternalServerError("the WhatsApp server could not be reached")
}
