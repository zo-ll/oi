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

func resolveSessionArg(dir string, infos []session.Info, arg string) (string, error) {
	if filepath.IsAbs(arg) || strings.ContainsRune(arg, os.PathSeparator) {
		return arg, nil
	}
	if idx, ok := parseSessionIndex(arg); ok {
		if idx < 1 || idx > len(infos) {
			return "", fmt.Errorf("session index out of range: %d", idx)
		}
		return infos[idx-1].Path, nil
	}
	for _, info := range infos {
		if info.ID == arg {
			return info.Path, nil
		}
	}
	path := resolveSessionPath(dir, arg)
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	matches, err := filteredSessions(dir, arg)
	if err != nil {
		return "", err
	}
	if len(matches) == 1 {
		return matches[0].Path, nil
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("multiple sessions match %q; use /sessions %s and load by index", arg, arg)
	}
	return "", fmt.Errorf("session not found: %s", arg)
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
		hay := strings.ToLower(info.ID + " " + info.Provider + " " + info.Model + " " + filepath.Base(info.Path))
		if strings.Contains(hay, filter) {
			out = append(out, info)
		}
	}
	return out, nil
}

func printSessions(out io.Writer, infos []session.Info) {
	for i, info := range infos {
		fmt.Fprintf(out, "%2d. %s  %s  %s  %s\n", i+1, info.ID, info.UpdatedAt.Format("2006-01-02 15:04:05"), valueOr(info.Provider, "-"), valueOr(info.Model, "-"))
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
