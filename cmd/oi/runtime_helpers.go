package main

import (
	"context"
	"fmt"
	"io"
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

func buildRuntime(cfg *config.Config, sel config.Selection, p provider.Provider, root string, in io.Reader, out io.Writer, logger *ilog.Logger) *agent.Runtime {
	policy := workspace.Policy{Root: root, ApprovalMode: workspace.ApprovalMode(cfg.Agent.ApprovalMode)}
	tools := tool.NewBuiltinRegistry(tool.Options{
		Policy:         policy,
		MaxOutputBytes: cfg.Agent.MaxToolOutputBytes,
		PromptInput:    in,
		PromptOutput:   out,
	})
	model := sel.Model
	if p != nil && p.Model() != "" {
		model = p.Model()
	}
	info := lookupModelInfo(p, model)
	return &agent.Runtime{
		Provider:          p,
		Tools:             tools,
		Policy:            policy,
		Session:           session.New(sel.Provider, model, root),
		ToolTimeout:       time.Duration(cfg.Agent.ToolTimeoutSeconds) * time.Second,
		RequestTimeout:    time.Duration(cfg.Agent.RequestTimeoutSeconds) * time.Second,
		ContextWindow:     info.ContextWindow,
		ThinkingLevel:     cfg.Agent.ReasoningEffort,
		ThinkingSupported: info.SupportsThinking,
		Logger:            logger,
	}
}

func lookupModelInfo(p provider.Provider, model string) provider.Model {
	if p == nil || strings.TrimSpace(model) == "" {
		return provider.Model{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	models, err := p.ListModels(ctx)
	if err != nil {
		return provider.Model{}
	}
	for _, item := range models {
		if item.ID == model {
			return item
		}
	}
	return provider.Model{}
}

func maybeDebugLogger(mode string, enabled bool) (*ilog.Logger, error) {
	if !enabled {
		return nil, nil
	}
	path := filepath.Join(config.LogsDir(), fmt.Sprintf("%s-%s.jsonl", mode, time.Now().UTC().Format("20060102-150405")))
	return ilog.NewJSONL(path)
}
