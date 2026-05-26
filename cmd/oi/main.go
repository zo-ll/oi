package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/zo-ll/oi/internal/config"
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
		return runDoctor(stdout)
	case "chat", "run", "rpc", "models":
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
	fmt.Fprintln(w, "Current status: scaffolded rebuild; doctor is available.")
}

func runDoctor(w io.Writer) error {
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
	sel, err := config.ResolveSelection(cfg, auth, "", "", "")
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
	}

	fmt.Fprintf(w, "agent max steps: %d\n", cfg.Agent.MaxSteps)
	fmt.Fprintf(w, "tool timeout seconds: %d\n", cfg.Agent.ToolTimeoutSeconds)
	fmt.Fprintf(w, "approval mode: %s\n", cfg.Agent.ApprovalMode)
	return nil
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
