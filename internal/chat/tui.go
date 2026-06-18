package chat

import (
	"bufio"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/zo-ll/oi/internal/agent"
	"github.com/zo-ll/oi/internal/tool"
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
