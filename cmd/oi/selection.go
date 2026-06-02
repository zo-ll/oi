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

type commonOptions struct {
	provider string
	model    string
	apiKey   string
	debug    bool
	rest     []string
}

func parseCommonOptions(name string, args []string) (commonOptions, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var opts commonOptions
	fs.StringVar(&opts.provider, "provider", "", "provider name")
	fs.StringVar(&opts.model, "model", "", "model name")
	fs.StringVar(&opts.apiKey, "api-key", "", "API key override")
	fs.BoolVar(&opts.debug, "debug", false, "enable debug logging")
	if err := fs.Parse(args); err != nil {
		return commonOptions{}, err
	}
	opts.rest = fs.Args()
	return opts, nil
}

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

func requireProvider(sel config.Selection) (iprovider.Provider, error) {
	return iprovider.NewForSelection(sel)
}

func canonicalProviderName(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "chatgpt", "codex", "openai-browser", "openai-chatgpt":
		return "openai-codex"
	default:
		return strings.TrimSpace(name)
	}
}

func knownProviderProfile(name string) (config.ProviderConfig, bool) {
	switch canonicalProviderName(name) {
	case "openai":
		return config.ProviderConfig{BaseURL: "https://api.openai.com/v1", APIKeyEnv: "OPENAI_API_KEY"}, true
	case "openai-codex":
		return config.ProviderConfig{BaseURL: "https://chatgpt.com/backend-api"}, true
	case "opencode-go":
		return config.ProviderConfig{BaseURL: "https://opencode.ai/zen/go/v1", APIKeyEnv: "OPENCODE_API_KEY"}, true
	default:
		return config.ProviderConfig{}, false
	}
}

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
