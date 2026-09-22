package ai

import (
	"context"
	"os"
	"testing"
	"time"
)

// A one-off manual proof that the TEI provider talks to the real containers on
// LXC 109. Skipped unless AI_LIVE_TEI_URL is set, so CI never opens a socket.
func TestLiveTEI(t *testing.T) {
	base := os.Getenv("AI_LIVE_TEI_URL")
	if base == "" {
		t.Skip("set AI_LIVE_TEI_URL to run the live proof")
	}
	p := newTEIProvider(ProviderConfig{Name: "tei-live", Kind: KindTEI, BaseURL: base, Timeout: 20 * time.Second})
	vecs, _, err := p.Embed(context.Background(), []string{"a company hiring Laravel engineers"})
	if err != nil {
		t.Fatalf("live embed: %v", err)
	}
	t.Logf("model=%s vectors=%d dims=%d first=%v", p.EmbedModel(), len(vecs), len(vecs[0]), vecs[0][:4])
}
