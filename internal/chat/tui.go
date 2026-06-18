package chat

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
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
	go app.readInputLoop()
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

func (a *tuiApp) readInputLoop() {
	for {
		b, err := readTUIByte(a.term.In)
		if err != nil {
			a.errCh <- err
			return
		}
		a.inputCh <- b
	}
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

func (a *tuiApp) nextByte() (byte, error) {
	for {
		select {
		case fn := <-a.events:
			if fn != nil {
				fn()
			}
		case req := <-a.approvals:
			a.approval = &req
			a.renderApprovalOverlay(req.action, req.target)
		case <-a.errCh:
			return 0, io.EOF
		case b := <-a.inputCh:
			if a.handleApprovalInput(b) {
				continue
			}
			return b, nil
		}
	}
}

func (a *tuiApp) addHistory(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	if len(a.history) > 0 && a.history[0] == line {
		a.historyIndex = -1
		a.historyDraft = nil
		return
	}
	a.history = append([]string{line}, a.history...)
	if len(a.history) > 200 {
		a.history = a.history[:200]
	}
	a.historyIndex = -1
	a.historyDraft = nil
}

func (a *tuiApp) historyPrev() {
	if len(a.history) == 0 {
		return
	}
	if a.historyIndex == -1 {
		a.historyDraft = append([]rune(nil), a.input...)
		a.historyIndex = 0
	} else if a.historyIndex+1 < len(a.history) {
		a.historyIndex++
	}
	a.input = []rune(a.history[a.historyIndex])
	a.cursor = len(a.input)
	a.hintIdx = 0
}

func (a *tuiApp) historyNext() {
	if a.historyIndex == -1 {
		return
	}
	if a.historyIndex > 0 {
		a.historyIndex--
		a.input = []rune(a.history[a.historyIndex])
	} else {
		a.historyIndex = -1
		a.input = append([]rune(nil), a.historyDraft...)
		a.historyDraft = nil
	}
	a.cursor = len(a.input)
	a.hintIdx = 0
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

func (a *tuiApp) handleApprovalInput(b byte) bool {
	if a.approval == nil {
		return false
	}
	switch b {
	case 'y', 'Y':
		a.approval.resp <- true
		a.approval = nil
		a.render()
	case 'n', 'N', 3, 27:
		a.approval.resp <- false
		a.approval = nil
		a.render()
	default:
		a.renderApprovalOverlay(a.approval.action, a.approval.target)
	}
	return true
}

func (a *tuiApp) overlayPicker(title string, items []string) (string, bool) {
	if len(items) == 0 {
		return "", false
	}
	idx := 0
	query := ""
	filtered := append([]string(nil), items...)
	refilter := func() {
		filtered = filtered[:0]
		q := strings.ToLower(strings.TrimSpace(query))
		for _, item := range items {
			if q == "" || strings.Contains(strings.ToLower(item), q) {
				filtered = append(filtered, item)
			}
		}
		if idx >= len(filtered) {
			idx = len(filtered) - 1
		}
		if idx < 0 {
			idx = 0
		}
	}
	for {
		a.renderPickerOverlay(title, query, filtered, idx)
		b, err := a.nextByte()
		if err != nil {
			a.render()
			return "", false
		}
		switch b {
		case 3:
			a.render()
			return "", false
		case 27:
			kind := a.readOverlayEscape()
			switch kind {
			case "up":
				if idx > 0 {
					idx--
				}
			case "down":
				if idx+1 < len(filtered) {
					idx++
				}
			case "page-up":
				idx -= 10
				if idx < 0 {
					idx = 0
				}
			case "page-down":
				idx += 10
				if idx >= len(filtered) {
					idx = len(filtered) - 1
				}
			default:
				a.render()
				return "", false
			}
		case '\r', '\n':
			a.render()
			if len(filtered) == 0 {
				return "", false
			}
			return filtered[idx], true
		case 8, 127:
			if query != "" {
				query = query[:len(query)-1]
				refilter()
			}
		default:
			if b >= 32 {
				query += string(rune(b))
				refilter()
			}
		}
	}
}

func (a *tuiApp) overlayInput(title, prompt, initial string) (string, bool) {
	buf := []rune(initial)
	cursor := len(buf)
	for {
		a.renderInputOverlay(title, prompt, string(buf), cursor)
		b, err := a.nextByte()
		if err != nil {
			a.render()
			return "", false
		}
		switch b {
		case 3:
			a.render()
			return "", false
		case 27:
			kind := a.readOverlayEscape()
			switch kind {
			case "left":
				if cursor > 0 {
					cursor--
				}
			case "right":
				if cursor < len(buf) {
					cursor++
				}
			case "home":
				cursor = 0
			case "end":
				cursor = len(buf)
			default:
				a.render()
				return "", false
			}
		case '\r', '\n':
			a.render()
			return string(buf), true
		case 8, 127:
			if cursor > 0 {
				buf = append(buf[:cursor-1], buf[cursor:]...)
				cursor--
			}
		default:
			if b >= 32 {
				r := rune(b)
				buf = append(buf[:cursor], append([]rune{r}, buf[cursor:]...)...)
				cursor++
			}
		}
	}
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

func (a *tuiApp) handleEscape() {
	first, err := a.nextByte()
	if err != nil {
		return
	}
	if first == 'b' {
		a.scrollPage(1)
		return
	}
	if first == 'f' {
		a.scrollPage(-1)
		return
	}
	if first != '[' && first != 'O' {
		return
	}
	seq := []byte{first}
	for len(seq) < 64 {
		b, err := a.nextByte()
		if err != nil {
			return
		}
		seq = append(seq, b)
		if (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || b == '~' {
			break
		}
	}
	if strings.HasPrefix(string(seq), "[<") {
		a.handleSGRMouse(string(seq))
		return
	}
	switch string(seq) {
	case "[A", "OA":
		if hints := a.commandHints(); len(hints) > 0 {
			if a.hintIdx > 0 {
				a.hintIdx--
			}
		} else {
			a.historyPrev()
		}
	case "[B", "OB":
		if hints := a.commandHints(); len(hints) > 0 {
			if a.hintIdx+1 < len(hints) {
				a.hintIdx++
			}
		} else {
			a.historyNext()
		}
	case "[5~":
		a.scrollPage(1)
	case "[6~":
		a.scrollPage(-1)
	case "[C", "OC":
		if a.cursor < len(a.input) {
			a.cursor++
		}
	case "[D", "OD":
		if a.cursor > 0 {
			a.cursor--
		}
	case "[H", "OH", "[1~":
		a.cursor = 0
	case "[F", "OF", "[4~":
		a.cursor = len(a.input)
	}
}

func (a *tuiApp) handleSGRMouse(seq string) {
	if !strings.HasSuffix(seq, "M") && !strings.HasSuffix(seq, "m") {
		return
	}
	body := strings.TrimPrefix(seq, "[<")
	body = strings.TrimSuffix(strings.TrimSuffix(body, "M"), "m")
	parts := strings.Split(body, ";")
	if len(parts) != 3 {
		return
	}
	button, err := strconv.Atoi(parts[0])
	if err != nil {
		return
	}
	switch button {
	case 64:
		a.scrollLines(3)
	case 65:
		a.scrollLines(-3)
	}
}

func (a *tuiApp) scrollLines(delta int) {
	a.scroll += delta
	if a.scroll < 0 {
		a.scroll = 0
	}
}

func (a *tuiApp) commandHints() []string {
	text := string(a.input)
	if len(text) == 0 || text[0] != '/' {
		return nil
	}
	query := strings.TrimPrefix(text, "/")
	return filterByPrefix(chatCommands(), query)
}

func (a *tuiApp) tabComplete() {
	hints := a.commandHints()
	if len(hints) == 0 {
		return
	}
	if a.hintIdx >= len(hints) {
		a.hintIdx = 0
	}
	a.input = []rune(hints[a.hintIdx])
	a.cursor = len(a.input)
}

func (a *tuiApp) scrollPage(delta int) {
	size := a.term.Size()
	step := size.Rows - 6
	if step < 1 {
		step = 1
	}
	a.scrollLines(delta * step)
}

func (a *tuiApp) renderSoon() {
	if time.Since(a.lastRender) < 33*time.Millisecond {
		return
	}
	a.render()
}

func (a *tuiApp) render() {
	a.lastRender = time.Now()
	size := a.term.Size()
	if size.Rows < 8 {
		size.Rows = 8
	}
	if size.Cols < 40 {
		size.Cols = 40
	}
	var frame strings.Builder
	tide.HideCursor(&frame)
	frame.WriteString("\x1b[H\x1b[J")
	header := fmt.Sprintf("oi  %s/%s  %s", valueOr(a.state.sel.Provider, "none"), valueOr(a.state.sel.Model, "none"), valueOr(a.state.rt.Policy.Root, "."))
	writeClipped(&frame, 1, 1, size.Cols, tide.Command(header))
	writeClipped(&frame, 2, 1, size.Cols, strings.Repeat("-", size.Cols))

	hints := a.commandHints()
	hintCount := 0
	if len(hints) > 0 {
		if a.hintIdx >= len(hints) {
			a.hintIdx = 0
		}
		hintCount = 5
		if hintCount > len(hints) {
			hintCount = len(hints)
		}
		maxHint := size.Rows - 6
		if maxHint < 1 {
			maxHint = 1
		}
		if hintCount > maxHint {
			hintCount = maxHint
		}
	}

	inputLines := a.renderInputLines(size.Cols)
	maxInputRows := size.Rows / 3
	if maxInputRows < 1 {
		maxInputRows = 1
	}
	if maxInputRows > 5 {
		maxInputRows = 5
	}
	inputStart := 0
	cursorLine := 0
	for i, line := range inputLines {
		if line.cursorCol >= 0 {
			cursorLine = i
			break
		}
	}
	if len(inputLines) > maxInputRows {
		if cursorLine >= maxInputRows {
			inputStart = cursorLine - maxInputRows + 1
		}
		if inputStart+maxInputRows > len(inputLines) {
			inputStart = len(inputLines) - maxInputRows
		}
		inputLines = inputLines[inputStart : inputStart+maxInputRows]
		cursorLine -= inputStart
	}

	bottomRows := 1 + len(inputLines) // separator + input lines
	if status := a.status; status != "" {
		bottomRows++
	}
	if hintCount > 0 {
		bottomRows += hintCount
	}
	viewHeight := size.Rows - 2 - bottomRows // 2 = header + separator
	if viewHeight < 1 {
		viewHeight = 1
	}

	lines := a.renderTranscript(size.Cols)
	maxScroll := len(lines) - viewHeight
	if maxScroll < 0 {
		maxScroll = 0
	}
	if a.scroll > maxScroll {
		a.scroll = maxScroll
	}
	start := len(lines) - viewHeight - a.scroll
	if start < 0 {
		start = 0
	}
	end := start + viewHeight
	if end > len(lines) {
		end = len(lines)
	}
	for ri, line := range lines[start:end] {
		writeClipped(&frame, 3+ri, 1, size.Cols, line)
	}

	nextRow := 3 + viewHeight
	status := a.status
	if status != "" {
		writeClipped(&frame, nextRow, 1, size.Cols, tide.Dim(status))
		nextRow++
	}

	if hintCount > 0 {
		hintStart := 0
		if a.hintIdx >= hintCount {
			hintStart = a.hintIdx - hintCount + 1
		}
		if hintStart+hintCount > len(hints) {
			hintStart = len(hints) - hintCount
			if hintStart < 0 {
				hintStart = 0
			}
		}
		for r := 0; r < hintCount && hintStart+r < len(hints); r++ {
			i := hintStart + r
			marker := "  "
			if i == a.hintIdx {
				marker = "> "
			}
			writeClipped(&frame, nextRow+r, 1, size.Cols, tide.Dim(marker+hints[i]))
		}
		nextRow += hintCount
	}

	writeClipped(&frame, nextRow, 1, size.Cols, strings.Repeat("-", size.Cols))
	for i, line := range inputLines {
		writeClipped(&frame, nextRow+1+i, 1, size.Cols, line.text)
	}
	cursorRow := nextRow + 1 + cursorLine
	cursorCol := 1
	if cursorLine >= 0 && cursorLine < len(inputLines) {
		cursorCol += inputLines[cursorLine].cursorCol
	}
	if cursorCol > size.Cols {
		cursorCol = size.Cols
	}
	tide.MoveTo(&frame, cursorRow, cursorCol)
	tide.ShowCursor(&frame)
	_, _ = io.WriteString(a.term.Out, frame.String())
}

func (a *tuiApp) renderInputLines(width int) []inputVisualLine {
	if width < 1 {
		width = 1
	}
	if len(a.input) == 0 {
		return []inputVisualLine{{text: "", start: 0, end: 0, cursorCol: 0}}
	}
	var lines []inputVisualLine
	start := 0
	used := 0
	for i, r := range a.input {
		rw := tide.DisplayWidth(string(r))
		if rw <= 0 {
			rw = 1
		}
		if used > 0 && used+rw > width {
			lines = append(lines, inputVisualLine{text: string(a.input[start:i]), start: start, end: i, cursorCol: -1})
			start = i
			used = 0
		}
		used += rw
	}
	lines = append(lines, inputVisualLine{text: string(a.input[start:]), start: start, end: len(a.input), cursorCol: -1})
	if a.cursor == len(a.input) && tide.DisplayWidth(lines[len(lines)-1].text) >= width {
		lines = append(lines, inputVisualLine{text: "", start: len(a.input), end: len(a.input), cursorCol: -1})
	}
	cursorLine := len(lines) - 1
	for i := range lines {
		line := &lines[i]
		if a.cursor >= line.start && a.cursor <= line.end {
			cursorLine = i
			if a.cursor == line.end && i+1 < len(lines) {
				cursorLine = i + 1
			}
			break
		}
	}
	if cursorLine < 0 {
		cursorLine = 0
	}
	if cursorLine >= len(lines) {
		cursorLine = len(lines) - 1
	}
	line := &lines[cursorLine]
	cursor := a.cursor
	if cursor < line.start {
		cursor = line.start
	}
	if cursor > line.end {
		cursor = line.end
	}
	line.cursorCol = tide.DisplayWidth(string(a.input[line.start:cursor]))
	return lines
}

func (a *tuiApp) renderApprovalOverlay(action, target string) {
	a.render()
	size := a.term.Size()
	width := size.Cols - 8
	if width > 92 {
		width = 92
	}
	if width < 40 {
		width = 40
	}
	bodyWidth := width - 4
	targetLines := wrapPlain(target, bodyWidth)
	if len(targetLines) > 8 {
		targetLines = append(targetLines[:8], "...")
	}
	height := len(targetLines) + 6
	if height < 7 {
		height = 7
	}
	top := (size.Rows - height) / 2
	left := (size.Cols - width) / 2
	if top < 1 {
		top = 1
	}
	if left < 1 {
		left = 1
	}
	var frame strings.Builder
	border := "+" + strings.Repeat("-", width-2) + "+"
	writeClipped(&frame, top, left, width, border)
	writeClipped(&frame, top+1, left, width, "| "+tide.Warn("approve "+action)+strings.Repeat(" ", width))
	writeClipped(&frame, top+2, left, width, "+"+strings.Repeat("-", width-2)+"+")
	row := top + 3
	for _, line := range targetLines {
		writeClipped(&frame, row, left, width, "| "+line)
		row++
	}
	for row < top+height-2 {
		writeClipped(&frame, row, left, width, "|")
		row++
	}
	writeClipped(&frame, top+height-2, left, width, "| "+tide.Command("y")+" approve   "+tide.Command("n")+" deny   Esc cancel")
	writeClipped(&frame, top+height-1, left, width, border)
	tide.MoveTo(&frame, top+height-2, left+2)
	tide.ShowCursor(&frame)
	_, _ = io.WriteString(a.term.Out, frame.String())
}

func (a *tuiApp) renderPickerOverlay(title, query string, items []string, idx int) {
	a.render()
	size := a.term.Size()
	width := size.Cols - 8
	if width > 92 {
		width = 92
	}
	if width < 32 {
		width = 32
	}
	height := len(items) + 5
	maxHeight := size.Rows - 4
	if height > maxHeight {
		height = maxHeight
	}
	if height < 7 {
		height = 7
	}
	top := (size.Rows - height) / 2
	left := (size.Cols - width) / 2
	if top < 1 {
		top = 1
	}
	if left < 1 {
		left = 1
	}
	var frame strings.Builder
	border := "+" + strings.Repeat("-", width-2) + "+"
	writeClipped(&frame, top, left, width, border)
	writeClipped(&frame, top+1, left, width, "| "+title)
	writeClipped(&frame, top+2, left, width, "| search: "+query)
	writeClipped(&frame, top+3, left, width, "+"+strings.Repeat("-", width-2)+"+")
	visible := height - 5
	start := 0
	if idx >= visible {
		start = idx - visible + 1
	}
	for row := 0; row < visible; row++ {
		itemIdx := start + row
		line := "| "
		if itemIdx < len(items) {
			marker := "  "
			if itemIdx == idx {
				marker = "> "
			}
			line += marker + items[itemIdx]
		}
		writeClipped(&frame, top+4+row, left, width, line)
	}
	writeClipped(&frame, top+height-1, left, width, border)
	_, _ = io.WriteString(a.term.Out, frame.String())
}

func (a *tuiApp) renderInputOverlay(title, prompt, text string, cursor int) {
	a.render()
	size := a.term.Size()
	width := size.Cols - 8
	if width > 80 {
		width = 80
	}
	if width < 32 {
		width = 32
	}
	height := 5
	top := (size.Rows - height) / 2
	left := (size.Cols - width) / 2
	if top < 1 {
		top = 1
	}
	if left < 1 {
		left = 1
	}
	var frame strings.Builder
	border := "+" + strings.Repeat("-", width-2) + "+"
	writeClipped(&frame, top, left, width, border)
	writeClipped(&frame, top+1, left, width, "| "+title)
	writeClipped(&frame, top+2, left, width, "| "+prompt+text)
	writeClipped(&frame, top+3, left, width, "| Enter save, Esc cancel")
	writeClipped(&frame, top+4, left, width, border)
	col := left + 2 + tide.DisplayWidth(prompt+string([]rune(text)[:cursor]))
	tide.MoveTo(&frame, top+2, col)
	_, _ = io.WriteString(a.term.Out, frame.String())
}

func (a *tuiApp) renderTranscript(width int) []string {
	var lines []string
	bodyWidth := width - 4
	if bodyWidth < 20 {
		bodyWidth = 20
	}
	for i := range a.blocks {
		block := &a.blocks[i]
		style := func(s string) string { return s }
		switch block.kind {
		case "user":
			style = tide.Command
		case "thinking":
			style = tide.Dim
		case "tool":
			style = tide.Dim
		case "assistant":
		case "error":
			style = func(s string) string { return tide.Warn("error: " + s) }
		default:
			style = tide.Dim
		}
		if block.wrapWidth != bodyWidth || block.wrapped == nil {
			block.wrapped = wrapPlain(block.text, bodyWidth)
			block.wrapWidth = bodyWidth
			if len(block.wrapped) == 0 {
				block.wrapped = []string{""}
			}
		}
		for _, line := range block.wrapped {
			lines = append(lines, style(line))
		}
		lines = append(lines, "")
	}
	return lines
}

func wrapPlain(text string, width int) []string {
	var out []string
	for _, para := range strings.Split(text, "\n") {
		wrapped := tide.Wrap(para, width)
		if len(wrapped) == 0 {
			out = append(out, "")
			continue
		}
		out = append(out, wrapped...)
	}
	return out
}

func writeClipped(w io.Writer, row, col, width int, text string) {
	tide.MoveTo(w, row, col)
	tide.ClearLine(w)
	runes := []rune(text)
	for len(runes) > 0 && tide.DisplayWidth(string(runes)) > width {
		runes = runes[:len(runes)-1]
	}
	_, _ = io.WriteString(w, string(runes))
}

func (a *tuiApp) readOverlayEscape() string {
	first, err := a.nextByte()
	if err != nil {
		return "cancel"
	}
	if first != '[' && first != 'O' {
		return "cancel"
	}
	seq := []byte{first}
	for len(seq) < 16 {
		b, err := a.nextByte()
		if err != nil {
			return "cancel"
		}
		seq = append(seq, b)
		if (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || b == '~' {
			break
		}
	}
	switch string(seq) {
	case "[A", "OA":
		return "up"
	case "[B", "OB":
		return "down"
	case "[C", "OC":
		return "right"
	case "[D", "OD":
		return "left"
	case "[H", "OH", "[1~":
		return "home"
	case "[F", "OF", "[4~":
		return "end"
	case "[5~":
		return "page-up"
	case "[6~":
		return "page-down"
	default:
		return "cancel"
	}
}

func readTUIByte(f *os.File) (byte, error) {
	var buf [1]byte
	_, err := f.Read(buf[:])
	return buf[0], err
}
