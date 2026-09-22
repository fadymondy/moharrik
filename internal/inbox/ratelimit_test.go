package inbox

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

func TestLimiterSlidingWindow(t *testing.T) {
	l := NewMemoryLimiter()
	t0 := time.Unix(1_700_000_000, 0)
	r := Rule{"k", 3, time.Minute}

	for i := 0; i < 3; i++ {
		if ok, _ := l.Take(t0.Add(time.Duration(i)*time.Second), r); !ok {
			t.Fatalf("request %d refused under the limit", i+1)
		}
	}
	ok, wait := l.Take(t0.Add(10*time.Second), r)
	if ok {
		t.Fatal("the 4th request in a minute was allowed")
	}
	// The first hit (t0) expires at t0+60s; asked at t0+10s, that is 50s away.
	if wait != 50*time.Second {
		t.Fatalf("retry after %v, want 50s", wait)
	}
	if ok, _ := l.Take(t0.Add(60 * time.Second)); !ok {
		// no rules: always allowed
		t.Fatal("a request with no rules was refused")
	}
	if ok, _ := l.Take(t0.Add(60*time.Second), r); !ok {
		t.Fatal("still refused once the oldest hit left the window")
	}
}

func TestLimiterIsAllOrNothing(t *testing.T) {
	l := NewMemoryLimiter()
	now := time.Unix(1_700_000_000, 0)
	tight := Rule{"tight", 1, time.Minute}
	loose := Rule{"loose", 2, time.Minute}

	if ok, _ := l.Take(now, tight, loose); !ok {
		t.Fatal("first request refused")
	}
	// Refused by tight: loose must NOT be charged for it.
	for i := 0; i < 5; i++ {
		if ok, _ := l.Take(now, tight, loose); ok {
			t.Fatal("tight rule did not refuse")
		}
	}
	if ok, _ := l.Take(now, loose); !ok {
		t.Fatal("refused requests were charged against a rule that allowed them")
	}
}

func TestLimiterRetryIsTheLongestRefusal(t *testing.T) {
	l := NewMemoryLimiter()
	t0 := time.Unix(1_700_000_000, 0)
	short := Rule{"s", 1, 10 * time.Second}
	long := Rule{"l", 1, 10 * time.Minute}
	l.Take(t0, short, long)
	if ok, wait := l.Take(t0.Add(time.Second), short, long); ok || wait != 10*time.Minute-time.Second {
		t.Fatalf("ok=%v wait=%v, want refused for the longer rule's 9m59s", ok, wait)
	}
}

func TestLimiterPrunesIdleKeys(t *testing.T) {
	l := NewMemoryLimiter().(*memoryLimiter)
	t0 := time.Unix(1_700_000_000, 0)
	for i := 0; i < 100; i++ {
		l.Take(t0, Rule{Key: "v" + string(rune('a'+i%26)) + string(rune('a'+i/26)), Limit: 5, Window: time.Minute})
	}
	l.Take(t0.Add(pruneEvery+time.Minute), Rule{"fresh", 5, time.Minute})
	if len(l.keys) != 1 {
		t.Fatalf("%d keys kept after everything idle expired, want 1", len(l.keys))
	}
}

func TestChatRulesOpeningVersusContinuing(t *testing.T) {
	d := DefaultChatLimits()
	open := chatRules(d, "w1", "1.2.3.4", "", true)
	cont := chatRules(d, "w1", "1.2.3.4", "cvt_x", false)
	if len(open) != 4 || len(cont) != 3 {
		t.Fatalf("rules: open %d, continue %d", len(open), len(cont))
	}
	// Both are charged to the address and the widget's message allowance.
	if open[0].Key != cont[0].Key || open[1].Key != cont[1].Key {
		t.Fatal("opening and continuing do not share the address/widget message buckets")
	}
	// Two widgets never share a bucket.
	other := chatRules(d, "w2", "1.2.3.4", "cvt_x", false)
	for i := range cont {
		if cont[i].Key == other[i].Key {
			t.Fatalf("widgets w1 and w2 share %q", cont[i].Key)
		}
	}
}

func TestAllowChatAnswers429WithRetryAfter(t *testing.T) {
	defer SetLimiter(NewMemoryLimiter(), ChatLimits{VisitorMessages: Rate{1, time.Minute}})()
	if err := allowChat("w", "1.2.3.4", "cvt_a", false); err != nil {
		t.Fatal(err)
	}
	err := allowChat("w", "1.2.3.4", "cvt_a", false)
	var status huma.StatusError
	var headers huma.HeadersError
	if !errors.As(err, &status) || status.GetStatus() != http.StatusTooManyRequests {
		t.Fatalf("second message: %v, want a 429", err)
	}
	if !errors.As(err, &headers) || headers.GetHeaders().Get("Retry-After") != "60" {
		t.Fatalf("Retry-After = %q, want 60", headers.GetHeaders().Get("Retry-After"))
	}
}
