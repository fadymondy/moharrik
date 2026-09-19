// Package feedback is a thin client for the Mahaam Feedback widget:
// it renders the embed script tag, injects it into HTML responses and
// reports server-side problems to the public intake. Stdlib only.
package feedback

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"html/template"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"
)

// DefaultBaseURL is the hosted Mahaam instance.
const DefaultBaseURL = "https://console.mahaam.app"

// Config configures a Client. Blank values fall back to the env vars
// MAHAAM_FEEDBACK_KEY, MAHAAM_URL and MAHAAM_APP_URL (each falls back to its old MANAGY_* name).
type Config struct {
	BaseURL  string // Mahaam base URL
	Key      string // pfk_... public key
	Locale   string // en | ar
	Endpoint string // optional data-endpoint override
	AppURL   string // a URL on the key's allowed origins; sent as page_url for server reports
	Disabled bool
	HTTP     *http.Client
}

// Client is safe for concurrent use.
type Client struct{ cfg Config }

// getenv reads MAHAAM_<name>, falling back to the pre-rebrand MANAGY_<name>.
func getenv(name string) string {
	if v := os.Getenv("MAHAAM_" + name); v != "" {
		return v
	}
	return os.Getenv("MANAGY_" + name)
}

// New builds a client, filling blanks from the environment.
func New(cfg Config) *Client {
	if cfg.Key == "" {
		cfg.Key = getenv("FEEDBACK_KEY")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = getenv("URL")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.AppURL == "" {
		cfg.AppURL = getenv("APP_URL")
	}
	if cfg.Locale == "" {
		cfg.Locale = "en"
	}
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{cfg: cfg}
}

// Enabled reports whether the client has a key and is not disabled.
func (c *Client) Enabled() bool { return !c.cfg.Disabled && c.cfg.Key != "" }

// ScriptTag renders the widget <script> tag, or "" when disabled.
func (c *Client) ScriptTag() template.HTML {
	if !c.Enabled() {
		return ""
	}
	e := html.EscapeString
	s := fmt.Sprintf(`<script src="%s/embed/v1.js" data-key="%s" data-locale="%s"`, e(c.cfg.BaseURL), e(c.cfg.Key), e(c.cfg.Locale))
	if c.cfg.Endpoint != "" {
		s += fmt.Sprintf(` data-endpoint="%s"`, e(c.cfg.Endpoint))
	}
	return template.HTML(s + " defer></script>")
}

// Report is one intake submission. Extra carries any other intake field
// (route, selector, user_agent, viewport, console_log, network_log).
type Report struct {
	Title    string
	Body     string
	Type     string // bug | feature | task
	Priority string // highest | high | medium | low | lowest
	PageURL  string
	Extra    map[string]string
}

// APIError is a non-2xx intake response (403 key/origin, 422 validation, 429 rate limit).
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("mahaam feedback: %d %s", e.Status, e.Message) }

// ErrDisabled is returned by Report when the client is not enabled.
var ErrDisabled = errors.New("mahaam feedback: disabled or no key")

// Report posts to {BaseURL}/api/feedback/embed and returns the issue key (e.g. MG-12).
func (c *Client) Report(ctx context.Context, r Report) (string, error) {
	if !c.Enabled() {
		return "", ErrDisabled
	}
	if strings.TrimSpace(r.Title) == "" {
		return "", errors.New("mahaam feedback: title is required")
	}
	r.Title = truncate(r.Title, 200)
	if r.PageURL == "" {
		r.PageURL = c.cfg.AppURL
	}
	fields := map[string]string{
		"public_key": c.cfg.Key, "title": r.Title, "body": r.Body,
		"issue_type": r.Type, "priority": r.Priority, "page_url": r.PageURL,
	}
	for k, v := range r.Extra {
		if _, taken := fields[k]; !taken {
			fields[k] = v
		}
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		if v != "" {
			_ = mw.WriteField(k, v)
		}
	}
	_ = mw.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/api/feedback/embed", &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	resp, err := c.cfg.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	var out struct {
		Key   string `json:"key"`
		Error string `json:"error"`
	}
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := out.Error
		if msg == "" {
			msg = strings.TrimSpace(string(raw))
		}
		return "", &APIError{Status: resp.StatusCode, Message: msg}
	}
	return out.Key, nil
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
