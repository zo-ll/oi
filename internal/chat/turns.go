package chat

import (
	"context"
	"fmt"
	"io"

	"github.com/zo-ll/oi/internal/agent"
	"github.com/zo-ll/oi/internal/config"
)

type chatState struct {
	cfg           *config.Config
	sel           config.Selection
	rt            *agent.Runtime
	streaming     bool
	autosave      bool
	tools         toolVerbosity
	contextWindow int
	lastAssistant string
}

func newChatState(cfg *config.Config, sel config.Selection, rt *agent.Runtime) *chatState {
	return &chatState{
		cfg:       cfg,
		sel:       sel,
		rt:        rt,
		streaming: true,
		autosave:  true,
		tools:     toolVerbosityErrors,
	}
}

func (s *chatState) reconfigureRuntime(out io.Writer) {
	s.contextWindow = lookupContextWindow(s.rt.Provider, s.sel.Model)
	configureChatRuntime(s.rt, out, s.tools)
}

func (s *chatState) applyCommandResult(newRT *agent.Runtime, newSel config.Selection, newStreaming, newAutosave bool, newTools toolVerbosity, out io.Writer) {
	s.rt = newRT
	s.sel = newSel
	s.streaming = newStreaming
	s.autosave = newAutosave
	s.tools = newTools
	s.reconfigureRuntime(out)
}

func (s *chatState) autosaveSession(out io.Writer, warn func(string)) {
	if !s.autosave {
		return
	}
	if _, err := saveSession(s.rt, s.sel); err != nil {
		warn(fmt.Sprintf("warning: autosave failed: %v", err))
	}
}

func runStreamingTurnTUI(ui *terminalUI, state *chatState, line string) {
	formatter := &outputFormatter{}
	resp, runErr := state.rt.RunOnceStream(context.Background(), line, func(delta string) {
		fmt.Fprint(ui, formatter.Push(delta))
	})
	fmt.Fprint(ui, formatter.Flush())
	fmt.Fprintln(ui)
	if runErr != nil {
		ui.notify("error: " + runErr.Error())
		return
	}
	state.lastAssistant = cleanDisplayText(resp)
	if ctx := formatContextUsage(state.contextWindow, state.rt.LastUsage); ctx != "" {
		ui.notify(styleText(ui, "dim", ctx))
	}
	ui.blankLine()
	state.autosaveSession(ui, ui.notify)
}

func runNonStreamingTurnTUI(ui *terminalUI, state *chatState, line string) {
	resp, runErr := state.rt.RunOnce(context.Background(), line)
	if runErr != nil {
		ui.notify("error: " + runErr.Error())
		return
	}
	resp = cleanDisplayText(resp)
	state.lastAssistant = resp
	ui.notify(resp)
	if ctx := formatContextUsage(state.contextWindow, state.rt.LastUsage); ctx != "" {
		ui.notify(styleText(ui, "dim", ctx))
	}
	ui.blankLine()
	state.autosaveSession(ui, ui.notify)
}

func runStreamingTurnLine(out io.Writer, state *chatState, line string) {
	formatter := &outputFormatter{}
	_, runErr := state.rt.RunOnceStream(context.Background(), line, func(delta string) {
		fmt.Fprint(out, formatter.Push(delta))
	})
	fmt.Fprint(out, formatter.Flush())
	fmt.Fprintln(out)
	if runErr != nil {
		fmt.Fprintf(out, "error: %v\n", runErr)
		return
	}
	if ctx := formatContextUsage(state.contextWindow, state.rt.LastUsage); ctx != "" {
		fmt.Fprintln(out, ctx)
	}
	fmt.Fprintln(out)
	state.autosaveSession(out, func(msg string) { fmt.Fprintln(out, msg) })
}

func runNonStreamingTurnLine(out io.Writer, state *chatState, line string) {
	resp, runErr := state.rt.RunOnce(context.Background(), line)
	if runErr != nil {
		fmt.Fprintf(out, "error: %v\n", runErr)
		return
	}
	fmt.Fprintln(out, cleanDisplayText(resp))
	if ctx := formatContextUsage(state.contextWindow, state.rt.LastUsage); ctx != "" {
		fmt.Fprintln(out, ctx)
	}
	fmt.Fprintln(out)
	state.autosaveSession(out, func(msg string) { fmt.Fprintln(out, msg) })
}
