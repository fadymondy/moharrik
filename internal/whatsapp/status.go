package whatsapp

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
The WhatsApp link's two operations the settings page and the MCP tools share
(MH-409): reading the state, and starting a link. routes.go's handlers are thin
wrappers around these.
*/

// WebhookHealth says whether Evolution will deliver to this app.
type WebhookHealth struct {
	// ok | not_set | wrong_url | disabled | unknown | not_linked
	Status string `json:"status" doc:"ok | not_set | wrong_url | disabled | unknown | not_linked"`
	// Detail says what to do about anything but ok.
	Detail string `json:"detail"`
	// LastDeliveryAt is when Evolution last reached the webhook with the right token.
	LastDeliveryAt *time.Time `json:"last_delivery_at"`
}

// Status reads the workspace's link: the live connection state, the number,
// the webhook's health and today's sends (in the workspace's time zone).
func Status(ctx context.Context, a *app.App) (*LinkOut, error) {
	s, err := scope(ctx, authz.InboxRead)
	if err != nil {
		return nil, err
	}
	cfg := LoadConfig()
	// "close" rather than "": the page renders this straight into a label
	// key, and an empty state showed the raw key instead of "Not linked".
	o := &LinkOut{Available: cfg.Configured(), State: "close",
		Webhook: WebhookHealth{Status: "not_linked", Detail: "no number is linked"}}
	if counts, err := CountSince(ctx, a, s.WorkspaceID, startOfDay(ctx, a, s.WorkspaceID)); err == nil {
		o.SentToday, o.FailedToday = counts.Sent, counts.Failed
	}
	l, err := Load(ctx, a, s.WorkspaceID)
	if errors.Is(err, ErrNoLink) {
		return o, nil
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("could not read the WhatsApp link")
	}
	o.Linked, o.Phone, o.LastError, o.LinkedAt = true, l.Phone, l.LastError, l.LinkedAt
	o.State = firstNonEmpty(l.State, "close")
	o.Webhook = WebhookHealth{Status: "unknown", LastDeliveryAt: lastHookAt(ctx, a, s.WorkspaceID)}

	if !cfg.Configured() {
		o.Webhook.Detail = ErrNotConfigured.Error()
		return o, nil
	}
	c := ClientFor(cfg, l)
	// The live state, not the stored one: a phone that was unlinked from
	// the handset shows as open here for ever otherwise.
	if state, err := c.State(ctx); err == nil {
		o.State = state
		if state != l.State {
			SetState(ctx, a, s.WorkspaceID, state, "")
		}
	}
	want := ""
	if cfg.HookBase != "" {
		want = cfg.HookBase + "/api/whatsapp/hook/" + l.HookID
	}
	o.Webhook.Status, o.Webhook.Detail = webhookVerdict(ctx, c, want)
	return o, nil
}

func webhookVerdict(ctx context.Context, c *Client, want string) (string, string) {
	wh, err := c.Webhook(ctx)
	switch {
	case err != nil:
		return "unknown", "could not ask the WhatsApp server: " + err.Error()
	case wh.URL == "":
		return "not_set", "the WhatsApp server has no webhook for this number: link it again"
	case !wh.Enabled:
		return "disabled", "the webhook is switched off on the WhatsApp server: link it again"
	case want != "" && !strings.EqualFold(strings.TrimRight(wh.URL, "/"), want):
		// The URL carries the hook id, which is the webhook's authorization,
		// so it is never echoed back.
		return "wrong_url", "the webhook points somewhere else: link it again"
	}
	return "ok", ""
}

// StartLink links a number: it creates (or reconnects) the Evolution instance,
// points its webhook here, and returns the QR code to scan — or, when a number
// is given, a pairing code to type on that phone instead.
func StartLink(ctx context.Context, a *app.App, number string) (*LinkOut, error) {
	// Linking a number is the same weight as creating a chat widget: it
	// decides what can write into this workspace's inbox.
	s, err := scope(ctx, authz.InboxManage)
	if err != nil {
		return nil, err
	}
	cfg := LoadConfig()
	if !cfg.Configured() {
		return nil, huma.Error503ServiceUnavailable(ErrNotConfigured.Error())
	}
	if cfg.HookBase == "" {
		return nil, huma.Error503ServiceUnavailable("this deployment has no public URL set (APP_URL), so WhatsApp could not call back")
	}
	number = Number(number)
	if number != "" && (len(number) < 8 || len(number) > 15) {
		return nil, huma.Error422UnprocessableEntity("the number must be 8 to 15 digits with its country code, e.g. 201012345678")
	}

	l, err := Ensure(ctx, a, s.WorkspaceID, s.UserID)
	if err != nil {
		slog.Warn("whatsapp: link: could not prepare the link row", "workspace", s.WorkspaceID, "err", masked(err))
		return nil, huma.Error500InternalServerError("could not prepare the link")
	}
	c := ClientFor(cfg, l)

	// Create, or connect if the instance is already there. Evolution
	// answers 403 for an existing instance, which is not an error here.
	qr, err := c.Create(ctx, number)
	if err != nil || (number != "" && qr.PairingCode == "") {
		if qr, err = c.Connect(ctx, number); err != nil && retryable(err) {
			// An instance Evolution is still starting (a create that timed
			// out, a restart) answers the first connect with a 5xx or not at
			// all; one more try a moment later is usually the QR (MH-529).
			select {
			case <-ctx.Done():
			case <-time.After(2 * time.Second):
				qr, err = c.Connect(ctx, number)
			}
		}
		if err != nil {
			slog.Warn("whatsapp: link: connect failed", "workspace", s.WorkspaceID, "err", masked(err))
			SetError(ctx, a, s.WorkspaceID, err)
			return nil, evolutionErr(err)
		}
	}
	hookURL := cfg.HookBase + "/api/whatsapp/hook/" + l.HookID
	if err := c.SetWebhook(ctx, hookURL, l.Token); err != nil {
		// Without the webhook nothing would ever arrive, so this is a
		// failure of the whole link rather than a detail.
		slog.Warn("whatsapp: link: setting the webhook failed", "workspace", s.WorkspaceID, "err", masked(err))
		SetError(ctx, a, s.WorkspaceID, err)
		return nil, evolutionErr(err)
	}

	state, _ := c.State(ctx)
	SetState(ctx, a, s.WorkspaceID, firstNonEmpty(state, "connecting"), "")
	return &LinkOut{
		Linked: true, State: firstNonEmpty(state, "connecting"),
		QRBase64: qr.Base64, QRCode: qr.Code, PairingCode: qr.PairingCode, Available: true,
		Webhook: WebhookHealth{Status: "ok", LastDeliveryAt: lastHookAt(ctx, a, s.WorkspaceID)},
	}, nil
}

func lastHookAt(ctx context.Context, a *app.App, workspaceID string) *time.Time {
	var t *time.Time
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	_ = a.SQLDB.QueryRowContext(sctx, `SELECT last_hook_at FROM whatsapp_links WHERE workspace_id = $1`, workspaceID).Scan(&t)
	return t
}

// startOfDay is midnight today in the workspace's time zone ("the day" a
// person in that workspace means), falling back to UTC.
func startOfDay(ctx context.Context, a *app.App, workspaceID string) time.Time {
	tz := "UTC"
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	_ = a.SQLDB.QueryRowContext(sctx, `SELECT timezone FROM workspaces WHERE id = $1`, workspaceID).Scan(&tz)
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	now := time.Now().In(loc)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
}

// WebhookConfig is what Evolution says the instance's webhook is.
type WebhookConfig struct {
	Enabled bool     `json:"enabled"`
	URL     string   `json:"url"`
	Events  []string `json:"events"`
}

// Webhook reads the instance's webhook. Evolution answers null when none is set.
func (c *Client) Webhook(ctx context.Context) (WebhookConfig, error) {
	var out *WebhookConfig
	if err := c.do(ctx, http.MethodGet, "/webhook/find/"+c.inst(), nil, &out); err != nil {
		if isNotFound(err) {
			return WebhookConfig{}, nil
		}
		return WebhookConfig{}, err
	}
	if out == nil {
		return WebhookConfig{}, nil
	}
	return *out, nil
}

// MarkHook records that Evolution reached the webhook with the right token.
func MarkHook(ctx context.Context, a *app.App, workspaceID string) {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	_, _ = a.SQLDB.ExecContext(sctx, `UPDATE whatsapp_links SET last_hook_at = now() WHERE workspace_id = $1`, workspaceID)
}

// retryable is an Evolution failure worth one more try: nothing got there, or
// the server itself failed. A 4xx is an answer, and asking again changes nothing.
func retryable(err error) bool {
	var e *Error
	return errors.As(err, &e) && (e.Unreachable || e.Status >= 500)
}

var longDigits = regexp.MustCompile(`\d{8,}`)

// masked is an error as a log line may carry it: Evolution's messages can echo
// the number being linked, and a phone number does not belong in the logs.
func masked(err error) string {
	return longDigits.ReplaceAllString(err.Error(), "<number>")
}
