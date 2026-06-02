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
	tools := toolVerbosityErrors
	logger, err := maybeDebugLogger("chat", opts.debug)
	if err != nil {
		return err
	}
	promptInput := &promptInput{ui: ui, reader: reader}
	rt := buildRuntime(cfg, sel, p, root, promptInput, ui, logger)
	configureChatRuntime(rt, ui, tools)
	contextWindow := lookupContextWindow(p, sel.Model)
	ui.notify(styleText(ui, "dim", formatHeader(sel.Model, root, contextWindow)))
	if startupNotice != "" {
		ui.notify(startupNotice)
	}
	lastAssistant := ""

	for {
		line, err := ui.readMessage(lastAssistant)
		if err != nil {
			_ = ui.suspendRaw()
			return exitChat(ui, rt, sel, autosave)
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		ui.commitInput(line)
		if strings.HasPrefix(line, "/") {
			if err := ui.suspendRaw(); err != nil {
				return err
			}
			exit, newRT, newSel, newStreaming, newAutosave, newTools, cmdErr := handleChatCommand(deps, cfg, sel, rt, reader, ui, line, streaming, autosave, tools)
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
				tools = newTools
				contextWindow = lookupContextWindow(rt.Provider, sel.Model)
				configureChatRuntime(rt, ui, tools)
			}
			if exit {
				return nil
			}
			continue
		}

		ctx := context.Background()
		if streaming {
			formatter := &outputFormatter{}
			resp, runErr := rt.RunOnceStream(ctx, line, func(delta string) {
				fmt.Fprint(ui, formatter.Push(delta))
			})
			fmt.Fprint(ui, formatter.Flush())
			fmt.Fprintln(ui)
			if runErr != nil {
				ui.notify("error: " + runErr.Error())
			} else {
				lastAssistant = cleanDisplayText(resp)
				if ctx := formatContextUsage(contextWindow, rt.LastUsage); ctx != "" {
					ui.notify(styleText(ui, "dim", ctx))
				}
				ui.blankLine()
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
				resp = cleanDisplayText(resp)
				lastAssistant = resp
				ui.notify(resp)
				if ctx := formatContextUsage(contextWindow, rt.LastUsage); ctx != "" {
					ui.notify(styleText(ui, "dim", ctx))
				}
				ui.blankLine()
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
	tools := toolVerbosityErrors
	logger, err := maybeDebugLogger("chat", opts.debug)
	if err != nil {
		return err
	}
	rt := buildRuntime(cfg, sel, p, root, reader, out, logger)
	configureChatRuntime(rt, out, tools)
	contextWindow := lookupContextWindow(p, sel.Model)

	fmt.Fprintln(out, formatHeader(sel.Model, root, contextWindow))
	if startupNotice != "" {
		fmt.Fprintln(out, startupNotice)
	}

	for {
		fmt.Fprint(out, "> ")
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			if err == io.EOF {
				fmt.Fprintln(out)
				return exitChat(out, rt, sel, autosave)
			}
			continue
		}
		if strings.HasPrefix(line, "/") {
			exit, newRT, newSel, newStreaming, newAutosave, newTools, cmdErr := handleChatCommand(deps, cfg, sel, rt, reader, out, line, streaming, autosave, tools)
			if cmdErr != nil {
				fmt.Fprintf(out, "error: %v\n", cmdErr)
			} else {
				rt = newRT
				sel = newSel
				streaming = newStreaming
				autosave = newAutosave
				tools = newTools
				contextWindow = lookupContextWindow(rt.Provider, sel.Model)
				configureChatRuntime(rt, out, tools)
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
			formatter := &outputFormatter{}
			_, runErr := rt.RunOnceStream(ctx, line, func(delta string) {
				fmt.Fprint(out, formatter.Push(delta))
			})
			fmt.Fprint(out, formatter.Flush())
			fmt.Fprintln(out)
			if runErr != nil {
				fmt.Fprintf(out, "error: %v\n", runErr)
			} else {
				if ctx := formatContextUsage(contextWindow, rt.LastUsage); ctx != "" {
					fmt.Fprintln(out, ctx)
				}
				fmt.Fprintln(out)
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
				fmt.Fprintln(out, cleanDisplayText(resp))
				if ctx := formatContextUsage(contextWindow, rt.LastUsage); ctx != "" {
					fmt.Fprintln(out, ctx)
				}
				fmt.Fprintln(out)
				if autosave {
					if _, saveErr := saveSession(rt, sel); saveErr != nil {
						fmt.Fprintf(out, "warning: autosave failed: %v\n", saveErr)
					}
				}
			}
		}
		if err == io.EOF {
			return exitChat(out, rt, sel, autosave)
		}
	}
}
