// Package main (continued) — CLI flag parsing, config loading, and provider
// selection helpers shared across subcommands.
package main

import (
	"flag"
	"io"
	"os"
	"strings"

	"github.com/zo-ll/oi/internal/config"
	"github.com/zo-ll/oi/internal/oauth"
	iprovider "github.com/zo-ll/oi/internal/provider"
)

// commonOptions holds flags accepted by most subcommands.
// It is parsed from subcommand arguments after the subcommand name itself.
type commonOptions struct {
	provider string
	model    string
	apiKey   string
	debug    bool
	jsonOut  bool
	ndjson   bool
	rest     []string
}

// parseCommonOptions parses the standard flag set shared by subcommands
// (--provider, --model, --api-key, --debug, --json, --ndjson).
// name is used only for usage messages via the flag set.
func parseCommonOptions(name string, args []string) (commonOptions, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var opts commonOptions
	fs.StringVar(&opts.provider, "provider", "", "provider name")
	fs.StringVar(&opts.model, "model", "", "model name")
	fs.StringVar(&opts.apiKey, "api-key", "", "API key override")
	fs.BoolVar(&opts.debug, "debug", false, "enable debug logging")
	fs.BoolVar(&opts.jsonOut, "json", false, "machine-readable JSON output")
	fs.BoolVar(&opts.ndjson, "ndjson", false, "machine-readable NDJSON event output")
	if err := fs.Parse(args); err != nil {
		return commonOptions{}, err
	}
	opts.rest = fs.Args()
	return opts, nil
}

// loadSelection loads config from disk, validates it, loads auth, and
// resolves the effective provider/model selection.
func loadSelection(opts commonOptions) (*config.Config, config.Selection, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, config.Selection{}, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, config.Selection{}, err
	}
	auth, err := config.LoadAuth()
	if err != nil {
		return nil, config.Selection{}, err
	}
	sel, err := config.ResolveSelection(cfg, auth, opts.provider, opts.model, opts.apiKey)
	if err != nil {
		return nil, config.Selection{}, err
	}
	return cfg, sel, nil
}

// requireProvider constructs the provider.Provider for a selection.
func requireProvider(sel config.Selection) (iprovider.Provider, error) {
	return iprovider.NewForSelection(sel)
}

// canonicalProviderName normalizes common aliases to their canonical
// internal provider ids. For example, "chatgpt" and "codex" both map
// to "openai-codex".
func canonicalProviderName(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "chatgpt", "codex", "openai-browser", "openai-chatgpt":
		return "openai-codex"
	default:
		return strings.TrimSpace(name)
	}
}

// knownProviderProfile returns the default base URL and API-key env-var
// for well-known providers. This is used during login to pre-fill
// provider configuration before the user supplies credentials.
func knownProviderProfile(name string) (config.ProviderConfig, bool) {
	switch canonicalProviderName(name) {
	case "openai":
		return config.ProviderConfig{BaseURL: "https://api.openai.com/v1", APIKeyEnv: "OPENAI_API_KEY"}, true
	case "openai-codex":
		return config.ProviderConfig{BaseURL: "https://chatgpt.com/backend-api"}, true
	case "openrouter":
		return config.ProviderConfig{BaseURL: "https://openrouter.ai/api/v1", APIKeyEnv: "OPENROUTER_API_KEY"}, true
	case "groq":
		return config.ProviderConfig{BaseURL: "https://api.groq.com/openai/v1", APIKeyEnv: "GROQ_API_KEY"}, true
	case "deepseek":
		return config.ProviderConfig{BaseURL: "https://api.deepseek.com/v1", APIKeyEnv: "DEEPSEEK_API_KEY"}, true
	case "together":
		return config.ProviderConfig{BaseURL: "https://api.together.xyz/v1", APIKeyEnv: "TOGETHER_API_KEY"}, true
	case "fireworks":
		return config.ProviderConfig{BaseURL: "https://api.fireworks.ai/inference/v1", APIKeyEnv: "FIREWORKS_API_KEY"}, true
	case "perplexity":
		return config.ProviderConfig{BaseURL: "https://api.perplexity.ai", APIKeyEnv: "PERPLEXITY_API_KEY"}, true
	case "mistral":
		return config.ProviderConfig{BaseURL: "https://api.mistral.ai/v1", APIKeyEnv: "MISTRAL_API_KEY"}, true
	case "xai":
		return config.ProviderConfig{BaseURL: "https://api.x.ai/v1", APIKeyEnv: "XAI_API_KEY"}, true
	case "cerebras":
		return config.ProviderConfig{BaseURL: "https://api.cerebras.ai/v1", APIKeyEnv: "CEREBRAS_API_KEY"}, true
	case "sambanova":
		return config.ProviderConfig{BaseURL: "https://api.sambanova.ai/v1", APIKeyEnv: "SAMBANOVA_API_KEY"}, true
	case "opencode-go":
		return config.ProviderConfig{BaseURL: "https://opencode.ai/zen/go/v1", APIKeyEnv: "OPENCODE_API_KEY"}, true
	default:
		return config.ProviderConfig{}, false
	}
}

// authSource describes where credentials for a provider are coming from
// (env var, auth.json, oauth, or none). Used by doctor and status output.
func authSource(name string, pc config.ProviderConfig, auth *config.Auth) string {
	if pc.APIKeyEnv != "" && strings.TrimSpace(os.Getenv(pc.APIKeyEnv)) != "" {
		return "env:" + pc.APIKeyEnv
	}
	if auth != nil {
		if strings.TrimSpace(auth.Keys[name]) != "" {
			return "auth.json"
		}
		if _, ok := auth.OAuth[name]; ok {
			return "oauth"
		}
	}
	return "none"
}

// loadAuthOrEmpty returns the persisted auth config, or a safe empty
// config if none exists.
func loadAuthOrEmpty() *config.Auth {
	auth, _ := config.LoadAuth()
	if auth == nil {
		return &config.Auth{Keys: map[string]string{}, OAuth: map[string]oauth.OpenAICodexCredentials{}}
	}
	if auth.Keys == nil {
		auth.Keys = map[string]string{}
	}
	if auth.OAuth == nil {
		auth.OAuth = map[string]oauth.OpenAICodexCredentials{}
	}
	return auth
}
