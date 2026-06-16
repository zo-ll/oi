package chat

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/zo-ll/oi/internal/agent"
	"github.com/zo-ll/oi/internal/config"
	"github.com/zo-ll/oi/internal/provider"
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

func (s *chatState) header(root string) string {
	level := ""
	if s != nil && s.rt != nil {
		level = s.rt.ThinkingLevel
	}
	usage := provider.Usage{}
	if s != nil && s.rt != nil {
		usage = s.rt.LastUsage
	}
	supported := false
	if s != nil && s.rt != nil {
		supported = s.rt.ThinkingSupported
	}
	return formatHeader(s.sel.Model, root, s.contextWindow, usage, level, supported)
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

func runAbortableTUI(ui *terminalUI, rt *agent.Runtime, run func(context.Context) (string, error)) (string, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	active := false
	aborted := false
	setActive := func(next bool) {
		mu.Lock()
		defer mu.Unlock()
		if active == next {
			return
		}
		active = next
		_ = setTerminalNonblock(ui.in, next)
	}
	isActive := func() bool {
		mu.Lock()
		defer mu.Unlock()
		return active
	}
	markAbort := func() {
		mu.Lock()
		already := aborted
		aborted = true
		mu.Unlock()
		if !already {
			cancel()
			ui.notify("aborting...")
		}
	}
	wasAborted := func() bool {
		mu.Lock()
		defer mu.Unlock()
		return aborted
	}

	var prevStart, prevStop func()
	if rt != nil {
		prevStart = rt.OnModelStart
		prevStop = rt.OnModelStop
		rt.OnModelStart = func() {
			setActive(true)
			if prevStart != nil {
				prevStart()
			}
		}
		rt.OnModelStop = func() {
			setActive(false)
			if prevStop != nil {
				prevStop()
			}
		}
		defer func() {
			rt.OnModelStart = prevStart
			rt.OnModelStop = prevStop
		}()
	}
	defer setActive(false)

	ch := make(chan turnResult, 1)
	go func() {
		resp, err := run(ctx)
		ch <- turnResult{resp: resp, err: err}
	}()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case res := <-ch:
			if wasAborted() {
				return res.resp, fmt.Errorf("aborted")
			}
			return res.resp, res.err
		case <-ticker.C:
			if !isActive() {
				continue
			}
			b, err := readByte(ui.in)
			if err != nil {
				if isWouldBlock(err) {
					continue
				}
				continue
			}
			switch b {
			case 3:
				markAbort()
			case 27:
				kind, _, handled, err := ui.readEscapeSequence()
				if err != nil {
					continue
				}
				if !handled {
					markAbort()
					continue
				}
				switch kind {
				case "pageup":
					ui.pageUp()
				case "pagedown":
					ui.pageDown()
				case "esc":
					markAbort()
				}
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
	ui.startAssistantResponse()
	renderer := &taggedStreamRenderer{}
	resp, runErr := runAbortableTUI(ui, state.rt, func(ctx context.Context) (string, error) {
		return state.rt.RunOnceStream(ctx, line, func(delta string) {
			ui.writeStreamSegments(renderer.Push(delta))
		})
	})
	ui.writeStreamSegments(renderer.Flush())
	ui.finishAssistantResponse()
	if runErr != nil {
		ui.notify("error: " + runErr.Error())
		return
	}
	resp = cleanDisplayText(resp)
	state.lastAssistant = resp
	ui.blankLine()
	state.autosaveSession(ui, ui.notify)
}

func runNonStreamingTurnTUI(ui *terminalUI, state *chatState, line string) {
	resp, runErr := runAbortableTUI(ui, state.rt, func(ctx context.Context) (string, error) {
		return state.rt.RunOnce(ctx, line)
	})
	if runErr != nil {
		ui.notify("error: " + runErr.Error())
		return
	}
	resp = cleanDisplayText(resp)
	state.lastAssistant = resp
	ui.notify(resp)
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
}

func (r *taggedStreamRenderer) Push(delta string) []responseSegment {
	r.pending += delta
	var out []responseSegment
	for {
		if r.inThink {
			idx := strings.Index(r.pending, "</think>")
			if idx < 0 {
				keep := partialTagSuffix(r.pending, "</think>")
				appendResponseSegment(&out, cleanDisplayText(r.pending[:len(r.pending)-keep]), true)
				r.pending = r.pending[len(r.pending)-keep:]
				return out
			}
			appendResponseSegment(&out, cleanDisplayText(r.pending[:idx]), true)
			r.pending = r.pending[idx+len("</think>"):]
			r.inThink = false
			continue
		}
		idx := strings.Index(r.pending, "<think>")
		if idx < 0 {
			keep := partialTagSuffix(r.pending, "<think>")
			appendResponseSegment(&out, cleanDisplayText(r.pending[:len(r.pending)-keep]), false)
			r.pending = r.pending[len(r.pending)-keep:]
			return out
		}
		appendResponseSegment(&out, cleanDisplayText(r.pending[:idx]), false)
		r.pending = r.pending[idx+len("<think>"):]
		r.inThink = true
	}
}

func (r *taggedStreamRenderer) Flush() []responseSegment {
	var out []responseSegment
	if r.pending != "" {
		if r.inThink {
			appendResponseSegment(&out, cleanDisplayText(r.pending), true)
		} else {
			appendResponseSegment(&out, cleanDisplayText(r.pending), false)
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
