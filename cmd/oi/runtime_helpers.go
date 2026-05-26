package main

import (
	"io"
	"time"

	"github.com/zo-ll/oi/internal/agent"
	"github.com/zo-ll/oi/internal/config"
	"github.com/zo-ll/oi/internal/provider"
	"github.com/zo-ll/oi/internal/session"
	"github.com/zo-ll/oi/internal/tool"
	"github.com/zo-ll/oi/internal/workspace"
)

func buildRuntime(cfg *config.Config, sel config.Selection, p provider.Provider, root string, in io.Reader, out io.Writer) *agent.Runtime {
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
	return &agent.Runtime{
		Provider:    p,
		Tools:       tools,
		Policy:      policy,
		Session:     session.New(sel.Provider, model, root),
		MaxSteps:    cfg.Agent.MaxSteps,
		ToolTimeout: time.Duration(cfg.Agent.ToolTimeoutSeconds) * time.Second,
	}
}
