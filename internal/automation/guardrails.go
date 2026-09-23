/*
Package automation drives a paired browser through a sequence of steps and
keeps the record of what happened (MH-61).

The guardrails are in this file, at the top of the package, because they are
part of the product rather than a layer over it. This system holds tokens that
drive browsers ALREADY SIGNED IN to someone's mail, bank and work tools, so
every control here fails CLOSED:

	HOST ALLOW-LIST   A step aimed at a host outside the browser's list is
	                  refused before anything is queued. An empty list allows
	                  nothing, so a newly paired browser can do nothing until
	                  someone says where it may go.
	PAGE HOST         A goto is not the only way to reach a host: a link, a
	                  submitted form or a redirect moves the tab too. The
	                  extension checks the page its tab is ACTUALLY on before
	                  and after every step, and at every navigation it commits,
	                  against the list the server sends with each step. Leaving
	                  the list stops the run (CodeLeftAllowedSites), whatever
	                  the step's continue_on_error says (MH-422).
	KILL SWITCH       One flag per workspace. While it is on, every queued and
	                  running run is cancelled and no new run is accepted.
	ONE AT A TIME     A browser runs one run at a time. Two runs sharing a
	                  browser interleave tabs and produce results nobody can
	                  attribute.
	DEADLINE          Every run has one. A browser that stops answering must
	                  not leave a run "running" for ever.
	ATTRIBUTION       Every run records who asked: a member, or an MCP token.

Ported from fadymondy.com's internal/automation, with workspace scoping in
place of account scoping.
*/
package automation

import (
	"errors"
	"net/url"
	"strings"
	"time"
)

const (
	// A step that has not answered in this long is treated as lost. The browser
	// may still be doing something, but the run cannot wait for ever.
	DefaultStepTimeout = 90 * time.Second

	// The whole run's ceiling, independent of the steps: a sequence of steps
	// that each answer just in time must still end.
	DefaultRunDeadline = 15 * time.Minute

	// Steps are capped so one request cannot queue an unbounded amount of work
	// in somebody's browser.
	MaxSteps = 200
)

var (
	// ErrHostNotAllowed marks a refusal by the allow-list, so a caller can tell
	// it apart from an ordinary failure and say so in the UI.
	ErrHostNotAllowed = errors.New("that host is not on this browser's allow-list")
	// ErrPaused is the kill switch answering.
	ErrPaused = errors.New("automation is paused for this workspace")
	// ErrBusy is one-run-at-a-time answering.
	ErrBusy = errors.New("this browser is already running something")
	// ErrOffline is a browser that is not connected right now.
	ErrOffline = errors.New("this browser is not connected")
	// ErrLeftAllowedSites is a run whose tab reached a host off the list by a
	// route no goto check could see — a link, a form, a redirect (MH-422). The
	// browser reports it as an AgentError carrying CodeLeftAllowedSites.
	ErrLeftAllowedSites = errors.New("the run left this browser's allowed sites")
)

// CodeLeftAllowedSites is the code the extension puts on a step's error when
// the run's tab has left the allowed sites. It is the run's failure reason:
// the run stops there, and its error is the browser's sentence naming the step
// and the host ("step 4 went to evil.example, which isn't on this browser's
// allowed sites").
const CodeLeftAllowedSites = "left_allowed_sites"

// HostAllowed reports whether host is on list.
//
// An entry matches the host itself and any subdomain of it: "example.com"
// allows "www.example.com" and "app.example.com", because a site that redirects
// from its apex to its www host would otherwise be impossible to allow without
// listing every hostname it might use.
//
// It deliberately does NOT match a suffix textually: "notexample.com" must not
// be allowed by an entry for "example.com", which is the classic bug in this
// kind of check.
func HostAllowed(list []string, host string) bool {
	host = normaliseHost(host)
	if host == "" {
		return false
	}
	for _, entry := range list {
		entry = normaliseHost(entry)
		if entry == "" {
			continue
		}
		if host == entry || strings.HasSuffix(host, "."+entry) {
			return true
		}
	}
	return false
}

// normaliseHost reduces what people actually type — a whole URL, a host with a
// port, a trailing dot, mixed case — to a bare lowercase hostname. Anything it
// cannot make sense of comes back empty, which the allow-list treats as "no".
func normaliseHost(v string) string {
	v = strings.TrimSpace(strings.ToLower(v))
	if v == "" {
		return ""
	}
	if strings.Contains(v, "://") {
		u, err := url.Parse(v)
		if err != nil || u.Hostname() == "" {
			return ""
		}
		v = u.Hostname()
	}
	// A host with a port, typed by hand.
	if i := strings.IndexByte(v, '/'); i >= 0 {
		v = v[:i]
	}
	if i := strings.IndexByte(v, ':'); i >= 0 {
		v = v[:i]
	}
	v = strings.TrimSuffix(v, ".") // "example.com." is the same host
	if v == "" || strings.ContainsAny(v, " \t") {
		return ""
	}
	return v
}

// StepHost returns the host a step will navigate to, and whether the step
// navigates at all. Only a goto names its destination in advance, so only a
// goto can be checked here, before anything is queued. A click, an Enter or a
// dropdown can navigate too, to wherever the page sends it; that destination
// only exists in the browser, and the extension checks it there (MH-422).
func StepHost(command string, args map[string]any) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(command)) {
	case "goto", "navigate", "open":
	default:
		return "", false
	}
	raw, _ := args["url"].(string)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", true // a navigation with no URL is still a navigation: refuse it
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", true
	}
	// A scheme-relative or bare host ("example.com/x") parses with an empty
	// Host, and treating that as "no navigation" would skip the check.
	if u.Host == "" {
		return normaliseHost(raw), true
	}
	return normaliseHost(u.Hostname()), true
}

// CheckSteps refuses a run before anything is queued. It returns the first
// problem it finds, because a caller fixing one step at a time is better served
// by a specific message than by a list.
func CheckSteps(steps []Step, allowedHosts []string) error {
	if len(steps) == 0 {
		return errors.New("a run needs at least one step")
	}
	if len(steps) > MaxSteps {
		return errors.New("that is more steps than a single run may carry")
	}
	for i, s := range steps {
		if strings.TrimSpace(s.Command) == "" {
			return &StepError{Position: i, Err: errors.New("a step needs a command")}
		}
		host, navigates := StepHost(s.Command, s.Args)
		if !navigates {
			continue
		}
		if !HostAllowed(allowedHosts, host) {
			return &StepError{Position: i, Host: host, Err: ErrHostNotAllowed}
		}
	}
	return nil
}

// StepError says which step was refused. The position is 0-based internally and
// rendered 1-based, which is the only place the two differ.
type StepError struct {
	Position int
	Host     string
	Err      error
}

func (e *StepError) Error() string {
	if e.Host != "" {
		return "step " + itoa(e.Position+1) + " (" + e.Host + "): " + e.Err.Error()
	}
	return "step " + itoa(e.Position+1) + ": " + e.Err.Error()
}

func (e *StepError) Unwrap() error { return e.Err }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
