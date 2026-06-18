package chat

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zo-ll/oi/internal/agent"
	"github.com/zo-ll/oi/internal/config"
	ilog "github.com/zo-ll/oi/internal/log"
	"github.com/zo-ll/oi/internal/provider"
	"github.com/zo-ll/oi/internal/session"
	"github.com/zo-ll/oi/internal/tool"
	"github.com/zo-ll/oi/internal/workspace"
)

type Dependencies struct {
	Login func(args []string, in io.Reader, out io.Writer) error
}

type commonOptions struct {
	provider string
	model    string
	apiKey   string
	debug    bool
	rest     []string
}

type approvalPrompter interface {
	Approve(action, target string) (bool, error)
}

func buildRuntime(cfg *config.Config, sel config.Selection, p provider.Provider, root string, in io.Reader, out io.Writer, logger *ilog.Logger) *agent.Runtime {
	policy := workspace.Policy{Root: root, ApprovalMode: workspace.ApprovalMode(cfg.Agent.ApprovalMode)}
	toolOpts := tool.Options{
		Policy:         policy,
		MaxOutputBytes: cfg.Agent.MaxToolOutputBytes,
		PromptInput:    in,
		PromptOutput:   out,
	}
	if prompter, ok := out.(approvalPrompter); ok {
		toolOpts.Approve = prompter.Approve
	}
	tools := tool.NewBuiltinRegistry(toolOpts)
	model := sel.Model
	if p != nil && p.Model() != "" {
		model = p.Model()
	}
	info := lookupModelInfo(p, model)
	level := clampThinkingLevel(info, "off")
	return &agent.Runtime{
		Provider:                p,
		Tools:                   tools,
		Policy:                  policy,
		Session:                 session.New(sel.Provider, model, root),
		ToolTimeout:             time.Duration(cfg.Agent.ToolTimeoutSeconds) * time.Second,
		RequestTimeout:          time.Duration(cfg.Agent.RequestTimeoutSeconds) * time.Second,
		ContextWindow:           info.ContextWindow,
		ThinkingLevel:           level,
		ThinkingValue:           thinkingValue(info, level),
		ThinkingFormat:          info.ThinkingFormat,
		ThinkingSupported:       info.SupportsThinking,
		SupportedThinkingLevels: supportedThinkingLevels(info),
		ThinkingLevelValues:     info.ThinkingLevelValues,
		AutoCompactThreshold:    cfg.Agent.AutoCompactThreshold,
		Logger:                  logger,
	}
}

func maybeDebugLogger(mode string, enabled bool) (*ilog.Logger, error) {
	if !enabled {
		return nil, nil
	}
	path := filepath.Join(config.LogsDir(), fmt.Sprintf("%s-%s.jsonl", mode, time.Now().UTC().Format("20060102-150405")))
	return ilog.NewJSONL(path)
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

func requireProvider(sel config.Selection) (provider.Provider, error) {
	return provider.NewForSelection(sel)
}

func valueOr(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
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

func canonicalProviderName(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "chatgpt", "codex", "openai-browser", "openai-chatgpt":
		return "openai-codex"
	default:
		return strings.TrimSpace(name)
	}
}

func interactiveProvider(sel config.Selection) (provider.Provider, string, error) {
	if sel.Provider == "" {
		return nil, "No provider configured. Use /login to set one up.", nil
	}
	p, err := requireProvider(sel)
	if err != nil {
		return nil, fmt.Sprintf("Provider %s is not ready: %v. Use /login, then /model.", sel.Provider, err), nil
	}
	if strings.TrimSpace(sel.Model) == "" {
		return p, "No model selected. Use /model.", nil
	}
	if notice := selectedModelStartupNotice(p, sel.Model); notice != "" {
		return p, notice, nil
	}
	return p, "", nil
}

func selectedModelStartupNotice(p provider.Provider, model string) string {
	if p == nil || strings.TrimSpace(model) == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	models, err := p.ListModels(ctx)
	if err != nil || len(models) == 0 {
		return ""
	}
	for _, item := range models {
		if item.ID == model {
			return ""
		}
	}
	return fmt.Sprintf("Selected model %s is unavailable. Use /model.", model)
}
