package chat

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zo-ll/oi/internal/agent"
	"github.com/zo-ll/oi/internal/tool"
	"github.com/zo-ll/oi/internal/workspace"
	"github.com/zo-ll/tide"
)

type tuiBlock struct {
	kind      string
	text      string
	wrapWidth int
	wrapped   []string
}

type inputVisualLine struct {
	text      string
	start     int
	end       int
	cursorCol int
}

type approvalRequest struct {
	action string
	target string
	resp   chan bool
}

type tuiApp struct {
	term         *tide.Terminal
	state        *chatState
	deps         Dependencies
	reader       *bufio.Reader
	blocks       []tuiBlock
	status       string
	input        []rune
	cursor       int
	scroll       int
	lastRender   time.Time
	hintIdx      int
	inputCh      chan byte
	errCh        chan error
	events       chan func()
	approvals    chan approvalRequest
	approval     *approvalRequest
	running      bool
	cancel       context.CancelFunc
	steerQueue   []string
	history      []string
	historyIndex int
	historyDraft []rune
}

func runEditMode(args []string, in *os.File, out *os.File, deps Dependencies) (err error) {
	root, err := workspace.DetectRoot("")
	if err != nil {
		return err
	}
	term, err := tide.Open(in, out)
	if err != nil {
		return err
	}
	if err := term.EnterRaw(); err != nil {
		return err
	}
	if err := term.EnterAltScreen(); err != nil {
		_ = term.Close()
		return err
	}
	if err := term.EnableMouse(); err != nil {
		_ = term.Close()
		return err
	}
	defer term.Close()

	app := &tuiApp{
		term:      term,
		deps:      deps,
		reader:    bufio.NewReader(in),
		inputCh:   make(chan byte, 128),
		errCh:     make(chan error, 1),
		events:    make(chan func(), 1024),
		approvals: make(chan approvalRequest, 8),
	}
	defer term.WatchResize(func() { app.post(func() { app.render() }) })()
	app.startInput(tide.NewInput(term.In))
	state, startupNotice, err := loadChatRuntime(args, root, app.reader, app)
	if err != nil {
		return err
	}
	app.state = state
	app.historyIndex = -1
	configureTUIRuntime(state.rt, state.tools, app)
	if startupNotice != "" {
		app.addBlock("system", startupNotice)
	}
	app.render()
	return app.loop()
}

func (a *tuiApp) startInput(in *tide.Input) {
	go func() {
		for {
			b, err := in.Next()
			if err != nil {
				a.errCh <- err
				return
			}
			a.inputCh <- b
		}
	}()
}

func (a *tuiApp) loop() error {
	for {
		select {
		case fn := <-a.events:
			if fn != nil {
				fn()
			}
			continue
		case req := <-a.approvals:
			a.approval = &req
			a.renderApprovalOverlay(req.action, req.target)
			continue
		case <-a.errCh:
			if a.cancel != nil {
				a.cancel()
			}
			return exitChat(a.term.Out, a.state.rt, a.state.sel, a.state.autosave)
		case b := <-a.inputCh:
			if a.handleApprovalInput(b) {
				continue
			}
			switch b {
			case 3, 4:
				if a.cancel != nil {
					a.cancel()
				}
				return exitChat(a.term.Out, a.state.rt, a.state.sel, a.state.autosave)
			case '\r', '\n':
				line := strings.TrimSpace(string(a.input))
				a.input = nil
				a.cursor = 0
				if line == "" {
					a.render()
					continue
				}
				if line == "/exit" || line == "/quit" {
					if a.cancel != nil {
						a.cancel()
					}
					return exitChat(a.term.Out, a.state.rt, a.state.sel, a.state.autosave)
				}
				a.addHistory(line)
				if a.running {
					a.steerQueue = append(a.steerQueue, line)
					a.addBlock("user", line)
					a.status = fmt.Sprintf("queued steer (%d)", len(a.steerQueue))
					a.render()
					continue
				}
				if strings.HasPrefix(line, "/") {
					exit, cmdErr := a.handleCommand(line)
					if cmdErr != nil {
						a.addBlock("error", cmdErr.Error())
						a.render()
					}
					if exit {
						return nil
					}
					continue
				}
				a.addBlock("user", line)
				a.runTurn(line)
			case 8, 127:
				if a.cursor > 0 {
					a.input = append(a.input[:a.cursor-1], a.input[a.cursor:]...)
					a.cursor--
				}
				a.hintIdx = 0
			case 9:
				a.tabComplete()
			case 21:
				a.scrollPage(1)
			case 6:
				a.scrollPage(-1)
			case 27:
				a.handleEscape()
			default:
				if b >= 32 {
					r, size := rune(b), 1
					if b >= utf8.RuneSelf {
						var more [utf8.UTFMax]byte
						more[0] = b
						for size < utf8.UTFMax && !utf8.FullRune(more[:size]) {
							select {
							case next := <-a.inputCh:
								more[size] = next
								size++
							case <-a.errCh:
								return exitChat(a.term.Out, a.state.rt, a.state.sel, a.state.autosave)
							}
						}
						r, _ = utf8.DecodeRune(more[:size])
					}
					a.input = append(a.input[:a.cursor], append([]rune{r}, a.input[a.cursor:]...)...)
					a.cursor++
					a.hintIdx = 0
				}
			}
			a.render()
		}
	}
}

func (a *tuiApp) handleCommand(line string) (bool, error) {
	exit, newRT, newSel, newStreaming, newAutosave, newTools, err := handleChatCommand(a.deps, a.state.cfg, a.state.sel, a.state.rt, a.reader, a, line, a.state.streaming, a.state.autosave, a.state.tools)
	if err != nil {
		return false, err
	}
	a.state.applyCommandResult(newRT, newSel, newStreaming, newAutosave, newTools, a)
	configureTUIRuntime(a.state.rt, a.state.tools, a)
	a.render()
	return exit, nil
}

func (a *tuiApp) post(fn func()) {
	if fn == nil {
		return
	}
	if a.events == nil {
		fn()
		return
	}
	a.events <- fn
}

func (a *tuiApp) runTurn(line string) {
	if a.running {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	a.running = true
	a.status = "thinking"
	a.render()
	streaming := a.state.streaming
	go func() {
		var err error
		if streaming {
			err = a.runStreamingTurn(ctx, line)
		} else {
			err = a.runNonStreamingTurn(ctx, line)
		}
		a.post(func() { a.finishTurn(line, err) })
	}()
}

func (a *tuiApp) finishTurn(line string, runErr error) {
	_ = line
	if a.cancel != nil {
		a.cancel()
	}
	a.cancel = nil
	a.running = false
	a.status = ""
	if runErr != nil && runErr != context.Canceled {
		a.addBlock("error", runErr.Error())
	}
	if len(a.steerQueue) > 0 {
		next := a.steerQueue[0]
		a.steerQueue = a.steerQueue[1:]
		a.runTurn(next)
		return
	}
	a.render()
}

func (a *tuiApp) runStreamingTurn(ctx context.Context, line string) error {
	currentIdx := -1
	var stepDraft []int
	renderer := &taggedStreamRenderer{}
	_, runErr := a.state.rt.RunOnceStreamObserved(ctx, line, agent.StreamObserver{Delta: func(delta string, reasoning bool) {
		a.post(func() {
			if reasoning {
				a.appendStreamBlock("thinking", cleanDisplayText(delta), &currentIdx, &stepDraft)
				a.renderSoon()
				return
			}
			for _, seg := range renderer.Push(delta, false) {
				if seg.reasoning {
					a.appendStreamBlock("thinking", seg.text, &currentIdx, &stepDraft)
					continue
				}
				a.appendStreamBlock("assistant", seg.text, &currentIdx, &stepDraft)
			}
			a.renderSoon()
		})
	}, StepDone: func(toolCalls bool) {
		_ = toolCalls
		a.post(func() {
			currentIdx = -1
			stepDraft = nil
			renderer = &taggedStreamRenderer{}
			a.render()
		})
	}})
	a.post(func() {
		for _, seg := range renderer.Flush() {
			if seg.reasoning {
				a.appendStreamBlock("thinking", seg.text, &currentIdx, &stepDraft)
			} else {
				a.appendStreamBlock("assistant", seg.text, &currentIdx, &stepDraft)
			}
		}
	})
	if runErr != nil {
		return runErr
	}
	a.state.autosaveSession(a, func(msg string) { a.post(func() { a.addBlock("error", msg) }) })
	return nil
}

func (a *tuiApp) appendStreamBlock(kind, text string, currentIdx *int, stepDraft *[]int) {
	if text == "" {
		return
	}
	if *currentIdx >= 0 && *currentIdx < len(a.blocks) && a.blocks[*currentIdx].kind == kind {
		a.blocks[*currentIdx].text += text
		a.blocks[*currentIdx].wrapWidth = 0
		a.blocks[*currentIdx].wrapped = nil
		return
	}
	idx := a.addBlock(kind, text)
	*currentIdx = idx
	*stepDraft = append(*stepDraft, idx)
}

func (a *tuiApp) runNonStreamingTurn(ctx context.Context, line string) error {
	resp, runErr := a.state.rt.RunOnce(ctx, line)
	if runErr != nil {
		return runErr
	}
	a.post(func() {
		if last := lastAssistantMessage(a.state.rt); last != nil && strings.TrimSpace(last.Reasoning) != "" {
			a.addBlock("thinking", cleanDisplayText(last.Reasoning))
		}
		a.addBlock("assistant", cleanDisplayText(resp))
	})
	a.state.autosaveSession(a, func(msg string) { a.post(func() { a.addBlock("error", msg) }) })
	return nil
}

func (a *tuiApp) Write(p []byte) (int, error) {
	text := strings.TrimSpace(string(p))
	if text != "" {
		a.post(func() {
			if strings.HasPrefix(text, "|") || strings.HasPrefix(text, "!") || strings.HasPrefix(text, "|>") || strings.HasPrefix(text, "! ") {
				a.status = text
				a.render()
				return
			}
			a.addBlock("system", text)
			a.render()
		})
	}
	return len(p), nil
}

func (a *tuiApp) Styled(kind, text string) string {
	switch kind {
	case "dim":
		return tide.Dim(text)
	case "warn":
		return tide.Warn(text)
	case "command":
		return tide.Command(text)
	default:
		return text
	}
}

func (a *tuiApp) ShowStatus(text string) {
	a.post(func() {
		a.status = strings.TrimSpace(text)
		a.render()
	})
}

func (a *tuiApp) ClearStatus() {
	a.post(func() {
		a.status = ""
		a.render()
	})
}

func (a *tuiApp) ClearScreen() {
	a.blocks = nil
	a.scroll = 0
	a.status = ""
	a.render()
}

func (a *tuiApp) Approve(action, target string) (bool, error) {
	req := approvalRequest{action: action, target: target, resp: make(chan bool, 1)}
	if a.approvals == nil {
		return false, fmt.Errorf("approval UI unavailable")
	}
	a.approvals <- req
	return <-req.resp, nil
}

func (a *tuiApp) addBlock(kind, text string) int {
	a.blocks = append(a.blocks, tuiBlock{kind: kind, text: strings.TrimRight(text, "\n")})
	a.scroll = 0
	return len(a.blocks) - 1
}

func (a *tuiApp) removeBlock(idx int) {
	if idx < 0 || idx >= len(a.blocks) {
		return
	}
	a.blocks = append(a.blocks[:idx], a.blocks[idx+1:]...)
}

func configureTUIRuntime(rt *agent.Runtime, tools toolVerbosity, app *tuiApp) {
	if rt == nil || app == nil {
		return
	}
	rt.OnRetrieve = nil
	rt.OnModelStart = func() { app.ShowStatus("thinking") }
	rt.OnModelStop = app.ClearStatus
	rt.OnToolStart = nil
	rt.OnToolResult = nil
	rt.OnCompact = func() { app.post(func() { app.status = "compacting"; app.render() }) }
	rt.DrainSteering = app.drainSteering
	if tools == toolVerbosityOff {
		return
	}
	rt.OnToolStart = func(call tool.Call) {
		app.post(func() {
			app.addBlock("tool", strings.TrimSpace(formatToolStartLine(call)))
			app.status = toolActivityLabel(call)
			app.render()
		})
	}
	rt.OnToolResult = func(call tool.Call, result tool.Result) {
		app.post(func() {
			if result.OK {
				app.status = ""
				app.render()
				return
			}
			app.addBlock("tool", strings.TrimSpace(formatToolResultLine(call, result)))
			app.status = formatToolResultLine(call, result)
			app.render()
		})
	}
}

func (a *tuiApp) drainSteering() []string {
	ch := make(chan []string, 1)
	a.post(func() {
		out := append([]string(nil), a.steerQueue...)
		a.steerQueue = nil
		if len(out) > 0 {
			a.status = "thinking"
			a.render()
		}
		ch <- out
	})
	return <-ch
}
