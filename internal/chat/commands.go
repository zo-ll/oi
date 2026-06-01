package chat

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/zo-ll/oi/internal/agent"
	"github.com/zo-ll/oi/internal/config"
	"github.com/zo-ll/oi/internal/session"
	"github.com/zo-ll/oi/internal/workspace"
)

func handleChatCommand(deps Dependencies, cfg *config.Config, sel config.Selection, rt *agent.Runtime, reader *bufio.Reader, out io.Writer, line string, streaming bool, autosave bool) (bool, *agent.Runtime, config.Selection, bool, bool, error) {
	fields := strings.Fields(line)
	cmd := fields[0]
	arg := ""
	if len(fields) > 1 {
		arg = strings.TrimSpace(strings.Join(fields[1:], " "))
	}

	switch cmd {
	case "/help":
		printHelpLine(out, "/help", "show commands")
		printHelpLine(out, "/login [provider]", "set up provider authentication")
		printHelpLine(out, "/model [name]", "show ready models and set model")
		printHelpLine(out, "/stream [on|off]", "show or set streaming mode")
		printHelpLine(out, "/autosave [on|off]", "show or set autosave mode")
		printHelpLine(out, "/new", "start a new session")
		printHelpLine(out, "/sessions", "list saved sessions")
		printHelpLine(out, "/save [name]", "save current session")
		printHelpLine(out, "/load <name|path>", "load a saved session")
		printHelpLine(out, "/exit", "exit interactive mode")
		printHelpLine(out, "Ctrl+V", "paste system clipboard")
		printHelpLine(out, "Ctrl+Y", "copy last assistant reply")
		printHelpLine(out, "Ctrl+K", "insert newline")
		printHelpLine(out, "Ctrl+D", "exit on empty input")
		return false, rt, sel, streaming, autosave, nil
	case "/exit", "/quit":
		if err := exitChat(reader, out, rt, sel, autosave); err != nil {
			return false, rt, sel, streaming, autosave, err
		}
		return true, rt, sel, streaming, autosave, nil
	case "/new":
		root, err := workspace.DetectRoot(rt.Policy.Root)
		if err != nil {
			return false, rt, sel, streaming, autosave, err
		}
		rt.Session = session.New(sel.Provider, sel.Model, root)
		fmt.Fprintln(out, "new session started")
		if autosave {
			if _, err := saveSession(rt, sel); err != nil {
				fmt.Fprintf(out, "warning: autosave failed: %v\n", err)
			}
		}
		return false, rt, sel, streaming, autosave, nil
	case "/login":
		nextRT, nextSel, err := loginAndSwitchChatProvider(deps, cfg, sel, rt, reader, out, fields[1:])
		if err != nil {
			return false, rt, sel, streaming, autosave, err
		}
		if autosave {
			if _, err := saveSession(nextRT, nextSel); err != nil {
				fmt.Fprintf(out, "warning: autosave failed: %v\n", err)
			}
		}
		return false, nextRT, nextSel, streaming, autosave, nil
	case "/provider":
		return false, rt, sel, streaming, autosave, fmt.Errorf("/provider was removed; use /model")
	case "/model":
		if arg == "" {
			choice, err := promptModelChoice(reader, out, sel)
			if err != nil {
				return false, rt, sel, streaming, autosave, err
			}
			if choice == "" {
				return false, rt, sel, streaming, autosave, nil
			}
			arg = choice
		}
		nextRT, nextSel, err := switchChatModel(cfg, sel, rt, reader, out, arg)
		if err != nil {
			return false, rt, sel, streaming, autosave, err
		}
		if autosave {
			if _, err := saveSession(nextRT, nextSel); err != nil {
				fmt.Fprintf(out, "warning: autosave failed: %v\n", err)
			}
		}
		return false, nextRT, nextSel, streaming, autosave, nil
	case "/stream":
		if arg == "" {
			fmt.Fprintf(out, "streaming: %s\n", onOff(streaming))
			return false, rt, sel, streaming, autosave, nil
		}
		switch strings.ToLower(arg) {
		case "on":
			fmt.Fprintln(out, "streaming: on")
			return false, rt, sel, true, autosave, nil
		case "off":
			fmt.Fprintln(out, "streaming: off")
			return false, rt, sel, false, autosave, nil
		default:
			return false, rt, sel, streaming, autosave, fmt.Errorf("usage: /stream [on|off]")
		}
	case "/autosave":
		if arg == "" {
			fmt.Fprintf(out, "autosave: %s\n", onOff(autosave))
			return false, rt, sel, streaming, autosave, nil
		}
		switch strings.ToLower(arg) {
		case "on":
			fmt.Fprintln(out, "autosave: on")
			if _, err := saveSession(rt, sel); err != nil {
				fmt.Fprintf(out, "warning: autosave failed: %v\n", err)
			}
			return false, rt, sel, streaming, true, nil
		case "off":
			fmt.Fprintln(out, "autosave: off")
			return false, rt, sel, streaming, false, nil
		default:
			return false, rt, sel, streaming, autosave, fmt.Errorf("usage: /autosave [on|off]")
		}
	case "/sessions":
		infos, err := filteredSessions(config.SessionsDir(), arg)
		if err != nil {
			return false, rt, sel, streaming, autosave, err
		}
		if len(infos) == 0 {
			fmt.Fprintln(out, "no saved sessions")
			return false, rt, sel, streaming, autosave, nil
		}
		printSessions(out, infos)
		return false, rt, sel, streaming, autosave, nil
	case "/save":
		path, err := saveSessionNamed(rt, sel, arg)
		if err != nil {
			return false, rt, sel, streaming, autosave, err
		}
		fmt.Fprintf(out, "saved: %s\n", path)
		return false, rt, sel, streaming, autosave, nil
	case "/load":
		path, err := resolveLoadTarget(reader, out, config.SessionsDir(), arg)
		if err != nil {
			return false, rt, sel, streaming, autosave, err
		}
		if path == "" {
			fmt.Fprintln(out, "load cancelled")
			return false, rt, sel, streaming, autosave, nil
		}
		loaded, err := session.Load(path)
		if err != nil {
			return false, rt, sel, streaming, autosave, err
		}
		nextSel := sel
		if loaded.Provider != "" {
			nextSel.Provider = loaded.Provider
		}
		if loaded.Model != "" {
			nextSel.Model = loaded.Model
		}
		cfg2, nextSel2, err := loadSelection(commonOptions{provider: nextSel.Provider, model: nextSel.Model})
		if err != nil {
			return false, rt, sel, streaming, autosave, err
		}
		p, err := requireProvider(nextSel2)
		if err != nil {
			return false, rt, sel, streaming, autosave, err
		}
		root := rt.Policy.Root
		if loaded.CWD != "" {
			if detected, detectErr := workspace.DetectRoot(loaded.CWD); detectErr == nil {
				root = detected
			}
		}
		*cfg = *cfg2
		nextRT := buildRuntime(cfg, nextSel2, p, root, reader, out, rt.Logger)
		loaded.Provider = nextSel2.Provider
		loaded.Model = nextSel2.Model
		loaded.CWD = root
		nextRT.Session = loaded
		fmt.Fprintf(out, "loaded: %s\n", path)
		return false, nextRT, nextSel2, streaming, autosave, nil
	default:
		return false, rt, sel, streaming, autosave, fmt.Errorf("unknown command: %s", cmd)
	}
}
