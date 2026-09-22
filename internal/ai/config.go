package ai

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

/*
Configuration is read from the environment once, at construction.

Two halves:

 1. Provider definitions. AI_PROVIDERS lists the names; each name N (upper-cased,
    '-' becoming '_') is then described by AI_PROVIDER_N_* variables.
 2. Task routing. AI_TASK_<TASK> names the provider for a task, AI_TASK_DEFAULT
    catches everything unset, and AI_FALLBACK[_<TASK>] is the chain tried when
    the chosen provider fails.

Nothing here reaches the network, so a misconfiguration is a startup error with
a readable message rather than a timeout an hour into a run.
*/

// Environment variable names, in one place so .env.example and the code cannot
// drift apart.
const (
	EnvProviders   = "AI_PROVIDERS"
	EnvTaskPrefix  = "AI_TASK_" // AI_TASK_DRAFT=claude
	EnvTaskDefault = "AI_TASK_DEFAULT"
	EnvFallback    = "AI_FALLBACK"  // global chain
	EnvFallbackPre = "AI_FALLBACK_" // AI_FALLBACK_DRAFT=qwen-local
	EnvJSONRetries = "AI_JSON_RETRIES"
	EnvPaidCap     = "AI_MAX_PAID_CALLS_PER_RUN"
	EnvProviderPre = "AI_PROVIDER_"
	EnvLegacyEmbed = "EMBEDDING_URL" // FM-26's embedder, bridged below
	EnvLegacyModel = "EMBEDDING_MODEL"
)

// Kind is a provider implementation. Adding one means adding a constant here,
// a case in newProvider, and the file that implements the interface.
type Kind string

const (
	KindOpenAICompatible Kind = "openai" // LM Studio, vLLM, Ollama, DashScope, OpenAI…
	KindTEI              Kind = "tei"    // HuggingFace text-embeddings-inference
	KindAnthropic        Kind = "anthropic"
	KindFake             Kind = "fake" // tests only; refuses to run outside them
)

// defaultTimeout bounds every call. A model that is loading can take seconds,
// and a pipeline run must not hang on one.
const defaultTimeout = 60 * time.Second

// ProviderConfig is one provider definition, all of it from env.
type ProviderConfig struct {
	Name        string
	Kind        Kind
	BaseURL     string
	Model       string // generation model, or the embed model for an embedder
	EmbedModel  string
	RerankModel string
	APIKeyEnv   string // the NAME of the env var holding the key — never the key
	APIKey      string // resolved from APIKeyEnv
	Timeout     time.Duration
	Paid        bool
	MaxTokens   int
	Temperature float64
	EmbedDim    int // 0 = accept whatever the endpoint returns
}

// Config is the whole layer's configuration.
type Config struct {
	Providers   []ProviderConfig
	Tasks       map[Task]string   // task -> provider name
	Default     string            // provider for tasks with no explicit mapping
	Fallbacks   map[Task][]string // task -> ordered fallback chain
	JSONRetries int               // extra attempts after a schema failure
	PaidCap     int               // paid calls allowed per run; 0 = none allowed, <0 = unlimited
}

// LoadConfig reads the environment. It returns an error only for configuration
// that cannot mean anything (a provider with no kind, a task pointing at a
// provider that was never defined) — an empty configuration is valid and
// yields a Client whose every call returns ErrNoProvider.
func LoadConfig() (Config, error) { return loadConfigFrom(os.Getenv) }

// Getenv is the environment lookup, injectable so tests never touch os.Setenv
// and can run in parallel.
type Getenv func(string) string

func loadConfigFrom(get Getenv) (Config, error) {
	cfg := Config{
		Tasks:       map[Task]string{},
		Fallbacks:   map[Task][]string{},
		JSONRetries: 1,
		PaidCap:     -1,
	}

	for _, name := range splitList(get(EnvProviders)) {
		pc, err := providerConfigFrom(get, name)
		if err != nil {
			return cfg, err
		}
		cfg.Providers = append(cfg.Providers, pc)
	}

	// FM-26 bridge: if nothing was declared but the old embedder is configured,
	// expose it as a provider named "embedding-url" rather than pretending the
	// machine has no embedder. internal/brain keeps using it directly either way.
	if len(cfg.Providers) == 0 {
		if base := strings.TrimSpace(get(EnvLegacyEmbed)); base != "" {
			model := strings.TrimSpace(get(EnvLegacyModel))
			if model == "" {
				model = "text-embedding-nomic-embed-text-v1.5"
			}
			cfg.Providers = append(cfg.Providers, ProviderConfig{
				Name:       "embedding-url",
				Kind:       KindOpenAICompatible,
				BaseURL:    strings.TrimRight(base, "/"),
				EmbedModel: model,
				Timeout:    defaultTimeout,
			})
			if get(EnvTaskPrefix+"EMBED") == "" && get(EnvTaskDefault) == "" {
				cfg.Tasks[TaskEmbed] = "embedding-url"
			}
		}
	}

	cfg.Default = strings.TrimSpace(get(EnvTaskDefault))

	for _, task := range AllTasks() {
		key := EnvTaskPrefix + strings.ToUpper(string(task))
		if v := strings.TrimSpace(get(key)); v != "" {
			cfg.Tasks[task] = v
		}
		chain := splitList(get(EnvFallbackPre + strings.ToUpper(string(task))))
		if len(chain) == 0 {
			chain = splitList(get(EnvFallback))
		}
		if len(chain) > 0 {
			cfg.Fallbacks[task] = chain
		}
	}

	if v := strings.TrimSpace(get(EnvJSONRetries)); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return cfg, fmt.Errorf("ai: %s must be a non-negative integer, got %q", EnvJSONRetries, v)
		}
		cfg.JSONRetries = n
	}
	if v := strings.TrimSpace(get(EnvPaidCap)); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return cfg, fmt.Errorf("ai: %s must be an integer, got %q", EnvPaidCap, v)
		}
		cfg.PaidCap = n
	}

	return cfg, cfg.validate()
}

func providerConfigFrom(get Getenv, name string) (ProviderConfig, error) {
	prefix := EnvProviderPre + envToken(name) + "_"
	pc := ProviderConfig{
		Name:    name,
		Kind:    Kind(strings.ToLower(strings.TrimSpace(get(prefix + "KIND")))),
		BaseURL: strings.TrimRight(strings.TrimSpace(get(prefix+"BASE_URL")), "/"),
		Model:   strings.TrimSpace(get(prefix + "MODEL")),
		Timeout: defaultTimeout,
	}
	pc.EmbedModel = strings.TrimSpace(get(prefix + "EMBED_MODEL"))
	pc.RerankModel = strings.TrimSpace(get(prefix + "RERANK_MODEL"))
	pc.APIKeyEnv = strings.TrimSpace(get(prefix + "API_KEY_ENV"))
	if pc.APIKeyEnv != "" {
		pc.APIKey = strings.TrimSpace(get(pc.APIKeyEnv))
	}
	if v := strings.TrimSpace(get(prefix + "TIMEOUT")); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return pc, fmt.Errorf("ai: %sTIMEOUT must be a duration like 30s, got %q", prefix, v)
		}
		pc.Timeout = d
	}
	pc.Paid = truthy(get(prefix + "PAID"))
	if v := strings.TrimSpace(get(prefix + "MAX_TOKENS")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return pc, fmt.Errorf("ai: %sMAX_TOKENS must be a positive integer, got %q", prefix, v)
		}
		pc.MaxTokens = n
	}
	if v := strings.TrimSpace(get(prefix + "TEMPERATURE")); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return pc, fmt.Errorf("ai: %sTEMPERATURE must be a number, got %q", prefix, v)
		}
		pc.Temperature = f
	}
	if v := strings.TrimSpace(get(prefix + "EMBED_DIM")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return pc, fmt.Errorf("ai: %sEMBED_DIM must be a positive integer, got %q", prefix, v)
		}
		pc.EmbedDim = n
	}

	if pc.Kind == "" {
		return pc, fmt.Errorf("ai: provider %q has no %sKIND (openai | tei | anthropic | fake)", name, prefix)
	}
	switch pc.Kind {
	case KindOpenAICompatible, KindTEI, KindAnthropic, KindFake:
	default:
		return pc, fmt.Errorf("ai: provider %q has unknown kind %q", name, pc.Kind)
	}
	if pc.BaseURL == "" && pc.Kind != KindFake {
		if pc.Kind == KindAnthropic {
			pc.BaseURL = anthropicDefaultBaseURL
		} else {
			return pc, fmt.Errorf("ai: provider %q has no %sBASE_URL", name, prefix)
		}
	}
	return pc, nil
}

// validate catches routing that names a provider nobody defined — the mistake
// that would otherwise surface as a silent ErrNoProvider mid-run.
func (c Config) validate() error {
	known := map[string]bool{}
	for _, p := range c.Providers {
		if known[p.Name] {
			return fmt.Errorf("ai: provider %q is defined twice in %s", p.Name, EnvProviders)
		}
		known[p.Name] = true
	}
	check := func(what, name string) error {
		if name != "" && !known[name] {
			return fmt.Errorf("ai: %s names provider %q, which is not in %s", what, name, EnvProviders)
		}
		return nil
	}
	if err := check(EnvTaskDefault, c.Default); err != nil {
		return err
	}
	tasks := make([]Task, 0, len(c.Tasks))
	for t := range c.Tasks {
		tasks = append(tasks, t)
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i] < tasks[j] })
	for _, t := range tasks {
		if err := check(EnvTaskPrefix+strings.ToUpper(string(t)), c.Tasks[t]); err != nil {
			return err
		}
	}
	for _, t := range AllTasks() {
		for _, name := range c.Fallbacks[t] {
			if err := check("fallback chain for "+string(t), name); err != nil {
				return err
			}
		}
	}
	return nil
}

// Chain is the ordered list of provider names to try for a task: the mapped
// provider (or the default), then the fallback chain, with duplicates removed
// so a provider is never asked twice for one call.
func (c Config) Chain(task Task) []string {
	var out []string
	seen := map[string]bool{}
	add := func(n string) {
		if n == "" || seen[n] {
			return
		}
		seen[n] = true
		out = append(out, n)
	}
	if n, ok := c.Tasks[task]; ok {
		add(n)
	} else {
		add(c.Default)
	}
	for _, n := range c.Fallbacks[task] {
		add(n)
	}
	return out
}

// envToken turns a provider name into the token used in its env var names:
// "qwen-local" -> "QWEN_LOCAL".
func envToken(name string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(name)) {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

func splitList(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
