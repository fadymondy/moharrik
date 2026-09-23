package google

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

/*
Calling Google's data APIs as one connection (MH-440..447).

	c := src.Client(google.ScopeAnalytics)
	err := c.Get(ctx, google.API().AnalyticsAdmin+"/accountSummaries?pageSize=200", &out)

Every call draws a live access token from the connection's Source (so a
disconnected or reauth connection is refused before anything is sent), and:

  - a 401 drops the cached access token and retries once with a fresh one;
  - a 429, 500, 502, 503 or 504 is retried with backoff (Retry-After when
    Google sends one), at most maxAttempts times in all, and then returned;
  - a refusal comes back as *APIError: the status, Google's reason and its
    message — never the request, which carried the token.

The bases honour GOOGLE_API_BASE exactly as the OAuth endpoints do: one fake
origin (googletest) stands in for every API, and production refuses it.
*/

// APIs are the base URLs of the data APIs, without a trailing slash.
type APIs struct {
	AnalyticsAdmin   string // analyticsadmin.googleapis.com/v1beta
	AnalyticsData    string // analyticsdata.googleapis.com/v1beta
	Webmasters       string // www.googleapis.com/webmasters/v3
	SearchConsole    string // searchconsole.googleapis.com/v1 (URL inspection)
	YouTube          string // www.googleapis.com/youtube/v3
	YouTubeAnalytics string // youtubeanalytics.googleapis.com/v2
	YouTubeReporting string // youtubereporting.googleapis.com/v1
}

// RealAPIs is Google's own set.
var RealAPIs = APIs{
	AnalyticsAdmin:   "https://analyticsadmin.googleapis.com/v1beta",
	AnalyticsData:    "https://analyticsdata.googleapis.com/v1beta",
	Webmasters:       "https://www.googleapis.com/webmasters/v3",
	SearchConsole:    "https://searchconsole.googleapis.com/v1",
	YouTube:          "https://www.googleapis.com/youtube/v3",
	YouTubeAnalytics: "https://youtubeanalytics.googleapis.com/v2",
	YouTubeReporting: "https://youtubereporting.googleapis.com/v1",
}

// APIsAt is the set a fake Google at base serves (googletest).
func APIsAt(base string) APIs {
	base = strings.TrimRight(base, "/")
	return APIs{
		AnalyticsAdmin:   base + "/analyticsadmin/v1beta",
		AnalyticsData:    base + "/analyticsdata/v1beta",
		Webmasters:       base + "/webmasters/v3",
		SearchConsole:    base + "/searchconsole/v1",
		YouTube:          base + "/youtube/v3",
		YouTubeAnalytics: base + "/youtubeanalytics/v2",
		YouTubeReporting: base + "/youtubereporting/v1",
	}
}

// API is the set in effect.
func API() APIs {
	if base := strings.TrimSpace(os.Getenv("GOOGLE_API_BASE")); base != "" && !Production() {
		return APIsAt(base)
	}
	return RealAPIs
}

// RetryBase is the first backoff step between retries. Tests shorten it.
var RetryBase = time.Second

const (
	maxAttempts = 3
	maxWait     = 8 * time.Second
	// Enough for a 25,000-row Search Analytics page; nothing Google answers
	// here is bigger.
	maxBody = 32 << 20
)

// APIError is a Google API refusal: the status and the first reason, never
// the request.
type APIError struct {
	Status  int
	Reason  string
	Message string
	// RetryAfter is what Google asked for on a 429/503, when it said.
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	msg := e.Message
	if r := []rune(msg); len(r) > 300 {
		msg = string(r[:300]) + "…"
	}
	return strings.TrimSpace(fmt.Sprintf("google api: %d %s %s", e.Status, e.Reason, msg))
}

// ParseAPIError reads Google's error envelope. Both shapes are handled: the
// JSON API one ({error:{code,message,errors:[{reason}]}}) and the gRPC-style
// one ({error:{code,message,status,details:[{reason}]}}).
func ParseAPIError(status int, body []byte) *APIError {
	var env struct {
		Error struct {
			Message string `json:"message"`
			Status  string `json:"status"`
			Errors  []struct {
				Reason string `json:"reason"`
			} `json:"errors"`
			Details []struct {
				Reason string `json:"reason"`
			} `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &env)
	e := &APIError{Status: status, Message: env.Error.Message, Reason: env.Error.Status}
	for _, d := range env.Error.Details {
		if d.Reason != "" {
			e.Reason = d.Reason
			break
		}
	}
	if len(env.Error.Errors) > 0 && env.Error.Errors[0].Reason != "" {
		e.Reason = env.Error.Errors[0].Reason
	}
	if e.Message == "" && e.Reason == "" {
		e.Message = http.StatusText(status)
	}
	return e
}

// Client calls the data APIs as one connection.
type Client struct {
	src  *Source
	need []string
}

// Client returns an API client drawing tokens that must carry need.
func (s *Source) Client(need ...string) *Client { return &Client{src: s, need: need} }

// ConnectionID is the connection the client acts as.
func (c *Client) ConnectionID() string { return c.src.id }

// Get GETs u and decodes the JSON answer into out.
func (c *Client) Get(ctx context.Context, u string, out any) error {
	return c.Do(ctx, http.MethodGet, u, nil, out)
}

// Post POSTs in as JSON to u and decodes the answer into out (nil: ignored).
func (c *Client) Post(ctx context.Context, u string, in, out any) error {
	return c.Do(ctx, http.MethodPost, u, in, out)
}

// Do is one API call with the retry rules above.
func (c *Client) Do(ctx context.Context, method, u string, in, out any) error {
	var payload []byte
	if in != nil {
		var err error
		if payload, err = json.Marshal(in); err != nil {
			return err
		}
	}
	retried401 := false
	for attempt := 1; ; attempt++ {
		tok, err := c.src.Token(ctx, c.need...)
		if err != nil {
			return err
		}
		body, err := send(ctx, method, u, tok, payload)
		if err == nil {
			if out == nil || len(bytes.TrimSpace(body)) == 0 {
				return nil
			}
			if err := json.Unmarshal(body, out); err != nil {
				return fmt.Errorf("google api: unreadable answer: %w", err)
			}
			return nil
		}
		var ae *APIError
		if !errors.As(err, &ae) {
			if attempt < maxAttempts && ctx.Err() == nil {
				if werr := wait(ctx, attempt, 0); werr != nil {
					return err
				}
				continue
			}
			return err
		}
		switch {
		case ae.Status == http.StatusUnauthorized && !retried401:
			// The access token was refused (revoked or expired early): the
			// next Token call refreshes from the stored grant.
			retried401 = true
			dropCache(c.src.id)
			continue
		case retryable(ae.Status) && attempt < maxAttempts:
			if werr := wait(ctx, attempt, ae.RetryAfter); werr != nil {
				return ae
			}
			continue
		}
		return ae
	}
}

func retryable(status int) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

func wait(ctx context.Context, attempt int, retryAfter time.Duration) error {
	d := RetryBase << (attempt - 1)
	if retryAfter > d {
		d = retryAfter
	}
	if d > maxWait {
		d = maxWait
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func send(ctx context.Context, method, u, token string, payload []byte) ([]byte, error) {
	var rd io.Reader
	if payload != nil {
		rd = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := apiClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("google api unreachable: %w", scrubURLError(err))
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxBody))
	if err != nil {
		return nil, err
	}
	if res.StatusCode >= 300 {
		e := ParseAPIError(res.StatusCode, body)
		if s := res.Header.Get("Retry-After"); s != "" {
			if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n > 0 {
				e.RetryAfter = time.Duration(n) * time.Second
			}
		}
		return nil, e
	}
	return body, nil
}

// apiClient has a longer leash than the OAuth one: a Search Analytics page
// of 25,000 rows takes Google a while to assemble.
var apiClient = &http.Client{Timeout: 60 * time.Second}

// ---- telling people what went wrong --------------------------------------------

// RateLimited reports whether err is Google saying "slow down" or "out of
// quota for today" — the errors a caller backs off from rather than retries.
func RateLimited(err error) bool {
	var ae *APIError
	if !errors.As(err, &ae) {
		return false
	}
	if ae.Status == http.StatusTooManyRequests {
		return true
	}
	switch ae.Reason {
	case "rateLimitExceeded", "userRateLimitExceeded", "quotaExceeded", "dailyLimitExceeded", "RESOURCE_EXHAUSTED":
		return true
	}
	return false
}

// Describe turns an error from this package or a data API into one sentence
// a workspace admin can act on. It is what lands in last_error and on the page.
func Describe(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, ErrReauth):
		return "Google no longer accepts this account's connection. Reconnect it in Settings → Integrations."
	case errors.Is(err, ErrScope):
		return "This Google account has not granted the access this needs. Reconnect it for this product in Settings → Integrations."
	case errors.Is(err, ErrNotConnected):
		return "The Google account this came from is no longer connected."
	case errors.Is(err, ErrNotConfigured):
		return "Google is not configured on this server."
	}
	var ae *APIError
	if errors.As(err, &ae) {
		switch {
		case ae.Reason == "SERVICE_DISABLED" || ae.Reason == "accessNotConfigured" ||
			strings.Contains(ae.Message, "has not been used in project") || strings.Contains(ae.Message, "is disabled"):
			return "This Google API is not enabled in the Google Cloud project of Moharrik's OAuth client. " +
				"An operator must enable it at console.cloud.google.com/apis/library. Google said: " + clip(ae.Message, 240)
		case RateLimited(err):
			return "Google is rate limiting these requests (" + firstNonEmpty(ae.Reason, "429") + "). Moharrik backs off and tries again later."
		case ae.Status == http.StatusForbidden:
			return "Google refused access (" + firstNonEmpty(ae.Reason, "403") + "): " + clip(firstNonEmpty(ae.Message, "this account cannot read it"), 240)
		case ae.Status == http.StatusNotFound:
			return "Google says this no longer exists or this account cannot see it."
		case ae.Status >= 500:
			return "Google had a server error (" + strconv.Itoa(ae.Status) + "). Moharrik tries again later."
		}
		return "Google answered " + strconv.Itoa(ae.Status) + ": " + clip(firstNonEmpty(ae.Message, ae.Reason), 240)
	}
	if strings.Contains(err.Error(), "google api unreachable") {
		return "Google could not be reached. Moharrik tries again later."
	}
	return clip(err.Error(), 300)
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
