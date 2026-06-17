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

type pickerUI interface {
	overlayPicker(title string, items []string) (string, bool)
}

type inputUI interface {
	overlayInput(title, prompt, initial string) (string, bool)
}

const maxCompletionMatchesShown = 7

func (c *completionContext) completeAtPath(current string) (next string, status string, matches []string, changed bool, err error) {
	matches, err = c.completionMatchesForText(current)
	if err != nil {
		return current, "", nil, false, err
	}
	if len(matches) == 0 {
		c.completion = completionState{}
		return current, "", nil, false, nil
	}
	if exact := exactMatch(current, matches); exact != "" {
		c.completion = completionState{candidates: []string{exact}, index: 0}
		return replaceTrailingToken(current, exact), exact, matches, true, nil
	}
	if len(matches) == 1 {
		c.completion = completionState{candidates: matches, index: 0}
		return replaceTrailingToken(current, matches[0]), matches[0], matches, true, nil
	}
	c.completion = completionState{}
	return current, formatCompletionMatches(matches), matches, false, nil
}

func (c *completionContext) completionMatchesForText(current string) ([]string, error) {
	if c == nil {
		return nil, nil
	}
	start, _, token, ok := trailingToken(current)
	if !ok {
		return nil, nil
	}
	if strings.HasPrefix(token, "@") {
		query := strings.TrimSpace(strings.TrimPrefix(token, "@"))
		files, err := c.workspaceFiles()
		if err != nil {
			return nil, err
		}
		if query == "" {
			if len(files) > maxCompletionMatchesShown*3 {
				files = files[:maxCompletionMatchesShown*3]
			}
			if len(files) == 0 {
				return nil, nil
			}
			return files, nil
		}
		return fuzzyFileMatches(query, files, maxCompletionMatchesShown+1), nil
	}
	if start == 0 && strings.HasPrefix(token, "/") {
		query := strings.TrimSpace(strings.TrimPrefix(token, "/"))
		cmds := chatCommands()
		if query == "" {
			return cmds, nil
		}
		return filterByPrefix(cmds, query), nil
	}
	return nil, nil
}

var chatCommandList = []string{
	"/help", "/login", "/model", "/stream", "/think", "/tools", "/autosave",
	"/status", "/new", "/save", "/session", "/compact", "/clear", "/exit",
}

func chatCommands() []string {
	return append([]string(nil), chatCommandList...)
}

func filterByPrefix(items []string, prefix string) []string {
	prefix = strings.ToLower(prefix)
	var out []string
	for _, item := range items {
		if strings.HasPrefix(strings.ToLower(item), "/") && strings.HasPrefix(strings.ToLower(strings.TrimPrefix(item, "/")), prefix) {
			out = append(out, item)
		}
	}
	return out
}

func exactMatch(current string, matches []string) string {
	if len(matches) == 0 {
		return ""
	}
	_, _, token, ok := trailingToken(current)
	if !ok {
		return ""
	}
	query := strings.TrimSpace(strings.TrimPrefix(token, strings.ToLower(string(token[:1]))))
	query = strings.ToLower(query)
	if query == "" {
		return ""
	}
	var exact string
	count := 0
	for _, match := range matches {
		lower := strings.ToLower(match)
		if !strings.HasPrefix(token, "@") {
			if lower == strings.ToLower(token) {
				exact = match
				count++
			}
		} else {
			slashed := strings.ToLower(filepath.ToSlash(match))
			if slashed == query {
				exact = match
				count++
			}
			if strings.ToLower(filepath.Base(slashed)) == query {
				if exact == "" {
					exact = match
				}
			}
		}
	}
	if count == 1 && exact != "" {
		return exact
	}
	return ""
}

func replaceTrailingToken(current, replacement string) string {
	start, end, _, ok := trailingToken(current)
	if !ok {
		return current
	}
	return current[:start] + replacement + current[end:]
}

func liveHint(matches []string) string {
	if len(matches) == 0 {
		return ""
	}
	if len(matches) == 1 {
		return "tab \u2192 " + matches[0]
	}
	return fmt.Sprintf("%d matches  tab\u2192pick", len(matches))
}

func pickerHint(matches []string, index int) string {
	if len(matches) == 0 {
		return ""
	}
	if index < 0 {
		index = 0
	}
	if index >= len(matches) {
		index = len(matches) - 1
	}
	shown := matches
	if len(shown) > maxCompletionMatchesShown {
		start := index - maxCompletionMatchesShown/2
		if start < 0 {
			start = 0
		}
		if start+maxCompletionMatchesShown > len(matches) {
			start = len(matches) - maxCompletionMatchesShown
		}
		if start < 0 {
			start = 0
		}
		shown = shown[start : start+maxCompletionMatchesShown]
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%d matches", len(matches)))
	b.WriteString("  up/down nav  enter pick\n")
	for _, match := range shown {
		marker := "  "
		if strings.EqualFold(match, matches[index]) {
			marker = "> "
		}
		fmt.Fprintf(&b, "%s%s\n", marker, match)
	}
	return strings.TrimRight(b.String(), "\n")
}

func filterPickerItems(items []string, query string) []string {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return append([]string(nil), items...)
	}
	var out []string
	for _, item := range items {
		if strings.Contains(strings.ToLower(item), query) {
			out = append(out, item)
		}
	}
	return out
}

func formatCompletionMatches(matches []string) string {
	if len(matches) == 0 {
		return ""
	}
	shown := matches
	if len(shown) > maxCompletionMatchesShown {
		shown = shown[:maxCompletionMatchesShown]
	}
	var b strings.Builder
	b.WriteString("matches:")
	for i, match := range shown {
		fmt.Fprintf(&b, "\n %d. %s", i+1, match)
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
