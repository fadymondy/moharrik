package inbox

import (
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

/*
Rate limiting on the public chat endpoint (MH-377).

The widget key is public by design — it sits in the customer's page source — and
the Origin allow-list is a header curl sets to whatever it likes. So until this,
anyone who viewed a customer's site could write a loop that filled that
workspace's inbox, one conversation per request.

The shape is fadymondy.com's internal/chat (Allow/Seen): a sliding window of
timestamps per key, pruned so a long-running process does not keep a map entry
per visitor for ever. It is widened from one key to three, because each stops a
different attack:

  - per VISITOR TOKEN: a script continuing one thread. The visitor token is the
    identity the widget itself holds, so this is the limit a real person could
    in principle meet — and 12 a minute is still a message every 5 seconds.
  - per CLIENT IP (per widget): a script that simply stops sending its visitor
    token, which starts a fresh conversation each time. That is the obvious way
    round a per-token limit, so the IP carries the real weight. The address is
    the one internal/clientip derives from the TRUSTED proxy chain, never a
    header the sender wrote.
  - per WIDGET: a ceiling on the whole inbox, for traffic spread across many
    addresses. It is the one limit that can refuse an innocent visitor, which
    is why it is set well above what one small business's site sees, and why
    its window is short — a flood that stops is forgotten in a minute.

Opening a conversation is counted separately and more tightly than writing in
one: starting threads is what fills the inbox list, and a person has one thread
per site. (There is no separate "open" route here — the first message opens
it — so "open" means a POST with no visitor token.)

Not ported: chat.go's first-message delay. It keys on a Seen() recorded when the
widget opens a conversation, before anyone types; this API has no such earlier
call, so the delay would refuse every visitor's first message once — the exact
bug chat.go's own Seen comment describes.

All-or-nothing: a request refused by any rule is recorded against none, so a
refused flood does not push the window forward and a visitor's refused attempt
does not use up the IP's allowance.
*/

// Rule is one limit: at most Limit requests per Window under Key.
type Rule struct {
	Key    string
	Limit  int
	Window time.Duration
}

/*
Limiter decides, atomically across several rules, whether a request may go
ahead. The in-memory one below is right for the single-instance deploy: it is
per-process best-effort abuse control, not an audit trail. A second API replica
would give each process its own counts (so the effective limits double); at
that point a shared store — Redis, or a Postgres table keyed on Rule.Key —
implements this interface and replaces NewMemoryLimiter in SetLimiter, with no
change to the handler.
*/
type Limiter interface {
	// Take records one request under every rule if all of them allow it. If
	// any refuses, nothing is recorded and retryAfter is how long until every
	// refusing rule would allow one more.
	Take(now time.Time, rules ...Rule) (ok bool, retryAfter time.Duration)
}

// ChatLimits are the numbers. Chosen, not derived — see the package comment
// above for what each one is for.
type ChatLimits struct {
	VisitorMessages Rate // one visitor token continuing its thread
	IPMessages      Rate // one client address, any number of tokens
	WidgetMessages  Rate // the whole widget
	IPOpens         Rate // new conversations from one address
	WidgetOpens     Rate // new conversations on the whole widget
}

// Rate is a limit without a key.
type Rate struct {
	Limit  int
	Window time.Duration
}

// DefaultChatLimits:
//
//   - 12/min per visitor: fadymondy.com's burstLimit, a message every 5s.
//   - 20/min per address: a visitor at full pace plus room for a second person
//     behind the same office NAT, and no more.
//   - 60/min per widget: a busy small-business site has a handful of live chats,
//     each a few messages a minute; this is ~20 people typing at once.
//   - 5 per 10 min new conversations per address: a person has one thread; five
//     covers cleared storage and a second browser.
//   - 30 per 10 min new conversations per widget: three new chats a minute,
//     sustained, is far beyond the sites this serves.
func DefaultChatLimits() ChatLimits {
	return ChatLimits{
		VisitorMessages: Rate{12, time.Minute},
		IPMessages:      Rate{20, time.Minute},
		WidgetMessages:  Rate{60, time.Minute},
		IPOpens:         Rate{5, 10 * time.Minute},
		WidgetOpens:     Rate{30, 10 * time.Minute},
	}
}

var (
	chatLimitsMu sync.RWMutex
	chatLimiter  Limiter = NewMemoryLimiter()
	chatLimits           = DefaultChatLimits()
)

// SetLimiter swaps the store and the numbers (a shared store for several
// replicas; tighter numbers in a test). Returns a function restoring the old.
func SetLimiter(l Limiter, limits ChatLimits) (restore func()) {
	chatLimitsMu.Lock()
	prevL, prevLimits := chatLimiter, chatLimits
	chatLimiter, chatLimits = l, limits
	chatLimitsMu.Unlock()
	return func() {
		chatLimitsMu.Lock()
		chatLimiter, chatLimits = prevL, prevLimits
		chatLimitsMu.Unlock()
	}
}

// chatRules builds the rules for one POST. opening is a POST with no visitor
// token, which starts a conversation.
func chatRules(limits ChatLimits, widgetID, ipBucket, visitor string, opening bool) []Rule {
	w := "chat:w:" + widgetID
	rules := []Rule{
		{w + ":ip:" + ipBucket + ":msg", limits.IPMessages.Limit, limits.IPMessages.Window},
		{w + ":msg", limits.WidgetMessages.Limit, limits.WidgetMessages.Window},
	}
	if opening {
		rules = append(rules,
			Rule{w + ":ip:" + ipBucket + ":open", limits.IPOpens.Limit, limits.IPOpens.Window},
			Rule{w + ":open", limits.WidgetOpens.Limit, limits.WidgetOpens.Window},
		)
	} else {
		rules = append(rules, Rule{w + ":v:" + visitor, limits.VisitorMessages.Limit, limits.VisitorMessages.Window})
	}
	return rules
}

// allowChat is the handler's one call: nil, or a 429 carrying Retry-After.
func allowChat(widgetID, ipBucket, visitor string, opening bool) error {
	chatLimitsMu.RLock()
	l, limits := chatLimiter, chatLimits
	chatLimitsMu.RUnlock()

	ok, retry := l.Take(time.Now(), chatRules(limits, widgetID, ipBucket, visitor, opening)...)
	if ok {
		return nil
	}
	// Whole seconds, rounded up: "0" would invite an immediate retry that is
	// refused again.
	secs := int((retry + time.Second - 1) / time.Second)
	if secs < 1 {
		secs = 1
	}
	return huma.ErrorWithHeaders(
		huma.Error429TooManyRequests(fmt.Sprintf("too many messages — try again in %d seconds", secs)),
		map[string][]string{"Retry-After": {strconv.Itoa(secs)}},
	)
}

// ---- the in-memory store --------------------------------------------------

const pruneEvery = 5 * time.Minute

type window struct {
	hits   []time.Time // oldest first, only ever <= Limit long
	length time.Duration
}

type memoryLimiter struct {
	mu     sync.Mutex
	keys   map[string]*window
	pruned time.Time
}

// NewMemoryLimiter is the single-process store. See Limiter for when it stops
// being enough.
func NewMemoryLimiter() Limiter {
	return &memoryLimiter{keys: map[string]*window{}}
}

func (m *memoryLimiter) Take(now time.Time, rules ...Rule) (bool, time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.pruned.IsZero() || now.Sub(m.pruned) > pruneEvery {
		m.prune(now)
		m.pruned = now
	}

	var wait time.Duration
	refused := false
	for _, r := range rules {
		if r.Limit <= 0 || r.Window <= 0 {
			continue // an unset rule limits nothing
		}
		w := m.keys[r.Key]
		if w == nil {
			continue // never seen: under any positive limit
		}
		w.length = r.Window
		recent := w.hits[:0]
		for _, at := range w.hits {
			if now.Sub(at) < r.Window {
				recent = append(recent, at)
			}
		}
		w.hits = recent
		if len(recent) >= r.Limit {
			refused = true
			// The slot that frees first is the oldest one that has to expire
			// for the count to drop below the limit.
			if d := recent[len(recent)-r.Limit].Add(r.Window).Sub(now); d > wait {
				wait = d
			}
		}
	}
	if refused {
		return false, wait
	}

	for _, r := range rules {
		if r.Limit <= 0 || r.Window <= 0 {
			continue
		}
		w := m.keys[r.Key]
		if w == nil {
			w = &window{length: r.Window}
			m.keys[r.Key] = w
		}
		w.hits = append(w.hits, now)
	}
	return true, 0
}

// prune drops keys nobody has hit within their window, so a flood of fresh
// visitor tokens or addresses costs memory only for as long as it is relevant.
func (m *memoryLimiter) prune(now time.Time) {
	for k, w := range m.keys {
		if len(w.hits) == 0 || now.Sub(w.hits[len(w.hits)-1]) >= w.length {
			delete(m.keys, k)
		}
	}
}
