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
		fmt.Fprintln(out, "/help                show commands")
		fmt.Fprintln(out, "/login [provider]    log in and switch provider")
		fmt.Fprintln(out, "/provider [name]     show, pick, or set provider")
		fmt.Fprintln(out, "/model [name]        show models or set model")
		fmt.Fprintln(out, "/stream [on|off]     show or set streaming mode")
		fmt.Fprintln(out, "/autosave [on|off]   show or set autosave mode")
		fmt.Fprintln(out, "/new                 start a new session")
		fmt.Fprintln(out, "/sessions            list saved sessions")
		fmt.Fprintln(out, "/save [name]         save current session")
		fmt.Fprintln(out, "/load <name|path>    load a saved session")
		fmt.Fprintln(out, "/exit                exit chat")
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
		if arg == "" {
			choice, err := promptProviderChoice(reader, out, cfg, sel.Provider)
			if err != nil {
				return false, rt, sel, streaming, autosave, err
			}
			if choice == "" {
				return false, rt, sel, streaming, autosave, nil
			}
			arg = choice
		}
		nextRT, nextSel, err := switchChatProvider(deps, cfg, sel, rt, reader, out, arg)
		if err != nil {
			return false, rt, sel, streaming, autosave, err
		}
		if autosave {
			if _, err := saveSession(nextRT, nextSel); err != nil {
				fmt.Fprintf(out, "warning: autosave failed: %v\n", err)
			}
		}
		return false, nextRT, nextSel, streaming, autosave, nil
	case "/model":
		if arg == "" {
			choice, err := promptModelChoice(reader, out, rt, sel.Model)
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
