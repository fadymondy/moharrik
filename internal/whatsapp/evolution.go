/*
Package whatsapp bridges the inbox to WhatsApp through an Evolution API server
(MH-134).

One INSTANCE per workspace, named after the workspace id: two workspaces can
never share a WhatsApp number, and an instance name cannot be guessed into.
Evolution is the thing holding the phone session; this package holds only the
link between a workspace and its instance, plus the two directions of traffic.

Ported from fadymondy.com's internal/whatsapp, cut down to what an inbox needs.
Everything about a personal assistant (self-chat commands, digests, quiet
hours, allowed contacts) is deliberately left behind: here a WhatsApp message
is a customer writing in, and nothing else.

Where the Evolution server is, and its global key, come from the environment
(EVOLUTION_API_URL, EVOLUTION_API_KEY) — one server serves every workspace, the
way one SMTP relay could. Per-workspace configuration is the instance, not the
server.
*/
package whatsapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Config is where the Evolution server is. Empty URL or key means the feature
// is not available, and every endpoint says so rather than half-working.
type Config struct {
	URL string
	Key string
	// HookBase is the public origin Evolution should call back on, e.g.
	// https://app.moharrik.com. It is separate from APP_URL because Evolution
	// reaches this app over the internal network in some deployments.
	HookBase string
}

func LoadConfig() Config {
	return Config{
		URL:      strings.TrimRight(os.Getenv("EVOLUTION_API_URL"), "/"),
		Key:      os.Getenv("EVOLUTION_API_KEY"),
		HookBase: strings.TrimRight(firstNonEmpty(os.Getenv("WHATSAPP_HOOK_BASE"), os.Getenv("APP_URL")), "/"),
	}
}

func (c Config) Configured() bool { return c.URL != "" && c.Key != "" }

// ErrNotConfigured: no Evolution server is set up for this deployment.
var ErrNotConfigured = errors.New("WhatsApp is not available: this deployment has no Evolution API server configured")

// Error is a non-2xx answer from Evolution. Unreachable means nothing got
// there at all, so nothing was sent — a distinction worth keeping, because one
// is worth retrying and the other is worth reading.
type Error struct {
	Status      int
	Message     string
	Unreachable bool
}

func (e *Error) Error() string {
	if e.Unreachable {
		return "the WhatsApp server is unreachable: " + e.Message
	}
	return fmt.Sprintf("the WhatsApp server answered %d: %s", e.Status, e.Message)
}

// Client talks to one Evolution server on behalf of one instance.
type Client struct {
	Cfg      Config
	Instance string
	HTTP     *http.Client
}

const maxBody = 2 << 20

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	if !c.Cfg.Configured() {
		return ErrNotConfigured
	}
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(raw)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, method, c.Cfg.URL+path, rd)
	if err != nil {
		return &Error{Unreachable: true, Message: "EVOLUTION_API_URL is not a usable URL"}
	}
	req.Header.Set("apikey", c.Cfg.Key)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	res, err := hc.Do(req)
	if err != nil {
		// The transport error can carry the URL, which carries the deployment's
		// internal addressing; only its kind is kept.
		msg := "connection failed"
		if errors.Is(err, context.DeadlineExceeded) {
			msg = "timed out"
		}
		return &Error{Unreachable: true, Message: msg}
	}
	defer res.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(res.Body, maxBody))
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return &Error{Status: res.StatusCode, Message: errorText(raw)}
	}
	if out != nil && len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("the WhatsApp server's answer was not readable: %w", err)
		}
	}
	return nil
}

// errorText pulls a short message out of Evolution's several error shapes
// without echoing a whole body back to a user.
func errorText(raw []byte) string {
	var e struct {
		Message  any `json:"message"`
		Error    any `json:"error"`
		Response struct {
			Message any `json:"message"`
		} `json:"response"`
	}
	_ = json.Unmarshal(raw, &e)
	for _, v := range []any{e.Response.Message, e.Message, e.Error} {
		switch t := v.(type) {
		case string:
			if t != "" {
				return clip(t, 200)
			}
		case []any:
			if len(t) > 0 {
				return clip(fmt.Sprint(t[0]), 200)
			}
		case map[string]any:
			if m, ok := t["message"].(string); ok {
				return clip(m, 200)
			}
		}
	}
	return "no detail"
}

func (c *Client) inst() string { return url.PathEscape(c.Instance) }

// QRCode is what create/connect return while no phone is linked yet.
type QRCode struct {
	PairingCode string `json:"pairingCode"`
	Code        string `json:"code"`
	Base64      string `json:"base64"`
}

// State is the instance's connection state: open | close | connecting.
func (c *Client) State(ctx context.Context) (string, error) {
	var out struct {
		Instance struct {
			State string `json:"state"`
		} `json:"instance"`
		State string `json:"state"`
	}
	if err := c.do(ctx, http.MethodGet, "/instance/connectionState/"+c.inst(), nil, &out); err != nil {
		return "", err
	}
	return firstNonEmpty(out.Instance.State, out.State, "unknown"), nil
}

// Create makes the instance and returns its first QR code. With a number
// (digits, country code first) Evolution also returns a pairing code to type
// on that phone, for when scanning a QR is not possible.
func (c *Client) Create(ctx context.Context, number string) (QRCode, error) {
	var out struct {
		QRCode QRCode `json:"qrcode"`
	}
	body := map[string]any{
		"instanceName": c.Instance,
		"qrcode":       true,
		"integration":  "WHATSAPP-BAILEYS",
	}
	if number != "" {
		body["number"] = number
	}
	err := c.do(ctx, http.MethodPost, "/instance/create", body, &out)
	return out.QRCode, err
}

// Connect asks for a fresh QR code for an instance that already exists, and a
// pairing code when a number is given.
func (c *Client) Connect(ctx context.Context, number string) (QRCode, error) {
	var out QRCode
	path := "/instance/connect/" + c.inst()
	if number != "" {
		path += "?number=" + url.QueryEscape(number)
	}
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

// Delete removes the instance. Unlinking must actually unlink: leaving the
// instance behind would leave a phone session open that nobody can see.
func (c *Client) Delete(ctx context.Context) error {
	err := c.do(ctx, http.MethodDelete, "/instance/delete/"+c.inst(), nil, nil)
	if isNotFound(err) {
		return nil // already gone is the state we wanted
	}
	return err
}

// Events are the only ones the webhook is registered for. Narrow on purpose:
// Evolution can send a firehose, and everything not listed here is noise this
// app would have to parse and ignore.
var Events = []string{"MESSAGES_UPSERT", "CONNECTION_UPDATE"}

// SetWebhook points the instance at this app.
func (c *Client) SetWebhook(ctx context.Context, hookURL, token string) error {
	return c.do(ctx, http.MethodPost, "/webhook/set/"+c.inst(), map[string]any{
		"webhook": map[string]any{
			"enabled":  true,
			"url":      hookURL,
			"headers":  map[string]string{TokenHeader: token},
			"byEvents": false,
			"base64":   false,
			"events":   Events,
		},
	}, nil)
}

// SendText sends one message and returns WhatsApp's own id for it, which is
// what lets the echo of our own message be recognised later.
func (c *Client) SendText(ctx context.Context, number, text string) (string, error) {
	var out struct {
		Key struct {
			ID string `json:"id"`
		} `json:"key"`
	}
	if err := c.do(ctx, http.MethodPost, "/message/sendText/"+c.inst(),
		map[string]any{"number": number, "text": text}, &out); err != nil {
		return "", err
	}
	return out.Key.ID, nil
}

func isNotFound(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
