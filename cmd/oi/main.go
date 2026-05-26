package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/zo-ll/oi/internal/config"
	iprovider "github.com/zo-ll/oi/internal/provider"
	"github.com/zo-ll/oi/internal/workspace"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "oi:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printUsage(stdout)
		return nil
	}

	switch args[0] {
	case "help", "--help", "-h":
		printUsage(stdout)
		return nil
	case "doctor":
		return runDoctor(args[1:], stdout)
	case "models":
		return runModels(args[1:], stdout)
	case "chat", "run", "rpc":
		return fmt.Errorf("%s: not implemented yet", args[0])
	default:
		return fmt.Errorf("unknown command: %s", args[0])
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "oi")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  oi help")
	fmt.Fprintln(w, "  oi doctor")
	fmt.Fprintln(w, "  oi chat")
	fmt.Fprintln(w, "  oi run \"task\"")
	fmt.Fprintln(w, "  oi rpc")
	fmt.Fprintln(w, "  oi models")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Current status: scaffolded rebuild; doctor and models are available.")
}

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

	fmt.Fprintf(w, "agent max steps: %d\n", cfg.Agent.MaxSteps)
	fmt.Fprintf(w, "tool timeout seconds: %d\n", cfg.Agent.ToolTimeoutSeconds)
	fmt.Fprintf(w, "approval mode: %s\n", cfg.Agent.ApprovalMode)
	return nil
}

func runModels(args []string, w io.Writer) error {
	opts, err := parseCommonOptions("models", args)
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
	if sel.Provider == "" {
		return fmt.Errorf("no provider selected")
	}
	if sel.APIKey == "" {
		return fmt.Errorf("no API key resolved for provider %q", sel.Provider)
	}
	p, err := iprovider.NewOpenAI(sel.Provider, sel.BaseURL, sel.APIKey, sel.Model)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	models, err := p.ListModels(ctx)
	if err != nil {
		return err
	}
	for _, m := range models {
		fmt.Fprintln(w, m.ID)
	}
	return nil
}

func doctorConnectivity(sel config.Selection) string {
	if sel.Provider == "" {
		return "skipped (no provider selected)"
	}
	if sel.APIKey == "" {
		return "skipped (no API key)"
	}
	p, err := iprovider.NewOpenAI(sel.Provider, sel.BaseURL, sel.APIKey, sel.Model)
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

func parseCommonOptions(name string, args []string) (commonOptions, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var opts commonOptions
	fs.StringVar(&opts.provider, "provider", "", "provider name")
	fs.StringVar(&opts.model, "model", "", "model name")
	fs.StringVar(&opts.apiKey, "api-key", "", "API key override")
	if err := fs.Parse(args); err != nil {
		return commonOptions{}, err
	}
	return opts, nil
}

type commonOptions struct {
	provider string
	model    string
	apiKey   string
}

func valueOr(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func yesNo(ok bool) string {
	if ok {
		return "yes"
	}
	return "no"
}
