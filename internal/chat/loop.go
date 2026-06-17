package chat

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/zo-ll/oi/internal/lineedit"
	"github.com/zo-ll/oi/internal/workspace"
)

func Run(args []string, in io.Reader, out io.Writer, deps Dependencies) error {
	if inFile, ok := in.(*os.File); ok && lineedit.IsTerminal(inFile) {
		if outFile, ok := out.(*os.File); ok && lineedit.IsTerminal(outFile) {
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

func runEditMode(args []string, in *os.File, out *os.File, deps Dependencies) (err error) {
	root, err := workspace.DetectRoot("")
	if err != nil {
		return err
	}
	reader := bufio.NewReader(in)
	completer := &completionContext{workspaceRoot: root}
	editor := lineedit.New(in, out, "> ", func(text string) (lineedit.Completion, error) {
		next, _, matches, changed, err := completer.completeAtPath(text)
		if err != nil {
			return lineedit.Completion{}, err
		}
		if changed {
			return lineedit.Completion{Text: next, Matches: matches}, nil
		}
		return lineedit.Completion{Matches: matches}, nil
	})
	defer editor.Close()
	cmdOut := interactiveCommandOutput{Writer: editor, editor: editor}

	state, startupNotice, err := loadChatRuntime(args, root, reader, cmdOut)
	if err != nil {
		return err
	}
	if startupNotice != "" {
		fmt.Fprintln(cmdOut, startupNotice)
	}

	for {
		line, err := editor.ReadLine()
		if err != nil {
			return exitChat(out, state.rt, state.sel, state.autosave)
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "/") {
			exit, cmdErr := runChatCommand(deps, state, reader, cmdOut, line)
			if cmdErr != nil {
				fmt.Fprintf(cmdOut, "error: %v\n", cmdErr)
			}
			if exit {
				return nil
			}
			continue
		}
		if state.streaming {
			runStreamingTurnLine(cmdOut, state, line)
		} else {
			runNonStreamingTurnLine(cmdOut, state, line)
		}
	}
}

type interactiveCommandOutput struct {
	io.Writer
	editor *lineedit.Editor
}

func (o interactiveCommandOutput) overlayPicker(title string, items []string) (string, bool) {
	if o.editor == nil {
		return "", false
	}
	selected, ok, err := o.editor.Select(title, items)
	if err != nil {
		return "", false
	}
	return selected, ok
}

func (o interactiveCommandOutput) Styled(kind, text string) string {
	if o.editor == nil {
		return text
	}
	return o.editor.Styled(kind, text)
}

func (o interactiveCommandOutput) ShowStatus(text string) {
	if o.editor != nil {
		o.editor.ShowStatus(text)
	}
}

func (o interactiveCommandOutput) ClearStatus() {
	if o.editor != nil {
		o.editor.ClearStatus()
	}
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
