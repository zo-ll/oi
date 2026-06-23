// Package main (continued) — CLI command dispatch and shared helpers.
package main

import (
	"fmt"
	"io"
	"runtime"
	"strings"

	"github.com/zo-ll/oi/internal/chat"
)

// dispatch routes the top-level subcommand to the appropriate handler.
//
// When no subcommand is given (or args start with flags like --debug),
// the interactive chat/TUI mode is launched via chat.Run.
// Recognised subcommands: help, version, doctor, models, login, logout,
// run, rpc.
func dispatch(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return chat.Run(args, stdin, stdout, chat.Dependencies{Login: runLogin})
	}

	switch args[0] {
	case "help", "--help", "-h":
		printUsage(stdout)
		return nil
	case "version", "--version", "-v":
		printVersion(stdout)
		return nil
	case "doctor":
		return runDoctor(args[1:], stdout)
	case "models":
		return runModels(args[1:], stdout)
	case "login":
		return runLogin(args[1:], stdin, stdout)
	case "logout":
		return runLogout(args[1:], stdout)
	case "run":
		return runTask(args[1:], stdin, stdout)
	case "rpc":
		return runRPC(stdin, stdout)
	case "chat":
		return fmt.Errorf("`oi chat` was removed; use `oi`")
	default:
		return fmt.Errorf("unknown command: %s", args[0])
	}
}

// printUsage writes the top-level help text.
func printUsage(w io.Writer) {
	fmt.Fprintln(w, "oi")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  oi [--debug] [--provider NAME] [--model NAME]  # interactive mode")
	fmt.Fprintln(w, "  oi help")
	fmt.Fprintln(w, "  oi doctor")
	fmt.Fprintln(w, "  oi models")
	fmt.Fprintln(w, "  oi login [provider]")
	fmt.Fprintln(w, "  oi logout [provider]")
	fmt.Fprintln(w, "  oi version")
	fmt.Fprintln(w, "  oi run [--json|--ndjson] \"task\"")
	fmt.Fprintln(w, "  oi rpc")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Current status: interactive mode is the default; doctor, models, login, logout, version, run, and rpc are available.")
}

// printVersion writes the build version, commit hash, build timestamp,
// and Go runtime version.
func printVersion(w io.Writer) {
	fmt.Fprintf(w, "oi %s\n", version)
	fmt.Fprintf(w, "commit: %s\n", commit)
	fmt.Fprintf(w, "built: %s\n", date)
	fmt.Fprintf(w, "go: %s\n", runtime.Version())
}

// firstNonEmpty returns the first string in values that is not blank.
// Returns "" if all values are empty or whitespace.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// valueOr returns v if non-empty, otherwise fallback.
func valueOr(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// yesNo returns "yes" for true, "no" for false.
func yesNo(ok bool) string {
	if ok {
		return "yes"
	}
	return "no"
}
