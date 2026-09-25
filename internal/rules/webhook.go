package rules

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"syscall"
	"time"

	"github.com/fadymondy/moharrik/internal/secret"
)

/*
The webhook action: a signed POST of the event to an address the rule names.

The body is JSON — {"event", "rule": {"id","name"}, "subject_id", "data",
"sent_at"} — and the signature is the one Moharrik's own inbound generic
webhooks take (MH-381), so a receiver verifies both the same way:

	X-Moharrik-Timestamp: <unix seconds>
	X-Moharrik-Signature: sha256=<hex HMAC-SHA256(secret, timestamp + "." + body)>

Each rule has its own secret, sealed at rest, shown to a manager on the rule's
page and rotated there. A receiver should refuse a timestamp older than five
minutes.

Private addresses are refused at dial time, on the resolved IP, exactly like
the notification collector (internal/notifications/collect.go): a rule's URL is
typed by a member, and without this it could aim the server at its own network
and the database behind it. RULES_WEBHOOK_ALLOW_PRIVATE=1 lifts it for local
development; the tests lift it for httptest.
*/

var webhookBox = secret.New(secret.DomainRuleWebhookSecret, "the rule's webhook secret", "rotate it on the rule's page")

// AllowPrivate lets the webhook action reach loopback and private addresses.
var AllowPrivate = os.Getenv("RULES_WEBHOOK_ALLOW_PRIVATE") == "1"

var errPrivate = errors.New("that address is on a private network, which a rule's webhook does not call")

var webhookClient = &http.Client{
	Timeout: 10 * time.Second,
	Transport: &http.Transport{
		Proxy: nil,
		DialContext: (&net.Dialer{
			Timeout: 5 * time.Second,
			Control: func(_, address string, _ syscall.RawConn) error {
				if AllowPrivate {
					return nil
				}
				host, _, err := net.SplitHostPort(address)
				if err != nil {
					return err
				}
				ip := net.ParseIP(host)
				if ip == nil || !publicIP(ip) {
					return errPrivate
				}
				return nil
			},
		}).DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
	},
	// A webhook answers where it is; following a redirect would POST the event
	// somewhere the rule never named.
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func publicIP(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast() ||
		(ip.To4() != nil && ip.To4()[0] == 100 && ip.To4()[1]&0xC0 == 64))
}

// Sign is the signature header value for body at t.
func Sign(secretKey string, t time.Time, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secretKey))
	mac.Write([]byte(strconv.FormatInt(t.Unix(), 10) + "."))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// webhookSecret returns the rule's signing secret, making one on first use.
func (x *execCtx) webhookSecret() (string, error) {
	var sealed string
	if err := x.a.SQLDB.QueryRowContext(x.ctx, `SELECT webhook_secret FROM automation_rules WHERE workspace_id = $1 AND id = $2`, x.ws, x.rule.ID).Scan(&sealed); err != nil {
		return "", err
	}
	if sealed != "" {
		return webhookBox.Open(sealed)
	}
	return RotateSecret(x.ctx, x.a.SQLDB, x.ws, x.rule.ID)
}

func (x *execCtx) webhook(act RuleAction) RuleActionResult {
	target := x.param(act, "url")
	u, err := url.Parse(target)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return fail(act.Type, "", fmt.Errorf("%q is not a web address", target), msg{}, m("invalid_url", "value", target))
	}
	summary := "POST the event to " + u.Scheme + "://" + u.Host + u.Path
	sm := m("webhook", "url", u.Scheme+"://"+u.Host+u.Path)
	if x.dry {
		return would(act.Type, summary+", signed with the rule's secret", sm)
	}
	secretKey, err := x.webhookSecret()
	if err != nil {
		return fail(act.Type, summary, errors.New("could not read the rule's signing secret"), sm, m("webhook_secret_unreadable"))
	}
	now := time.Now().UTC()
	body, _ := json.Marshal(map[string]any{
		"event": x.rule.Trigger, "rule": map[string]string{"id": x.rule.ID, "name": x.rule.Name},
		"subject_id": x.subjectID, "data": x.data, "sent_at": now.Format(time.RFC3339),
	})
	ctx, cancel := context.WithTimeout(x.ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return fail(act.Type, summary, err, sm, generic(err))
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Moharrik-Rules/1.0 (+https://moharrik.com)")
	req.Header.Set("X-Moharrik-Event", x.rule.Trigger)
	req.Header.Set("X-Moharrik-Timestamp", strconv.FormatInt(now.Unix(), 10))
	req.Header.Set("X-Moharrik-Signature", Sign(secretKey, now, body))
	res, err := webhookClient.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) && errors.Is(ue.Err, errPrivate) {
			return fail(act.Type, summary, errPrivate, sm, m("private_address"))
		}
		if errors.Is(err, errPrivate) {
			return fail(act.Type, summary, errPrivate, sm, m("private_address"))
		}
		return fail(act.Type, summary, fmt.Errorf("could not reach it: %s", clip(err.Error(), 200)), sm, m("webhook_unreachable", "detail", clip(err.Error(), 200)))
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fail(act.Type, summary, fmt.Errorf("it answered %d", res.StatusCode), sm, m("webhook_bad_status", "status", strconv.Itoa(res.StatusCode)))
	}
	return ok(act.Type, fmt.Sprintf("%s — it answered %d", summary, res.StatusCode), "", m("webhook_ok", "url", u.Scheme+"://"+u.Host+u.Path, "status", strconv.Itoa(res.StatusCode)))
}
