package chat

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

type completionState struct {
	candidates []string
	index      int
}

const maxCompletionMatchesShown = 8

func (ui *terminalUI) setWorkspaceRoot(root string) {
	if ui == nil {
		return
	}
	ui.workspaceRoot = strings.TrimSpace(root)
	ui.fileList = nil
	ui.completion = completionState{}
}

func (ui *terminalUI) addHistoryEntry(text string) {
	if ui == nil {
		return
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if n := len(ui.history); n > 0 && ui.history[n-1] == text {
		ui.historyIndex = -1
		ui.historyDraft = ""
		return
	}
	ui.history = append(ui.history, text)
	if len(ui.history) > 200 {
		ui.history = ui.history[len(ui.history)-200:]
	}
	ui.historyIndex = -1
	ui.historyDraft = ""
}

func (ui *terminalUI) historyPrev(current string) (string, bool) {
	if ui == nil || len(ui.history) == 0 {
		return "", false
	}
	if ui.historyIndex == -1 {
		ui.historyDraft = current
		ui.historyIndex = len(ui.history) - 1
	} else if ui.historyIndex > 0 {
		ui.historyIndex--
	} else {
		return "", false
	}
	ui.completion = completionState{}
	return ui.history[ui.historyIndex], true
}

func (ui *terminalUI) historyNext() (string, bool) {
	if ui == nil || ui.historyIndex == -1 {
		return "", false
	}
	if ui.historyIndex < len(ui.history)-1 {
		ui.historyIndex++
		ui.completion = completionState{}
		return ui.history[ui.historyIndex], true
	}
	ui.historyIndex = -1
	ui.completion = completionState{}
	return ui.historyDraft, true
}

func (ui *terminalUI) completeAtPath(current string) (next string, status string, matches []string, changed bool, err error) {
	if ui == nil {
		return current, "", nil, false, nil
	}
	start, end, token, ok := trailingToken(current)
	if !ok {
		return current, "", nil, false, nil
	}
	if !strings.HasPrefix(token, "@") {
		ui.completion = completionState{}
		return current, "", nil, false, nil
	}
	query := strings.TrimSpace(strings.TrimPrefix(token, "@"))
	if query == "" {
		return current, "type more after @", nil, false, nil
	}
	files, err := ui.workspaceFiles()
	if err != nil {
		return current, "", nil, false, err
	}
	matches = fuzzyFileMatches(query, files, maxCompletionMatchesShown+1)
	if len(matches) == 0 {
		ui.completion = completionState{}
		return current, fmt.Sprintf("no file match for %q", query), nil, false, nil
	}
	if len(matches) == 1 {
		ui.completion = completionState{candidates: matches, index: 0}
		candidate := matches[0]
		return current[:start] + candidate + current[end:], candidate, matches, true, nil
	}
	ui.completion = completionState{}
	return current, formatCompletionMatches(matches), matches, false, nil
}

func formatCompletionMatches(matches []string) string {
	if len(matches) == 0 {
		return ""
	}
	shown := matches
	more := 0
	if len(shown) > maxCompletionMatchesShown {
		shown = shown[:maxCompletionMatchesShown]
		more = len(matches) - len(shown)
	}
	var b strings.Builder
	b.WriteString("matches:")
	for i, match := range shown {
		fmt.Fprintf(&b, "\n %d. %s", i+1, match)
	}
	if more > 0 {
		fmt.Fprintf(&b, "\n +%d more", more)
	}
	return b.String()
}

func trailingToken(s string) (start, end int, token string, ok bool) {
	runes := []rune(s)
	if len(runes) == 0 {
		return 0, 0, "", false
	}
	end = len(runes)
	start = end
	for start > 0 && !unicode.IsSpace(runes[start-1]) {
		start--
	}
	token = string(runes[start:end])
	return start, end, token, token != ""
}

func (ui *terminalUI) workspaceFiles() ([]string, error) {
	if ui == nil {
		return nil, nil
	}
	if ui.fileList != nil {
		return ui.fileList, nil
	}
	root := ui.workspaceRoot
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
	ui.fileList = out
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
