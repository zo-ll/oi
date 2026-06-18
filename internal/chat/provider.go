package chat

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/zo-ll/oi/internal/agent"
	"github.com/zo-ll/oi/internal/config"
	"github.com/zo-ll/oi/internal/provider"
)

// Status display and interactive choice prompts. This file used to be a tiny
// placeholder; it now holds the /status renderer and the small on/off + enum
// pickers used by slash commands.

func autoCompactLabel(threshold int) string {
	if threshold < 0 {
		return "off"
	}
	if threshold == 0 {
		return "90% (default)"
	}
	return fmt.Sprintf("%d%%", threshold)
}

func printStatus(out io.Writer, sel config.Selection, rt *agent.Runtime, streaming bool, autosave bool, tools toolVerbosity) {
	root := ""
	model := sel.Model
	providerName := sel.Provider
	contextWindow := 0
	usage := provider.Usage{}
	thinking := "off"
	thinkingSupported := false
	sessionMessages := 0
	autoCompact := "90%"
	if rt != nil {
		root = rt.Policy.Root
		contextWindow = rt.ContextWindow
		usage = rt.LastUsage
		thinking = valueOr(rt.ThinkingLevel, "off")
		thinkingSupported = rt.ThinkingSupported
		autoCompact = autoCompactLabel(rt.AutoCompactThreshold)
		if rt.Provider != nil && rt.Provider.Model() != "" {
			model = rt.Provider.Model()
		}
		if rt.Session != nil {
			sessionMessages = len(rt.Session.Messages)
			providerName = valueOr(rt.Session.Provider, providerName)
			model = valueOr(rt.Session.Model, model)
		}
	}
	if !thinkingSupported {
		thinking = "n/a"
	}
	fmt.Fprintf(out, "provider: %s\n", valueOr(providerName, "(none)"))
	fmt.Fprintf(out, "model: %s\n", valueOr(model, "(none)"))
	fmt.Fprintf(out, "cwd: %s\n", valueOr(root, "(none)"))
	fmt.Fprintf(out, "context: %s\n", valueOr(formatStatusContextUsage(contextWindow, usage), "n/a"))
	fmt.Fprintf(out, "thinking: %s\n", thinking)
	fmt.Fprintf(out, "streaming: %s\n", onOff(streaming))
	fmt.Fprintf(out, "autosave: %s\n", onOff(autosave))
	fmt.Fprintf(out, "tools: %s\n", tools)
	fmt.Fprintf(out, "auto-compact: %s\n", autoCompact)
	fmt.Fprintf(out, "session messages: %d\n", sessionMessages)
}

func chooseStreamMode(reader *bufio.Reader, out io.Writer, current bool) (*bool, error) {
	selected, ok, err := pickSimpleChoice(reader, out, "choose streaming mode", []string{"on", "off"})
	if err != nil || !ok {
		return nil, err
	}
	next := strings.EqualFold(selected, "on")
	_ = current
	return &next, nil
}

func chooseToolVerbosity(reader *bufio.Reader, out io.Writer, current toolVerbosity) (toolVerbosity, bool, error) {
	selected, ok, err := pickSimpleChoice(reader, out, "choose tool visibility", []string{string(toolVerbosityOff), string(toolVerbosityErrors), string(toolVerbosityOn)})
	if err != nil || !ok {
		return current, false, err
	}
	next, err := parseToolVerbosity(selected)
	if err != nil {
		return current, false, err
	}
	return next, true, nil
}

func chooseAutosaveMode(reader *bufio.Reader, out io.Writer, current bool) (*bool, error) {
	selected, ok, err := pickSimpleChoice(reader, out, "choose autosave mode", []string{"on", "off"})
	if err != nil || !ok {
		return nil, err
	}
	next := strings.EqualFold(selected, "on")
	_ = current
	return &next, nil
}

func promptSaveName(reader *bufio.Reader, out io.Writer) (string, error) {
	if ui, ok := out.(inputUI); ok {
		text, saved := ui.overlayInput("save session", "name: ", "")
		if !saved {
			return "", nil
		}
		return strings.TrimSpace(text), nil
	}
	fmt.Fprint(out, "Save as? [blank=current] ")
	text, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimSpace(text), nil
}

func chooseThinkingLevel(reader *bufio.Reader, out io.Writer, current string, levels []string) (string, bool, error) {
	selected, ok, err := pickSimpleChoice(reader, out, "choose thinking level", levels)
	if err != nil || !ok {
		return current, false, err
	}
	level, err := parseThinkingLevel(selected)
	if err != nil {
		return current, false, err
	}
	return level, true, nil
}

func parseThinkingLevel(level string) (string, error) {
	level = strings.ToLower(strings.TrimSpace(level))
	switch level {
	case "off", "minimal", "low", "medium", "high", "xhigh":
		return level, nil
	case "default":
		return "", nil
	default:
		return "", fmt.Errorf("usage: /think")
	}
}

func pickSimpleChoice(reader *bufio.Reader, out io.Writer, title string, items []string) (string, bool, error) {
	if picker, ok := out.(pickerUI); ok {
		selected, picked := picker.overlayPicker(title, items)
		if !picked || strings.TrimSpace(selected) == "" {
			return "", false, nil
		}
		return selected, true, nil
	}
	fmt.Fprintln(out, title)
	for i, item := range items {
		fmt.Fprintf(out, "%2d. %s\n", i+1, item)
	}
	fmt.Fprint(out, "Choose? [number/name, blank=cancel] ")
	text, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", false, err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", false, nil
	}
	if idx, ok := parseSessionIndex(text); ok {
		if idx < 1 || idx > len(items) {
			return "", false, fmt.Errorf("choice index out of range: %d", idx)
		}
		return items[idx-1], true, nil
	}
	for _, item := range items {
		if strings.EqualFold(item, text) {
			return item, true, nil
		}
	}
	return "", false, fmt.Errorf("invalid choice: %s", text)
}
