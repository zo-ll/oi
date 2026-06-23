// Package main is the entry point for the oi CLI tool.
//
// oi is a small agent harness with three frontends over a shared core:
//   - oi              interactive fullscreen TUI
//   - oi run          one-shot machine-readable execution
//   - oi rpc          multi-session NDJSON stdio protocol
//
// Build-time variables are injected via linker flags (-ldflags):
//
//	go build -ldflags "-X main.version=v0.1.0 -X main.commit=abc123 -X main.date=2026-06-22T12:00:00Z"
//
// Without linker flags they default to dev/none/unknown.
package main

import (
	"fmt"
	"io"
	"os"
)

// Build-time variables set by -ldflags. Defaults are used when building
// without linker flags (e.g. during local development).
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// cliError is a structured error that carries an explicit exit code and
// controls whether the error message has already been printed to the user.
//
// It is used to avoid duplicate error output when a subcommand has already
// emitted a human-readable message but still needs to signal a non-zero exit.
type cliError struct {
	err     error
	printed bool // true if the error message was already printed to the user
	code    int  // exit code; if <= 0, defaults to 1
}

func (e cliError) Error() string {
	if e.err == nil {
		return ""
	}
	return e.err.Error()
}

// main is the process entry point. It delegates to run(), then handles errors
// by printing them to stderr and exiting with an appropriate code.
//
// cliError values get special treatment: if the error message was already
// printed by the subcommand (e.g. --json mode emits its own structured error),
// main skips the extra human-readable stderr line. This keeps machine-facing
// paths clean of duplicate noise.
func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		if ce, ok := err.(cliError); ok {
			if !ce.printed {
				fmt.Fprintln(os.Stderr, "oi:", ce.err)
			}
			code := ce.code
			if code <= 0 {
				code = 1
			}
			os.Exit(code)
		}
		fmt.Fprintln(os.Stderr, "oi:", err)
		os.Exit(1)
	}
}

// run wires stdin/stdout/stderr to the command dispatcher.
// It accepts injected readers/writers so tests can exercise the full
// CLI pipeline without real terminals.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	return dispatch(args, stdin, stdout, stderr)
}
