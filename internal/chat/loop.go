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
	if ui, ok := newTerminalUI(in, out); ok {
		defer ui.Close()
		return runTUIMode(args, in, out, ui, deps)
	}
	return runLineMode(args, in, out, deps)
}

func runTUIMode(args []string, in io.Reader, out io.Writer, ui *terminalUI, deps Dependencies) error {
	opts, err := parseCommonOptions("", args)
	if err != nil {
		return err
	}
	cfg, sel, err := loadSelection(opts)
	if err != nil {
		return err
	}
	p, startupNotice, err := interactiveProvider(sel)
	if err != nil {
		return err
	}
	root, err := workspace.DetectRoot("")
	if err != nil {
		return err
	}
	if err := ui.enableRawMode(); err != nil {
		return runLineMode(args, in, out, deps)
	}
	defer ui.disableRawMode()
	reader := bufio.NewReader(in)
	streaming := true
	autosave := true
	logger, err := maybeDebugLogger("chat", opts.debug)
	if err != nil {
		return err
	}
	promptInput := &promptInput{ui: ui, reader: reader}
	rt := buildRuntime(cfg, sel, p, root, promptInput, ui, logger)
	configureChatRuntime(rt, ui)
	ui.notify(fmt.Sprintf("oi\nprovider: %s\nmodel: %s\nworkspace: %s", valueOr(sel.Provider, "(none)"), valueOr(sel.Model, "(none)"), root))
	if startupNotice != "" {
		ui.notify(startupNotice)
	}
	lastAssistant := ""

	for {
		line, err := ui.readMessage(lastAssistant)
		if err != nil {
			_ = ui.suspendRaw()
			return exitChat(reader, ui, rt, sel, autosave)
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		ui.commitInput(line)
		if strings.HasPrefix(line, "/") {
			if err := ui.suspendRaw(); err != nil {
				return err
			}
			exit, newRT, newSel, newStreaming, newAutosave, cmdErr := handleChatCommand(deps, cfg, sel, rt, reader, ui, line, streaming, autosave)
			if resumeErr := ui.resumeRaw(); resumeErr != nil {
				return resumeErr
			}
			if cmdErr != nil {
				ui.notify("error: " + cmdErr.Error())
			} else {
				rt = newRT
				sel = newSel
				streaming = newStreaming
				autosave = newAutosave
				configureChatRuntime(rt, ui)
			}
			if exit {
				return nil
			}
			continue
		}

		ctx := context.Background()
		if streaming {
			resp, runErr := rt.RunOnceStream(ctx, line, func(delta string) {
				fmt.Fprint(ui, delta)
			})
			fmt.Fprintln(ui)
			if runErr != nil {
				ui.notify("error: " + runErr.Error())
			} else {
				lastAssistant = resp
				if autosave {
					if _, saveErr := saveSession(rt, sel); saveErr != nil {
						ui.notify("warning: autosave failed: " + saveErr.Error())
					}
				}
			}
		} else {
			resp, runErr := rt.RunOnce(ctx, line)
			if runErr != nil {
				ui.notify("error: " + runErr.Error())
			} else {
				lastAssistant = resp
				ui.notify(resp)
				if autosave {
					if _, saveErr := saveSession(rt, sel); saveErr != nil {
						ui.notify("warning: autosave failed: " + saveErr.Error())
					}
				}
			}
		}
	}
}

func runLineMode(args []string, in io.Reader, out io.Writer, deps Dependencies) error {
	opts, err := parseCommonOptions("", args)
	if err != nil {
		return err
	}
	cfg, sel, err := loadSelection(opts)
	if err != nil {
		return err
	}
	p, startupNotice, err := interactiveProvider(sel)
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

	fmt.Fprintf(out, "oi\nprovider: %s\nmodel: %s\nworkspace: %s\n", valueOr(sel.Provider, "(none)"), valueOr(sel.Model, "(none)"), root)
	if startupNotice != "" {
		fmt.Fprintln(out, startupNotice)
	}
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
			_, runErr := rt.RunOnceStream(ctx, line, func(delta string) {
				fmt.Fprint(out, delta)
			})
			fmt.Fprintln(out)
			if runErr != nil {
				fmt.Fprintf(out, "error: %v\n", runErr)
			} else if autosave {
				if _, saveErr := saveSession(rt, sel); saveErr != nil {
					fmt.Fprintf(out, "warning: autosave failed: %v\n", saveErr)
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
