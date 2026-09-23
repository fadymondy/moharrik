package trends

import (
	"context"
	"strings"
	"sync"

	"github.com/fadymondy/moharrik/internal/ai"
	"github.com/fadymondy/moharrik/internal/app"
)

/*
AILayer is what clustering and summaries use from internal/ai (MH-385). nil
means "deterministic only", which is production today: there is no generation
model reachable, so the extractive summary and the token clusterer have to
stand on their own, and do. Ported from fadymondy.com internal/trends/ai_layer.go.
*/
type AILayer interface {
	// Embed returns one vector per text and the model that produced them.
	Embed(ctx context.Context, texts []string) ([][]float32, string, error)
	// Summarise writes a short neutral summary; prov is "provider/model".
	Summarise(ctx context.Context, material string) (text, prov string, err error)
}

type clientLayer struct {
	c                    *ai.Client
	canEmbed, canSummary bool
}

var (
	layerOnce sync.Once
	layer     AILayer
	// layerOverride lets tests inject a fake layer.
	layerOverride AILayer
)

// Layer is the process's AI layer, built once from the environment; nil when
// neither an embedder nor a summariser is configured.
func Layer(a *app.App) AILayer {
	if layerOverride != nil {
		return layerOverride
	}
	layerOnce.Do(func() {
		if a == nil || a.Log == nil {
			return
		}
		c, err := ai.NewFromEnv(a.Log)
		if err != nil {
			a.Log.Warn("trends: AI config unreadable, clustering deterministically", "err", err)
			return
		}
		layer = LayerFromClient(c)
	})
	return layer
}

// LayerFromClient adapts an ai.Client (tests pass one with fake providers).
func LayerFromClient(c *ai.Client) AILayer {
	if c == nil {
		return nil
	}
	_, e := c.ProviderFor(ai.TaskEmbed)
	_, s := c.ProviderFor(ai.TaskSummarise)
	if !e && !s {
		return nil
	}
	return &clientLayer{c: c, canEmbed: e, canSummary: s}
}

func (l *clientLayer) Embed(ctx context.Context, texts []string) ([][]float32, string, error) {
	if !l.canEmbed {
		return nil, "", ai.ErrNoProvider
	}
	res, err := l.c.Embed(ctx, texts)
	if err != nil {
		return nil, "", err
	}
	return res.Vectors, res.Provenance.Provider + "/" + res.Provenance.Model, nil
}

func (l *clientLayer) Summarise(ctx context.Context, material string) (string, string, error) {
	if !l.canSummary {
		return "", "", ai.ErrNoProvider
	}
	res, err := l.c.Generate(ctx, ai.TaskSummarise, ai.GenerateRequest{
		System: "You summarise one news story for a busy business owner. Two sentences, plain and neutral, " +
			"in the language of the headlines, using only the headlines and excerpts given. " +
			"Do not invent facts, numbers or quotes.",
		Messages:  []ai.Message{{Role: "user", Content: material}},
		MaxTokens: 180,
	})
	if err != nil {
		return "", "", err
	}
	return strings.TrimSpace(res.Text), res.Provenance.Provider + "/" + res.Provenance.Model, nil
}
