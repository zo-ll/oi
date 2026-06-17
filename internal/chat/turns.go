package chat

import (
	"context"
	"fmt"
	"io"
	"strings"

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
	model := lookupModelInfo(s.rt.Provider, s.sel.Model)
	level := clampThinkingLevel(model, s.rt.ThinkingLevel)
	s.contextWindow = model.ContextWindow
	s.rt.ContextWindow = model.ContextWindow
	s.rt.ThinkingLevel = level
	s.rt.ThinkingValue = thinkingValue(model, level)
	s.rt.ThinkingFormat = model.ThinkingFormat
	s.rt.ThinkingSupported = model.SupportsThinking
	s.rt.SupportedThinkingLevels = supportedThinkingLevels(model)
	s.rt.ThinkingLevelValues = model.ThinkingLevelValues
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

func runStreamingTurnLine(out io.Writer, state *chatState, line string) {
	// Streaming output is itself the progress indicator. A concurrent model
	// spinner rewrites terminal lines while deltas are being printed, corrupting
	// the response. Keep tool status enabled, but suppress model start/stop status
	// for streaming turns.
	onModelStart, onModelStop := state.rt.OnModelStart, state.rt.OnModelStop
	state.rt.OnModelStart, state.rt.OnModelStop = nil, nil
	defer func() {
		state.rt.OnModelStart, state.rt.OnModelStop = onModelStart, onModelStop
	}()

	renderer := &taggedStreamRenderer{}
	blocks := &responseBlockWriter{out: out}
	_, runErr := state.rt.RunOnceStream(context.Background(), line, func(delta string, reasoning bool) {
		if reasoning {
			return
		}
		for _, seg := range renderer.Push(delta, reasoning) {
			blocks.Write(seg)
		}
	})
	for _, seg := range renderer.Flush() {
		blocks.Write(seg)
	}
	blocks.Finish()
	if runErr != nil {
		fmt.Fprintf(out, "error: %v\n", runErr)
		return
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
	blocks := &responseBlockWriter{out: out}
	blocks.Write(responseSegment{text: cleanDisplayText(resp)})
	blocks.Finish()
	state.autosaveSession(out, func(msg string) { fmt.Fprintln(out, msg) })
}

type responseSegment struct {
	text      string
	reasoning bool
}

type taggedStreamRenderer struct {
	pending string
	inThink bool
}

func (r *taggedStreamRenderer) Push(delta string, reasoning bool) []responseSegment {
	if reasoning {
		var out []responseSegment
		r.appendResponseSegment(&out, cleanDisplayText(delta), true)
		return out
	}
	r.pending += delta
	var out []responseSegment
	for {
		if r.inThink {
			idx := strings.Index(r.pending, "</think>")
			if idx < 0 {
				keep := partialTagSuffix(r.pending, "</think>")
				r.appendResponseSegment(&out, cleanDisplayText(r.pending[:len(r.pending)-keep]), true)
				r.pending = r.pending[len(r.pending)-keep:]
				return out
			}
			r.appendResponseSegment(&out, cleanDisplayText(r.pending[:idx]), true)
			r.pending = r.pending[idx+len("</think>"):]
			r.inThink = false
			continue
		}
		idx := strings.Index(r.pending, "<think>")
		if idx < 0 {
			keep := partialTagSuffix(r.pending, "<think>")
			r.appendResponseSegment(&out, cleanDisplayText(r.pending[:len(r.pending)-keep]), false)
			r.pending = r.pending[len(r.pending)-keep:]
			return out
		}
		r.appendResponseSegment(&out, cleanDisplayText(r.pending[:idx]), false)
		r.pending = r.pending[idx+len("<think>"):]
		r.inThink = true
	}
}

func (r *taggedStreamRenderer) Flush() []responseSegment {
	var out []responseSegment
	if r.pending != "" {
		if r.inThink {
			r.appendResponseSegment(&out, cleanDisplayText(r.pending), true)
		} else {
			r.appendResponseSegment(&out, cleanDisplayText(r.pending), false)
		}
		r.pending = ""
	}
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

func (r *taggedStreamRenderer) appendResponseSegment(out *[]responseSegment, text string, reasoning bool) {
	if text == "" {
		return
	}
	if n := len(*out); n > 0 && (*out)[n-1].reasoning == reasoning {
		(*out)[n-1].text += text
	} else {
		*out = append(*out, responseSegment{text: text, reasoning: reasoning})
	}
}

func trailingNewlines(text string) int {
	n := 0
	for i := len(text) - 1; i >= 0 && text[i] == '\n'; i-- {
		n++
	}
	return n
}

type responseBlockWriter struct {
	out          io.Writer
	current      string
	trailingNL   int
	wroteContent bool
}

func (w *responseBlockWriter) Write(seg responseSegment) {
	kind := "response"
	if seg.reasoning {
		kind = "thinking"
	}
	text := seg.text
	if w.current != kind {
		text = strings.TrimLeft(text, "\n")
	}
	if text == "" {
		return
	}
	w.beginBlock(kind)
	w.trailingNL = trailingNewlines(text)
	if seg.reasoning {
		text = styleText(w.out, "dim", text)
	}
	fmt.Fprint(w.out, text)
	w.wroteContent = true
}

func (w *responseBlockWriter) beginBlock(kind string) {
	if w.current == kind {
		return
	}
	if w.current != "" {
		w.ensureBlankLine()
	}
	w.current = kind
	w.trailingNL = 0
}

func (w *responseBlockWriter) ensureBlankLine() {
	for w.trailingNL < 2 {
		fmt.Fprintln(w.out)
		w.trailingNL++
	}
}

func (w *responseBlockWriter) Finish() {
	if !w.wroteContent {
		return
	}
	w.ensureBlankLine()
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
