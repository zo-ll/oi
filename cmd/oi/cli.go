package main

import (
	"fmt"
	"io"
	"runtime"
	"strings"

	"github.com/zo-ll/oi/internal/chat"
)

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

func printVersion(w io.Writer) {
	fmt.Fprintf(w, "oi %s\n", version)
	fmt.Fprintf(w, "commit: %s\n", commit)
	fmt.Fprintf(w, "built: %s\n", date)
	fmt.Fprintf(w, "go: %s\n", runtime.Version())
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
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
