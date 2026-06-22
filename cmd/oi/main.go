package main

import (
	"fmt"
	"io"
	"os"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

type cliError struct {
	err     error
	printed bool
	code    int
}

func (e cliError) Error() string {
	if e.err == nil {
		return ""
	}
	return e.err.Error()
}

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

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	return dispatch(args, stdin, stdout, stderr)
}
