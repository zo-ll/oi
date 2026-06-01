package chat

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/zo-ll/oi/internal/workspace"
)

func Run(args []string, in io.Reader, out io.Writer, deps Dependencies) error {
	opts, err := parseCommonOptions("chat", args)
	if err != nil {
		return err
	}
	cfg, sel, err := loadSelection(opts)
	if err != nil {
		return err
	}
	p, err := requireProvider(sel)
	if err != nil {
		return err
	}
	root, err := workspace.DetectRoot("")
	if err != nil {
		return err
	}
	reader := bufio.NewReader(in)
	streaming := true
	autosave := true
	logger, err := maybeDebugLogger("chat", opts.debug)
	if err != nil {
		return err
	}
	rt := buildRuntime(cfg, sel, p, root, reader, out, logger)
	configureChatRuntime(rt, out)

	fmt.Fprintf(out, "oi chat\nprovider: %s\nmodel: %s\nworkspace: %s\n", sel.Provider, valueOr(sel.Model, "(none)"), root)
	fmt.Fprintln(out, "Type /help for commands.")

	for {
		fmt.Fprint(out, "oi> ")
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			if err == io.EOF {
				fmt.Fprintln(out)
				return exitChat(reader, out, rt, sel, autosave)
			}
			continue
		}
		if strings.HasPrefix(line, "/") {
			exit, newRT, newSel, newStreaming, newAutosave, cmdErr := handleChatCommand(deps, cfg, sel, rt, reader, out, line, streaming, autosave)
			if cmdErr != nil {
				fmt.Fprintf(out, "error: %v\n", cmdErr)
			} else {
				rt = newRT
				sel = newSel
				streaming = newStreaming
				autosave = newAutosave
				configureChatRuntime(rt, out)
			}
			if exit {
				return nil
			}
			if err == io.EOF {
				return nil
			}
			continue
		}

		ctx := context.Background()
		if streaming {
			spinnerStop := startThinkingIndicator(out)
			startedOutput := false
			resp, runErr := rt.RunOnceStream(ctx, line, func(delta string) {
				if !startedOutput {
					spinnerStop()
					startedOutput = true
				}
				fmt.Fprint(out, delta)
			})
			spinnerStop()
			if runErr != nil {
				if startedOutput {
					fmt.Fprintln(out)
				}
				fmt.Fprintf(out, "error: %v\n", runErr)
			} else {
				if !startedOutput {
					fmt.Fprintln(out, resp)
				} else {
					fmt.Fprintln(out)
				}
				if autosave {
					if _, saveErr := saveSession(rt, sel); saveErr != nil {
						fmt.Fprintf(out, "warning: autosave failed: %v\n", saveErr)
					}
				}
			}
		} else {
			resp, runErr := rt.RunOnce(context.Background(), line)
			if runErr != nil {
				fmt.Fprintf(out, "error: %v\n", runErr)
			} else {
				fmt.Fprintln(out, resp)
				if autosave {
					if _, saveErr := saveSession(rt, sel); saveErr != nil {
						fmt.Fprintf(out, "warning: autosave failed: %v\n", saveErr)
					}
				}
			}
		}
		if err == io.EOF {
			return exitChat(reader, out, rt, sel, autosave)
		}
	}
}
