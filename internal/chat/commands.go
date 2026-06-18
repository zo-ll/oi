package chat

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/zo-ll/oi/internal/agent"
	"github.com/zo-ll/oi/internal/config"
	"github.com/zo-ll/oi/internal/provider"
	"github.com/zo-ll/oi/internal/session"
	"github.com/zo-ll/oi/internal/workspace"
)

func handleChatCommand(deps Dependencies, cfg *config.Config, sel config.Selection, rt *agent.Runtime, reader *bufio.Reader, out io.Writer, line string, streaming bool, autosave bool, tools toolVerbosity) (bool, *agent.Runtime, config.Selection, bool, bool, toolVerbosity, error) {
	fields := strings.Fields(line)
	cmd := fields[0]
	arg := ""
	if len(fields) > 1 {
		arg = strings.TrimSpace(strings.Join(fields[1:], " "))
	}

	switch cmd {
	case "/help":
		if arg != "" {
			return false, rt, sel, streaming, autosave, tools, fmt.Errorf("usage: /help")
		}
		printHelpLine(out, "/help", "show commands")
		printHelpLine(out, "/status", "show model, context, and session status")
		printHelpLine(out, "/login", "set up provider authentication")
		printHelpLine(out, "/model", "choose and set model")
		printHelpLine(out, "/stream", "choose streaming mode")
		printHelpLine(out, "/think", "set thinking level")
		printHelpLine(out, "/tools", "choose tool events level")
		printHelpLine(out, "/autosave", "choose autosave mode")
		printHelpLine(out, "/new", "start a new session")
		printHelpLine(out, "/save", "save current session")
		printHelpLine(out, "/session", "browse and load sessions")
		printHelpLine(out, "/compact", "compact session history now")
		printHelpLine(out, "/clear", "clear the screen")
		printHelpLine(out, "/exit", "exit interactive mode")
		printHelpLine(out, "Ctrl+V", "paste system clipboard")
		printHelpLine(out, "Ctrl+Y", "copy last assistant reply")
		printHelpLine(out, "Ctrl+K", "insert newline")
		printHelpLine(out, "Ctrl+D", "exit on empty input")
		return false, rt, sel, streaming, autosave, tools, nil
	case "/status":
		if arg != "" {
			return false, rt, sel, streaming, autosave, tools, fmt.Errorf("usage: /status")
		}
		printStatus(out, sel, rt, streaming, autosave, tools)
		return false, rt, sel, streaming, autosave, tools, nil
	case "/compact":
		if arg != "" {
			return false, rt, sel, streaming, autosave, tools, fmt.Errorf("usage: /compact")
		}
		if rt == nil || rt.Session == nil {
			return false, rt, sel, streaming, autosave, tools, fmt.Errorf("no session to compact")
		}
		changed, _ := rt.ForceCompactSession()
		if changed {
			fmt.Fprintln(out, "session compacted")
			if autosave {
				if _, err := saveSession(rt, sel); err != nil {
					fmt.Fprintf(out, "warning: autosave failed: %v\n", err)
				}
			}
		} else {
			fmt.Fprintln(out, "session already compact")
		}
		return false, rt, sel, streaming, autosave, tools, nil
	case "/clear":
		if arg != "" {
			return false, rt, sel, streaming, autosave, tools, fmt.Errorf("usage: /clear")
		}
		clearScreen(out)
		return false, rt, sel, streaming, autosave, tools, nil
	case "/exit":
		if arg != "" {
			return false, rt, sel, streaming, autosave, tools, fmt.Errorf("usage: /exit")
		}
		if err := exitChat(out, rt, sel, autosave); err != nil {
			return false, rt, sel, streaming, autosave, tools, err
		}
		return true, rt, sel, streaming, autosave, tools, nil
	case "/new":
		if arg != "" {
			return false, rt, sel, streaming, autosave, tools, fmt.Errorf("usage: /new")
		}
		root, err := workspace.DetectRoot(rt.Policy.Root)
		if err != nil {
			return false, rt, sel, streaming, autosave, tools, err
		}
		rt.Session = session.New(sel.Provider, sel.Model, root)
		modelInfo := provider.Model{SupportsThinking: rt.ThinkingSupported, SupportedThinkingLevels: rt.SupportedThinkingLevels, ThinkingLevelValues: rt.ThinkingLevelValues}
		rt.ThinkingLevel = clampThinkingLevel(modelInfo, "off")
		rt.ThinkingValue = thinkingValue(modelInfo, rt.ThinkingLevel)
		rt.Session.ThinkingLevel = rt.ThinkingLevel
		fmt.Fprintln(out, "new session started")
		if autosave {
			if _, err := saveSession(rt, sel); err != nil {
				fmt.Fprintf(out, "warning: autosave failed: %v\n", err)
			}
		}
		return false, rt, sel, streaming, autosave, tools, nil
	case "/login":
		if arg != "" {
			return false, rt, sel, streaming, autosave, tools, fmt.Errorf("usage: /login")
		}
		nextRT, nextSel, err := loginAndSwitchChatProvider(deps, cfg, sel, rt, reader, out, nil)
		if err != nil {
			return false, rt, sel, streaming, autosave, tools, err
		}
		if autosave {
			if _, err := saveSession(nextRT, nextSel); err != nil {
				fmt.Fprintf(out, "warning: autosave failed: %v\n", err)
			}
		}
		return false, nextRT, nextSel, streaming, autosave, tools, nil
	case "/model":
		if arg != "" {
			return false, rt, sel, streaming, autosave, tools, fmt.Errorf("usage: /model")
		}
		if arg == "" {
			if picker, ok := out.(pickerUI); ok {
				choice, err := modelPickerPick(picker, sel.Provider)
				if err != nil {
					return false, rt, sel, streaming, autosave, tools, err
				}
				if choice == "" {
					return false, rt, sel, streaming, autosave, tools, nil
				}
				arg = choice
			} else {
				choice, err := promptModelChoice(reader, out, sel)
				if err != nil {
					return false, rt, sel, streaming, autosave, tools, err
				}
				if choice == "" {
					return false, rt, sel, streaming, autosave, tools, nil
				}
				arg = choice
			}
		}
		nextRT, nextSel, err := switchChatModel(cfg, sel, rt, reader, out, arg)
		if err != nil {
			return false, rt, sel, streaming, autosave, tools, err
		}
		if autosave {
			if _, err := saveSession(nextRT, nextSel); err != nil {
				fmt.Fprintf(out, "warning: autosave failed: %v\n", err)
			}
		}
		return false, nextRT, nextSel, streaming, autosave, tools, nil
	case "/stream":
		if arg != "" {
			return false, rt, sel, streaming, autosave, tools, fmt.Errorf("usage: /stream")
		}
		nextStreaming, err := chooseStreamMode(reader, out, streaming)
		if err != nil {
			return false, rt, sel, streaming, autosave, tools, err
		}
		if nextStreaming == nil {
			return false, rt, sel, streaming, autosave, tools, nil
		}
		fmt.Fprintf(out, "streaming: %s\n", onOff(*nextStreaming))
		return false, rt, sel, *nextStreaming, autosave, tools, nil
	case "/think":
		if arg != "" {
			return false, rt, sel, streaming, autosave, tools, fmt.Errorf("usage: /think")
		}
		levels := []string{"default", "off", "low", "medium", "high"}
		if rt != nil && len(rt.SupportedThinkingLevels) > 0 {
			levels = append([]string{"default"}, rt.SupportedThinkingLevels...)
		}
		currentLevel := "off"
		if rt != nil && rt.ThinkingLevel != "" {
			currentLevel = rt.ThinkingLevel
		}
		level, ok, err := chooseThinkingLevel(reader, out, currentLevel, levels)
		if err != nil {
			return false, rt, sel, streaming, autosave, tools, err
		}
		if !ok {
			return false, rt, sel, streaming, autosave, tools, nil
		}
		if level != "" && level != "off" && (rt == nil || !rt.ThinkingSupported) {
			return false, rt, sel, streaming, autosave, tools, fmt.Errorf("selected model does not advertise thinking levels")
		}
		if rt != nil {
			model := provider.Model{SupportsThinking: rt.ThinkingSupported, SupportedThinkingLevels: rt.SupportedThinkingLevels, ThinkingLevelValues: rt.ThinkingLevelValues}
			rt.ThinkingLevel = clampThinkingLevel(model, level)
			rt.ThinkingValue = thinkingValue(model, rt.ThinkingLevel)
			if rt.Session != nil {
				rt.Session.ThinkingLevel = rt.ThinkingLevel
			}
			level = rt.ThinkingLevel
		}
		if autosave {
			if _, err := saveSession(rt, sel); err != nil {
				fmt.Fprintf(out, "warning: autosave failed: %v\n", err)
			}
		}
		fmt.Fprintf(out, "thinking: %s\n", valueOr(level, "off"))
		return false, rt, sel, streaming, autosave, tools, nil
	case "/tools":
		if arg != "" {
			return false, rt, sel, streaming, autosave, tools, fmt.Errorf("usage: /tools")
		}
		nextTools, ok, err := chooseToolVerbosity(reader, out, tools)
		if err != nil {
			return false, rt, sel, streaming, autosave, tools, err
		}
		if !ok {
			return false, rt, sel, streaming, autosave, tools, nil
		}
		fmt.Fprintf(out, "tools: %s\n", nextTools)
		return false, rt, sel, streaming, autosave, nextTools, nil
	case "/autosave":
		if arg != "" {
			return false, rt, sel, streaming, autosave, tools, fmt.Errorf("usage: /autosave")
		}
		nextAutosave, err := chooseAutosaveMode(reader, out, autosave)
		if err != nil {
			return false, rt, sel, streaming, autosave, tools, err
		}
		if nextAutosave == nil {
			return false, rt, sel, streaming, autosave, tools, nil
		}
		fmt.Fprintf(out, "autosave: %s\n", onOff(*nextAutosave))
		if *nextAutosave {
			if _, err := saveSession(rt, sel); err != nil {
				fmt.Fprintf(out, "warning: autosave failed: %v\n", err)
			}
		}
		return false, rt, sel, streaming, *nextAutosave, tools, nil
	case "/save":
		if arg != "" {
			return false, rt, sel, streaming, autosave, tools, fmt.Errorf("usage: /save")
		}
		name, err := promptSaveName(reader, out)
		if err != nil {
			return false, rt, sel, streaming, autosave, tools, err
		}
		path, err := saveSessionNamed(rt, sel, name)
		if err != nil {
			return false, rt, sel, streaming, autosave, tools, err
		}
		fmt.Fprintf(out, "saved: %s\n", path)
		return false, rt, sel, streaming, autosave, tools, nil
	case "/session":
		if strings.TrimSpace(arg) != "" {
			return false, rt, sel, streaming, autosave, tools, fmt.Errorf("usage: /session")
		}
		path := ""
		if picker, ok := out.(pickerUI); ok {
			infos, err := filteredSessions(config.SessionsDir(), "")
			if err != nil {
				return false, rt, sel, streaming, autosave, tools, err
			}
			if info, picked := pickSessionInfo(picker, infos, "choose session"); picked {
				path = info.Path
			}
		} else {
			var err error
			path, err = resolveLoadTarget(reader, out, config.SessionsDir(), "")
			if err != nil {
				return false, rt, sel, streaming, autosave, tools, err
			}
		}
		if path == "" {
			return false, rt, sel, streaming, autosave, tools, nil
		}
		loaded, err := session.Load(path)
		if err != nil {
			return false, rt, sel, streaming, autosave, tools, err
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
			return false, rt, sel, streaming, autosave, tools, err
		}
		p, err := requireProvider(nextSel2)
		if err != nil {
			return false, rt, sel, streaming, autosave, tools, err
		}
		root := rt.Policy.Root
		*cfg = *cfg2
		nextRT := buildRuntime(cfg, nextSel2, p, root, reader, out, rt.Logger)
		loaded.Provider = nextSel2.Provider
		loaded.Model = nextSel2.Model
		loaded.CWD = root
		nextRT.Session = loaded
		loadedLevel := loaded.ThinkingLevel
		if loadedLevel == "" {
			loadedLevel = "off"
		}
		modelInfo := provider.Model{SupportsThinking: nextRT.ThinkingSupported, SupportedThinkingLevels: nextRT.SupportedThinkingLevels, ThinkingLevelValues: nextRT.ThinkingLevelValues}
		nextRT.ThinkingLevel = clampThinkingLevel(modelInfo, loadedLevel)
		nextRT.ThinkingValue = thinkingValue(modelInfo, nextRT.ThinkingLevel)
		nextRT.Session.ThinkingLevel = nextRT.ThinkingLevel
		fmt.Fprintf(out, "loaded: %s\n", path)
		if len(loaded.Messages) > 0 {
			fmt.Fprintln(out)
			printSessionTranscript(out, loaded.Messages)
		}
		return false, nextRT, nextSel2, streaming, autosave, tools, nil
	default:
		return false, rt, sel, streaming, autosave, tools, fmt.Errorf("unknown command: %s", cmd)
	}
}
