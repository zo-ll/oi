package retrieval

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

const (
	maxFilesScanned = 2000
	maxFileBytes    = 256 * 1024
	maxContextBytes = 8 * 1024
	maxChunks       = 6
)

type Notice struct {
	SnippetCount int
	FileCount    int
	Bytes        int
	Query        string
	Skipped      bool
}

type chunk struct {
	path      string
	startLine int
	endLine   int
	text      string
	score     int
}

func BuildContext(root, input string) (string, Notice, error) {
	root = strings.TrimSpace(root)
	query := normalizeSpaces(input)
	notice := Notice{Query: query}
	if root == "" || query == "" || !ShouldUse(query) {
		notice.Skipped = true
		return "", notice, nil
	}
	candidates, err := collectCandidates(root, query)
	if err != nil {
		return "", notice, err
	}
	if len(candidates) == 0 {
		return "", notice, nil
	}
	contextText, snippetCount, fileCount, totalBytes := formatContext(root, candidates)
	notice.SnippetCount = snippetCount
	notice.FileCount = fileCount
	notice.Bytes = totalBytes
	return contextText, notice, nil
}

func ShouldUse(input string) bool {
	tokens := tokenize(input)
	if len(tokens) == 0 {
		return false
	}
	joined := strings.ToLower(normalizeSpaces(input))
	if strings.Contains(joined, string(os.PathSeparator)) || strings.Contains(joined, ".go") || strings.Contains(joined, "stack trace") {
		return true
	}
	for _, token := range tokens {
		switch {
		case strings.Contains(token, "."):
			return true
		case strings.Contains(token, "_"):
			return true
		case strings.Contains(token, "-"):
			return true
		case len(token) > 0 && (unicode.IsUpper(rune(token[0])) || strings.ContainsAny(token, "(){}[]:/")):
			return true
		}
	}
	keywords := map[string]bool{
		"repo": true, "repository": true, "code": true, "function": true, "method": true, "struct": true,
		"interface": true, "package": true, "import": true, "type": true, "file": true, "files": true,
		"path": true, "error": true, "panic": true, "bug": true, "test": true, "config": true,
		"workspace": true, "rpc": true, "provider": true, "session": true, "model": true,
	}
	for _, token := range tokens {
		if keywords[token] {
			return true
		}
	}
	return len(tokens) >= 4
}

func collectCandidates(root, query string) ([]chunk, error) {
	queryTokens := tokenize(query)
	if len(queryTokens) == 0 {
		return nil, nil
	}
	var paths []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path == root {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if shouldSkipDir(name) {
				return filepath.SkipDir
			}
			return nil
		}
		if shouldSkipFile(name) {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() <= 0 || info.Size() > maxFileBytes {
			return nil
		}
		paths = append(paths, path)
		if len(paths) >= maxFilesScanned {
			return fs.SkipAll
		}
		return nil
	})
	if err != nil && err != fs.SkipAll {
		return nil, err
	}
	var all []chunk
	for _, path := range paths {
		fileChunks, err := fileChunks(path)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(root, path)
		if err == nil {
			path = rel
		}
		pathLower := strings.ToLower(path)
		for _, item := range fileChunks {
			item.path = path
			item.score = scoreChunk(pathLower, item.text, queryTokens)
			if item.score > 0 {
				all = append(all, item)
			}
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].score != all[j].score {
			return all[i].score > all[j].score
		}
		if all[i].path != all[j].path {
			return all[i].path < all[j].path
		}
		return all[i].startLine < all[j].startLine
	})
	if len(all) > maxChunks*4 {
		all = all[:maxChunks*4]
	}
	return dedupeChunks(all), nil
}

func fileChunks(path string) ([]chunk, error) {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 || len(data) > maxFileBytes || isBinary(data) {
		return nil, err
	}
	text := string(data)
	if strings.HasSuffix(path, ".go") {
		if chunks := goFileChunks(path, text); len(chunks) > 0 {
			return chunks, nil
		}
	}
	return textChunks(text), nil
}

func goFileChunks(path, text string) []chunk {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, text, parser.ParseComments)
	if err != nil {
		return nil
	}
	lines := strings.Split(text, "\n")
	var out []chunk
	if len(file.Imports) > 0 {
		start := fset.Position(file.Imports[0].Pos()).Line
		end := fset.Position(file.Imports[len(file.Imports)-1].End()).Line
		out = append(out, sliceChunk(lines, start, end))
	}
	for _, decl := range file.Decls {
		start := fset.Position(decl.Pos()).Line
		end := fset.Position(decl.End()).Line
		if end <= start {
			end = start
		}
		if end-start > 120 {
			end = start + 120
		}
		switch d := decl.(type) {
		case *ast.FuncDecl:
			chunk := sliceChunk(lines, start, end)
			if d.Name != nil {
				chunk.text = "func " + d.Name.Name + "\n" + chunk.text
			}
			out = append(out, chunk)
		case *ast.GenDecl:
			chunk := sliceChunk(lines, start, end)
			if len(d.Specs) > 0 {
				if ts, ok := d.Specs[0].(*ast.TypeSpec); ok && ts.Name != nil {
					chunk.text = d.Tok.String() + " " + ts.Name.Name + "\n" + chunk.text
				}
			}
			out = append(out, chunk)
		}
	}
	return out
}

func textChunks(text string) []chunk {
	lines := strings.Split(text, "\n")
	if len(lines) == 0 {
		return nil
	}
	const size = 40
	const overlap = 8
	var out []chunk
	for start := 1; start <= len(lines); start += size - overlap {
		end := start + size - 1
		if end > len(lines) {
			end = len(lines)
		}
		out = append(out, sliceChunk(lines, start, end))
		if end == len(lines) {
			break
		}
	}
	return out
}

func sliceChunk(lines []string, start, end int) chunk {
	if start < 1 {
		start = 1
	}
	if end < start {
		end = start
	}
	if end > len(lines) {
		end = len(lines)
	}
	text := strings.Join(lines[start-1:end], "\n")
	return chunk{startLine: start, endLine: end, text: strings.TrimSpace(text)}
}

func scoreChunk(pathLower, text string, queryTokens []string) int {
	textLower := strings.ToLower(text)
	score := 0
	for _, token := range queryTokens {
		switch {
		case token == "":
		case strings.Contains(pathLower, token):
			score += 8
		case strings.Contains(textLower, token):
			score += 4
		case strings.Contains(strings.ReplaceAll(pathLower, "_", ""), strings.ReplaceAll(token, "_", "")):
			score += 3
		}
	}
	if strings.Contains(pathLower, filepath.Base(pathLower)) {
		score++
	}
	return score
}

func dedupeChunks(chunks []chunk) []chunk {
	seen := make(map[string]bool)
	out := make([]chunk, 0, maxChunks)
	for _, item := range chunks {
		key := item.path + fmt.Sprintf(":%d:%d", item.startLine, item.endLine)
		if seen[key] || strings.TrimSpace(item.text) == "" {
			continue
		}
		seen[key] = true
		out = append(out, item)
		if len(out) >= maxChunks {
			break
		}
	}
	return out
}

func formatContext(root string, chunks []chunk) (string, int, int, int) {
	var buf bytes.Buffer
	buf.WriteString("Retrieved workspace context:\n")
	usedFiles := make(map[string]bool)
	count := 0
	for _, item := range chunks {
		entry := fmt.Sprintf("\n--- %s:%d-%d ---\n%s\n", item.path, item.startLine, item.endLine, item.text)
		if buf.Len()+len(entry) > maxContextBytes && count > 0 {
			break
		}
		buf.WriteString(entry)
		usedFiles[item.path] = true
		count++
	}
	return strings.TrimSpace(buf.String()), count, len(usedFiles), buf.Len()
}

func tokenize(s string) []string {
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.' || r == '/')
	})
	seen := make(map[string]bool)
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.Trim(field, "._-/")
		if len(field) < 2 || seen[field] {
			continue
		}
		seen[field] = true
		out = append(out, field)
	}
	return out
}

func shouldSkipDir(name string) bool {
	switch name {
	case ".git", ".hg", ".svn", "node_modules", "dist", "build", "tmp", "vendor", ".next", "coverage":
		return true
	default:
		return strings.HasPrefix(name, ".cache")
	}
}

func shouldSkipFile(name string) bool {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".png"), strings.HasSuffix(lower, ".jpg"), strings.HasSuffix(lower, ".jpeg"), strings.HasSuffix(lower, ".gif"), strings.HasSuffix(lower, ".webp"), strings.HasSuffix(lower, ".pdf"), strings.HasSuffix(lower, ".zip"), strings.HasSuffix(lower, ".gz"), strings.HasSuffix(lower, ".tar"), strings.HasSuffix(lower, ".ico"), strings.HasSuffix(lower, ".woff"), strings.HasSuffix(lower, ".woff2"), strings.HasSuffix(lower, ".ttf"), strings.HasSuffix(lower, ".bin"):
		return true
	default:
		return false
	}
}

func isBinary(data []byte) bool {
	n := len(data)
	if n > 512 {
		n = 512
	}
	for _, b := range data[:n] {
		if b == 0 {
			return true
		}
	}
	return false
}

func normalizeSpaces(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}
