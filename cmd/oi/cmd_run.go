package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/zo-ll/oi/internal/agent"
	irpc "github.com/zo-ll/oi/internal/rpc"
	"github.com/zo-ll/oi/internal/tool"
	"github.com/zo-ll/oi/internal/workspace"
)

func runTask(args []string, stdin io.Reader, w io.Writer) error {
	opts, err := parseCommonOptions("run", args)
	if err != nil {
		return err
	}
	if opts.jsonOut && opts.ndjson {
		return fmt.Errorf("use only one of --json or --ndjson")
	}
	prompt := strings.TrimSpace(strings.Join(opts.rest, " "))
	if prompt == "" {
		return fmt.Errorf("usage: oi run [--provider NAME] [--model NAME] [--api-key KEY] [--json|--ndjson] \"task\"")
	}
	cfg, sel, err := loadSelection(opts)
	if err != nil {
		return emitRunError(w, opts, err)
	}
	p, err := requireProvider(sel)
	if err != nil {
		return emitRunError(w, opts, err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return emitRunError(w, opts, err)
	}
	root, err := workspace.DetectRoot(cwd)
	if err != nil {
		return emitRunError(w, opts, err)
	}
	logger, err := maybeDebugLogger("run", opts.debug)
	if err != nil {
		return emitRunError(w, opts, err)
	}
	runtime := buildRuntime(cfg, sel, p, root, stdin, w, logger)

	switch {
	case opts.ndjson:
		enc := irpc.NewEncoder(w)
		if err := enc.Encode(irpc.Event{Type: "started", Data: runStateData(runtime)}); err != nil {
			return err
		}
		runtime.OnToolStart = func(call tool.Call) {
			_ = enc.Encode(irpc.Event{Type: "tool_start", Data: map[string]any{"name": call.Name, "args": runJSONRaw(call.Args)}})
		}
		runtime.OnToolResult = func(call tool.Call, result tool.Result) {
			_ = enc.Encode(irpc.Event{Type: "tool_result", Data: map[string]any{"name": call.Name, "result": result}})
		}
		out, err := runtime.RunOnceStreamObserved(context.Background(), prompt, agent.StreamObserver{Delta: func(delta string, reasoning bool) {
			if reasoning || strings.TrimSpace(delta) == "" {
				return
			}
			_ = enc.Encode(irpc.Event{Type: "assistant_delta", Delta: delta})
		}, StepDone: func(bool) {}})
		if err != nil {
			_ = enc.Encode(irpc.Event{Type: "error", Error: err.Error()})
			_ = enc.Encode(irpc.Event{Type: "done"})
			return cliError{err: err, printed: true, code: 1}
		}
		_ = enc.Encode(irpc.Event{Type: "assistant_done", Message: out})
		_ = enc.Encode(irpc.Event{Type: "done", Data: runStateData(runtime)})
		return nil
	case opts.jsonOut:
		out, err := runtime.RunOnce(context.Background(), prompt)
		if err != nil {
			payload := map[string]any{"ok": false, "error": err.Error(), "provider": sel.Provider, "model": p.Model(), "workspace": root}
			if encErr := json.NewEncoder(w).Encode(payload); encErr != nil {
				return err
			}
			return cliError{err: err, printed: true, code: 1}
		}
		payload := map[string]any{
			"ok":        true,
			"message":   out,
			"provider":  sel.Provider,
			"model":     p.Model(),
			"workspace": root,
		}
		if runtime.Session != nil {
			payload["session_id"] = runtime.Session.ID
			payload["message_count"] = len(runtime.Session.Messages)
		}
		return json.NewEncoder(w).Encode(payload)
	default:
		out, err := runtime.RunOnce(context.Background(), prompt)
		if err != nil {
			return err
		}
		fmt.Fprintln(w, out)
		return nil
	}
}

func emitRunError(w io.Writer, opts commonOptions, err error) error {
	if !opts.jsonOut && !opts.ndjson {
		return err
	}
	if opts.ndjson {
		enc := irpc.NewEncoder(w)
		_ = enc.Encode(irpc.Event{Type: "error", Error: err.Error()})
		_ = enc.Encode(irpc.Event{Type: "done"})
		return cliError{err: err, printed: true, code: 1}
	}
	payload := map[string]any{"ok": false, "error": err.Error()}
	if encErr := json.NewEncoder(w).Encode(payload); encErr != nil {
		return err
	}
	return cliError{err: err, printed: true, code: 1}
}

func runStateData(runtime *agent.Runtime) map[string]any {
	data := map[string]any{}
	if runtime == nil {
		return data
	}
	if runtime.Provider != nil {
		data["provider"] = runtime.Provider.Name()
		data["model"] = runtime.Provider.Model()
	}
	data["workspace"] = runtime.Policy.Root
	if runtime.Session != nil {
		data["session_id"] = runtime.Session.ID
		data["message_count"] = len(runtime.Session.Messages)
	}
	return data
}

func runJSONRaw(raw []byte) any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var v any
	if err := json.Unmarshal(raw, &v); err == nil {
		return v
	}
	return string(raw)
}

func runRPC(in io.Reader, w io.Writer) error {
	srv, err := irpc.NewServer()
	if err != nil {
		return err
	}
	return srv.Serve(in, w)
}
