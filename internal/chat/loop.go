package chat

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/zo-ll/oi/internal/workspace"
	"github.com/zo-ll/tide"
)

func Run(args []string, in io.Reader, out io.Writer, deps Dependencies) error {
	if inFile, ok := in.(*os.File); ok && tide.IsTerminal(inFile) {
		if outFile, ok := out.(*os.File); ok && tide.IsTerminal(outFile) {
			return runEditMode(args, inFile, outFile, deps)
		}
	}
	return runLineMode(args, in, out, deps)
}

type completionContext struct {
	workspaceRoot string
	fileList      []string
	completion    completionState
}

func loadChatRuntime(args []string, root string, in io.Reader, out io.Writer) (*chatState, string, error) {
	opts, err := parseCommonOptions("", args)
	if err != nil {
		return nil, "", err
	}
	cfg, sel, err := loadSelection(opts)
	if err != nil {
		return nil, "", err
	}
	p, startupNotice, err := interactiveProvider(sel)
	if err != nil {
		return nil, "", err
	}
	logger, err := maybeDebugLogger("chat", opts.debug)
	if err != nil {
		return nil, "", err
	}
	rt := buildRuntime(cfg, sel, p, root, in, out, logger)
	state := newChatState(cfg, sel, rt)
	state.reconfigureRuntime(out)
	return state, startupNotice, nil
}

func runLineMode(args []string, in io.Reader, out io.Writer, deps Dependencies) error {
	root, err := workspace.DetectRoot("")
	if err != nil {
		return err
	}
	reader := bufio.NewReader(in)
	state, startupNotice, err := loadChatRuntime(args, root, reader, out)
	if err != nil {
		return err
	}

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
				return exitChat(out, state.rt, state.sel, state.autosave)
			}
			continue
		}
		if strings.HasPrefix(line, "/") {
			exit, cmdErr := runChatCommand(deps, state, reader, out, line)
			if cmdErr != nil {
				fmt.Fprintf(out, "error: %v\n", cmdErr)
			}
			if exit {
				return nil
			}
			if err == io.EOF {
				return nil
			}
			continue
		}
		if state.streaming {
			runStreamingTurnLine(out, state, line)
		} else {
			runNonStreamingTurnLine(out, state, line)
		}
		if err == io.EOF {
			return exitChat(out, state.rt, state.sel, state.autosave)
		}
	}
}

func runChatCommand(deps Dependencies, state *chatState, reader *bufio.Reader, out io.Writer, line string) (bool, error) {
	exit, newRT, newSel, newStreaming, newAutosave, newTools, err := handleChatCommand(deps, state.cfg, state.sel, state.rt, reader, out, line, state.streaming, state.autosave, state.tools)
	if err != nil {
		return false, err
	}
	state.applyCommandResult(newRT, newSel, newStreaming, newAutosave, newTools, out)
	return exit, nil
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
