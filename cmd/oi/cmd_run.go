package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	irpc "github.com/zo-ll/oi/internal/rpc"
	"github.com/zo-ll/oi/internal/workspace"
)

func runTask(args []string, stdin io.Reader, w io.Writer) error {
	opts, err := parseCommonOptions("run", args)
	if err != nil {
		return err
	}
	prompt := strings.TrimSpace(strings.Join(opts.rest, " "))
	if prompt == "" {
		return fmt.Errorf("usage: oi run [--provider NAME] [--model NAME] [--api-key KEY] \"task\"")
	}
	cfg, sel, err := loadSelection(opts)
	if err != nil {
		return err
	}
	p, err := requireProvider(sel)
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
	logger, err := maybeDebugLogger("run", opts.debug)
	if err != nil {
		return err
	}
	runtime := buildRuntime(cfg, sel, p, root, stdin, w, logger)
	out, err := runtime.RunOnce(context.Background(), prompt)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, out)
	return nil
}

func runRPC(in io.Reader, w io.Writer) error {
	srv, err := irpc.NewServer()
	if err != nil {
		return err
	}
	return srv.Serve(in, w)
}
