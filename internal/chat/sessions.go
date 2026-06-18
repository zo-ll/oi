package chat

import (
	"bufio"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

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

func (c *completionContext) workspaceFiles() ([]string, error) {
	if c == nil {
		return nil, nil
	}
	if c.fileList != nil {
		return c.fileList, nil
	}
	root := c.workspaceRoot
	if root == "" {
		return nil, nil
	}
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path == root {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if shouldSkipPickerDir(name) {
				return filepath.SkipDir
			}
			return nil
		}
		if shouldSkipPickerFile(name) {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		out = append(out, rel)
		if len(out) >= 5000 {
			return fs.SkipAll
		}
		return nil
	})
	if err != nil && err != fs.SkipAll {
		return nil, err
	}
	sort.Strings(out)
	c.fileList = out
	return out, nil
}

func shouldSkipPickerDir(name string) bool {
	switch name {
	case ".git", ".hg", ".svn", "node_modules", "dist", "build", "tmp", "vendor", ".next", "coverage":
		return true
	default:
		return strings.HasPrefix(name, ".cache")
	}
}

func shouldSkipPickerFile(name string) bool {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".png"), strings.HasSuffix(lower, ".jpg"), strings.HasSuffix(lower, ".jpeg"), strings.HasSuffix(lower, ".gif"), strings.HasSuffix(lower, ".webp"), strings.HasSuffix(lower, ".pdf"), strings.HasSuffix(lower, ".zip"), strings.HasSuffix(lower, ".gz"), strings.HasSuffix(lower, ".tar"), strings.HasSuffix(lower, ".ico"), strings.HasSuffix(lower, ".woff"), strings.HasSuffix(lower, ".woff2"), strings.HasSuffix(lower, ".ttf"), strings.HasSuffix(lower, ".bin"):
		return true
	default:
		return false
	}
}

func fuzzyFileMatches(query string, files []string, limit int) []string {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil
	}
	type scored struct {
		path  string
		score int
	}
	var scoredFiles []scored
	for _, path := range files {
		score := scoreFileMatch(query, path)
		if score > 0 {
			scoredFiles = append(scoredFiles, scored{path: path, score: score})
		}
	}
	sort.SliceStable(scoredFiles, func(i, j int) bool {
		if scoredFiles[i].score != scoredFiles[j].score {
			return scoredFiles[i].score > scoredFiles[j].score
		}
		return scoredFiles[i].path < scoredFiles[j].path
	})
	if limit > 0 && len(scoredFiles) > limit {
		scoredFiles = scoredFiles[:limit]
	}
	out := make([]string, 0, len(scoredFiles))
	for _, item := range scoredFiles {
		out = append(out, item.path)
	}
	return out
}

func scoreFileMatch(query, path string) int {
	pathLower := strings.ToLower(filepath.ToSlash(path))
	baseLower := strings.ToLower(filepath.Base(pathLower))
	score := 0
	switch {
	case baseLower == query:
		score += 200
	case strings.Contains(baseLower, query):
		score += 140
	case strings.Contains(pathLower, query):
		score += 100
	}
	if subseq := subsequenceScore(query, baseLower); subseq > 0 {
		score += subseq + 40
	}
	if subseq := subsequenceScore(query, pathLower); subseq > 0 {
		score += subseq
	}
	for _, token := range splitQueryTokens(query) {
		switch {
		case token == "":
		case baseLower == token:
			score += 40
		case strings.Contains(baseLower, token):
			score += 24
		case strings.Contains(pathLower, token):
			score += 16
		}
	}
	if strings.Count(pathLower, "/") == 0 {
		score += 5
	}
	return score
}

func subsequenceScore(query, target string) int {
	if query == "" || target == "" {
		return 0
	}
	qi := 0
	gaps := 0
	prev := -1
	for i, r := range target {
		if qi >= len(query) {
			break
		}
		if byte(r) != query[qi] {
			continue
		}
		if prev >= 0 {
			gaps += i - prev - 1
		}
		prev = i
		qi++
	}
	if qi != len(query) {
		return 0
	}
	score := len(query)*6 - gaps
	if score < 1 {
		return 1
	}
	return score
}

func splitQueryTokens(query string) []string {
	parts := strings.FieldsFunc(query, func(r rune) bool {
		return r == '/' || r == '-' || r == '_' || r == '.' || unicode.IsSpace(r)
	})
	var out []string
	seen := map[string]bool{}
	for _, part := range parts {
		part = strings.TrimSpace(strings.ToLower(part))
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	return out
}
