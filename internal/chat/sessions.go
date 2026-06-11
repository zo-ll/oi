package chat

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/zo-ll/oi/internal/agent"
	"github.com/zo-ll/oi/internal/config"
	"github.com/zo-ll/oi/internal/session"
	"github.com/zo-ll/oi/internal/workspace"
)

func resolveSessionPath(dir, arg string) string {
	if filepath.IsAbs(arg) || strings.ContainsRune(arg, os.PathSeparator) {
		return arg
	}
	name := arg
	if !strings.HasSuffix(name, ".json") {
		name += ".json"
	}
	return filepath.Join(dir, name)
}

func resolveLoadTarget(reader *bufio.Reader, out io.Writer, dir, arg string) (string, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		infos, err := filteredSessions(dir, "")
		if err != nil {
			return "", err
		}
		if len(infos) == 0 {
			return "", nil
		}
		printSessions(out, infos)
		fmt.Fprint(out, "Load which session? [number/id, blank=cancel] ")
		choice, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return "", err
		}
		arg = strings.TrimSpace(choice)
		if arg == "" {
			return "", nil
		}
		return resolveSessionArg(dir, infos, arg)
	}
	infos, err := filteredSessions(dir, "")
	if err != nil {
		return "", err
	}
	return resolveSessionArg(dir, infos, arg)
}

func pickSessionInfo(picker pickerUI, infos []session.Info, title string) (session.Info, bool) {
	if picker == nil || len(infos) == 0 {
		return session.Info{}, false
	}
	labels := make([]string, 0, len(infos))
	labelToInfo := make(map[string]session.Info, len(infos))
	for _, info := range infos {
		label := sessionPickerLabel(info)
		labels = append(labels, label)
		labelToInfo[label] = info
	}
	selected, ok := picker.overlayPicker(title, labels)
	if !ok || strings.TrimSpace(selected) == "" {
		return session.Info{}, false
	}
	info, ok := labelToInfo[selected]
	return info, ok
}

func sessionPickerLabel(info session.Info) string {
	started := info.CreatedAt.Local().Format("2006-01-02 15:04")
	return fmt.Sprintf("%s  %s", valueOr(info.Preview, info.ID), started)
}

func resolveSessionArg(dir string, infos []session.Info, arg string) (string, error) {
	if path, ok, err := resolveSessionExactArg(dir, infos, arg); ok || err != nil {
		return path, err
	}
	matches, err := filteredSessions(dir, arg)
	if err != nil {
		return "", err
	}
	if len(matches) == 1 {
		return matches[0].Path, nil
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("multiple sessions match %q; use /session %s to list matches, then load by index", arg, arg)
	}
	return "", fmt.Errorf("session not found: %s", arg)
}

func resolveSessionExactArg(dir string, infos []session.Info, arg string) (string, bool, error) {
	if filepath.IsAbs(arg) || strings.ContainsRune(arg, os.PathSeparator) {
		return arg, true, nil
	}
	if idx, ok := parseSessionIndex(arg); ok {
		if idx < 1 || idx > len(infos) {
			return "", true, fmt.Errorf("session index out of range: %d", idx)
		}
		return infos[idx-1].Path, true, nil
	}
	for _, info := range infos {
		if info.ID == arg {
			return info.Path, true, nil
		}
	}
	path := resolveSessionPath(dir, arg)
	if _, err := os.Stat(path); err == nil {
		return path, true, nil
	}
	return "", false, nil
}

func parseSessionIndex(arg string) (int, bool) {
	var n int
	if _, err := fmt.Sscanf(arg, "%d", &n); err == nil {
		return n, true
	}
	return 0, false
}

func filteredSessions(dir, filter string) ([]session.Info, error) {
	infos, err := session.List(dir)
	if err != nil {
		return nil, err
	}
	filter = strings.ToLower(strings.TrimSpace(filter))
	if filter == "" {
		return infos, nil
	}
	out := make([]session.Info, 0, len(infos))
	for _, info := range infos {
		hay := strings.ToLower(info.ID + " " + info.Provider + " " + info.Model + " " + info.Preview + " " + filepath.Base(info.Path))
		if strings.Contains(hay, filter) {
			out = append(out, info)
		}
	}
	return out, nil
}

func printSessions(out io.Writer, infos []session.Info) {
	for i, info := range infos {
		fmt.Fprintf(out, "%2d. %s  %s\n", i+1, valueOr(info.Preview, info.ID), info.CreatedAt.Format("2006-01-02 15:04:05"))
	}
}

func printSessionTranscript(out io.Writer, messages []session.Message) {
	for _, msg := range messages {
		text := cleanDisplayText(strings.TrimSpace(msg.Content))
		if text == "" {
			continue
		}
		switch msg.Role {
		case "user":
			fmt.Fprintf(out, "> %s\n", text)
		case "assistant":
			fmt.Fprintln(out, text)
		case "system":
			if msg.Kind == "summary" {
				fmt.Fprintf(out, "[summary] %s\n", text)
			}
		}
		fmt.Fprintln(out)
	}
}

func validateSessionName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("session name must not be empty")
	}
	if strings.ContainsRune(name, os.PathSeparator) {
		return fmt.Errorf("session name must not contain path separators")
	}
	return nil
}

func saveSession(rt *agent.Runtime, sel config.Selection) (string, error) {
	return saveSessionNamed(rt, sel, "")
}

func saveSessionNamed(rt *agent.Runtime, sel config.Selection, name string) (string, error) {
	if rt == nil || rt.Session == nil {
		return "", fmt.Errorf("no session to save")
	}
	target := rt.Session
	if name != "" {
		if err := validateSessionName(name); err != nil {
			return "", err
		}
		clone := *rt.Session
		if rt.Session.Messages != nil {
			clone.Messages = append([]session.Message(nil), rt.Session.Messages...)
		}
		clone.ID = name
		target = &clone
	}
	target.Provider = sel.Provider
	target.Model = sel.Model
	root, err := workspace.DetectRoot(rt.Policy.Root)
	if err == nil {
		target.CWD = root
	}
	return session.Save(config.SessionsDir(), target)
}

func exitChat(out io.Writer, rt *agent.Runtime, sel config.Selection, autosave bool) error {
	if _, err := saveSession(rt, sel); err != nil {
		return err
	}
	if !autosave {
		fmt.Fprintln(out, "session saved")
	}
	return nil
}
