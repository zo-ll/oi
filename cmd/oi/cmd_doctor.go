// Package main (continued) — oi doctor: inspect config, auth, and provider state.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/zo-ll/oi/internal/config"
	iprovider "github.com/zo-ll/oi/internal/provider"
	"github.com/zo-ll/oi/internal/workspace"
)

// runDoctor prints a diagnostic overview: config/auth paths, workspace root,
// configured providers, selected provider/model, and a connectivity check.
func runDoctor(args []string, w io.Writer) error {
	opts, err := parseCommonOptions("doctor", args)
	if err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	auth, err := config.LoadAuth()
	if err != nil {
		return err
	}
	sel, err := config.ResolveSelection(cfg, auth, opts.provider, opts.model, opts.apiKey)
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	root, err := workspace.DetectRoot(cwd)
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "config: %s\n", config.ConfigPath())
	fmt.Fprintf(w, "auth: %s\n", config.AuthPath())
	fmt.Fprintf(w, "state: %s\n", config.StateDir())
	fmt.Fprintf(w, "workspace: %s\n", root)

	providers := config.ProviderNames(cfg)
	if len(providers) == 0 {
		fmt.Fprintln(w, "providers: (none configured)")
	} else {
		fmt.Fprintf(w, "providers: %s\n", strings.Join(providers, ", "))
	}

	if sel.Provider == "" {
		fmt.Fprintln(w, "selected provider: (none)")
	} else {
		fmt.Fprintf(w, "selected provider: %s\n", sel.Provider)
		fmt.Fprintf(w, "selected model: %s\n", valueOr(sel.Model, "(none)"))
		fmt.Fprintf(w, "base url: %s\n", valueOr(sel.BaseURL, "(none)"))
		fmt.Fprintf(w, "api key: %s\n", yesNo(sel.APIKey != ""))
		fmt.Fprintf(w, "connectivity: %s\n", doctorConnectivity(sel))
	}

	fmt.Fprintf(w, "tool timeout seconds: %d\n", cfg.Agent.ToolTimeoutSeconds)
	fmt.Fprintf(w, "request timeout seconds: %d\n", cfg.Agent.RequestTimeoutSeconds)
	fmt.Fprintf(w, "approval mode: %s\n", cfg.Agent.ApprovalMode)
	return nil
}

// doctorConnectivity probes the selected provider by listing models with
// a 10s timeout. Returns a human-readable result.
func doctorConnectivity(sel config.Selection) string {
	if sel.Provider == "" {
		return "skipped (no provider selected)"
	}
	if sel.APIKey == "" {
		return "skipped (no API key)"
	}
	p, err := iprovider.NewForSelection(sel)
	if err != nil {
		return "error: " + err.Error()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	models, err := p.ListModels(ctx)
	if err != nil {
		return "error: " + err.Error()
	}
	return fmt.Sprintf("ok (%d models)", len(models))
}
