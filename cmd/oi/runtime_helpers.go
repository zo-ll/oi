// Package main (continued) — runtime construction and model introspection
// helpers shared across the CLI subcommands and chat mode.
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
		Logger:                  logger,
	}
}

func supportedThinkingLevels(model provider.Model) []string {
	if !model.SupportsThinking {
		return []string{"off"}
	}
	levels := []string{"off", "low", "medium", "high"}
	if len(model.SupportedThinkingLevels) > 0 {
		levels = append([]string(nil), model.SupportedThinkingLevels...)
	}
	if !containsString(levels, "off") {
		levels = append([]string{"off"}, levels...)
	}
	return levels
}

func thinkingValue(model provider.Model, level string) string {
	if model.ThinkingLevelValues != nil {
		if value, ok := model.ThinkingLevelValues[level]; ok {
			return value
		}
	}
	return level
}

func clampThinkingLevel(model provider.Model, level string) string {
	levels := supportedThinkingLevels(model)
	if len(levels) == 0 {
		return "off"
	}
	if level == "" {
		level = "off"
	}
	if containsString(levels, level) {
		return level
	}
	for _, fallback := range []string{"medium", "high", "low", "off"} {
		if containsString(levels, fallback) {
			return fallback
		}
	}
	return levels[0]
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
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
