package chat

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/zo-ll/oi/internal/agent"
	"github.com/zo-ll/oi/internal/config"
	"github.com/zo-ll/oi/internal/session"
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

func (s *chatState) header(root string) string {
	level := ""
	if s != nil && s.rt != nil {
		level = s.rt.ThinkingLevel
	}
	return formatHeader(s.sel.Model, root, s.contextWindow, level)
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

type turnResult struct {
	resp string
	err  error
}

func runAbortableTUI(ui *terminalUI, run func(context.Context) (string, error)) (string, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := make(chan turnResult, 1)
	go func() {
		resp, err := run(ctx)
		ch <- turnResult{resp: resp, err: err}
	}()
	if err := setTerminalNonblock(ui.in, true); err != nil {
		res := <-ch
		return res.resp, res.err
	}
	defer func() { _ = setTerminalNonblock(ui.in, false) }()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	aborted := false
	for {
		select {
		case res := <-ch:
			if aborted {
				return res.resp, fmt.Errorf("aborted")
			}
			return res.resp, res.err
		case <-ticker.C:
			b, err := readByte(ui.in)
			if err != nil {
				if isWouldBlock(err) {
					continue
				}
				continue
			}
			if b == 27 || b == 3 {
				aborted = true
				cancel()
				ui.notify("aborting...")
			}
		}
	}
}

func setTerminalNonblock(f *os.File, enabled bool) error {
	return syscall.SetNonblock(int(f.Fd()), enabled)
}

func isWouldBlock(err error) bool {
	if pathErr, ok := err.(*os.PathError); ok {
		err = pathErr.Err
	}
	return err == syscall.EAGAIN || err == syscall.EWOULDBLOCK
}

func runStreamingTurnTUI(ui *terminalUI, state *chatState, line string) {
	renderer := &taggedStreamRenderer{}
	resp, runErr := runAbortableTUI(ui, func(ctx context.Context) (string, error) {
		return state.rt.RunOnceStream(ctx, line, func(delta string) {
			for _, seg := range renderer.Push(delta) {
				writeResponseSegment(ui, seg)
			}
		})
	})
	for _, seg := range renderer.Flush() {
		writeResponseSegment(ui, seg)
	}
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
	resp, runErr := runAbortableTUI(ui, func(ctx context.Context) (string, error) {
		return state.rt.RunOnce(ctx, line)
	})
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
	renderer := &taggedStreamRenderer{}
	_, runErr := state.rt.RunOnceStream(context.Background(), line, func(delta string) {
		for _, seg := range renderer.Push(delta) {
			writeResponseSegment(out, seg)
		}
	})
	for _, seg := range renderer.Flush() {
		writeResponseSegment(out, seg)
	}
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

type responseSegment struct {
	text      string
	reasoning bool
}

type taggedStreamRenderer struct {
	pending string
	inThink bool
	normal  outputFormatter
	thought outputFormatter
}

func (r *taggedStreamRenderer) Push(delta string) []responseSegment {
	r.pending += delta
	var out []responseSegment
	for {
		if r.inThink {
			idx := strings.Index(r.pending, "</think>")
			if idx < 0 {
				keep := partialTagSuffix(r.pending, "</think>")
				appendResponseSegment(&out, r.thought.Push(r.pending[:len(r.pending)-keep]), true)
				r.pending = r.pending[len(r.pending)-keep:]
				return out
			}
			appendResponseSegment(&out, r.thought.Push(r.pending[:idx]), true)
			appendResponseSegment(&out, r.thought.Flush(), true)
			r.pending = r.pending[idx+len("</think>"):]
			r.inThink = false
			continue
		}
		idx := strings.Index(r.pending, "<think>")
		if idx < 0 {
			keep := partialTagSuffix(r.pending, "<think>")
			appendResponseSegment(&out, r.normal.Push(r.pending[:len(r.pending)-keep]), false)
			r.pending = r.pending[len(r.pending)-keep:]
			return out
		}
		appendResponseSegment(&out, r.normal.Push(r.pending[:idx]), false)
		appendResponseSegment(&out, r.normal.Flush(), false)
		r.pending = r.pending[idx+len("<think>"):]
		r.inThink = true
	}
}

func (r *taggedStreamRenderer) Flush() []responseSegment {
	var out []responseSegment
	if r.pending != "" {
		if r.inThink {
			appendResponseSegment(&out, r.thought.Push(r.pending), true)
		} else {
			appendResponseSegment(&out, r.normal.Push(r.pending), false)
		}
		r.pending = ""
	}
	appendResponseSegment(&out, r.normal.Flush(), false)
	appendResponseSegment(&out, r.thought.Flush(), true)
	return out
}

func partialTagSuffix(s, tag string) int {
	max := len(tag) - 1
	if max > len(s) {
		max = len(s)
	}
	for n := max; n > 0; n-- {
		if strings.HasSuffix(s, tag[:n]) {
			return n
		}
	}
	return 0
}

func appendResponseSegment(out *[]responseSegment, text string, reasoning bool) {
	if text == "" {
		return
	}
	if n := len(*out); n > 0 && (*out)[n-1].reasoning == reasoning {
		(*out)[n-1].text += text
		return
	}
	*out = append(*out, responseSegment{text: text, reasoning: reasoning})
}

func writeResponseSegment(out io.Writer, seg responseSegment) {
	if seg.text == "" {
		return
	}
	if seg.reasoning {
		fmt.Fprint(out, styleText(out, "dim", seg.text))
		return
	}
	fmt.Fprint(out, seg.text)
}

func lastAssistantMessage(rt *agent.Runtime) *session.Message {
	if rt == nil || rt.Session == nil {
		return nil
	}
	for i := len(rt.Session.Messages) - 1; i >= 0; i-- {
		if rt.Session.Messages[i].Role == "assistant" {
			return &rt.Session.Messages[i]
		}
	}
	return nil
}
