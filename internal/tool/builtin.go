package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/zo-ll/oi/internal/workspace"
)

const defaultMaxOutputBytes = 64 * 1024

// Options configures built-in tools.
type Options struct {
	Policy         workspace.Policy
	MaxOutputBytes int
	PromptInput    io.Reader
	PromptOutput   io.Writer
}

// NewBuiltinRegistry returns the built-in oi toolset.
func NewBuiltinRegistry(opts Options) *Registry {
	return NewRegistry(
		readFileTool{opts: opts},
		listDirTool{opts: opts},
		findFilesTool{opts: opts},
		grepTool{opts: opts},
		runCommandTool{opts: opts},
		writeFileTool{opts: opts},
		replaceInFileTool{opts: opts},
	)
}

func (o Options) maxOutputBytes() int {
	if o.MaxOutputBytes <= 0 {
		return defaultMaxOutputBytes
	}
	return o.MaxOutputBytes
}

func (o Options) root() (string, error) {
	return workspace.DetectRoot(o.Policy.Root)
}

func (o Options) approve(action, target string) error {
	mode := o.Policy.ApprovalMode
	if mode == "" {
		mode = workspace.ApprovalPrompt
	}
	switch mode {
	case workspace.ApprovalAuto:
		return nil
	case workspace.ApprovalNever:
		return fmt.Errorf("approval mode forbids %s", action)
	case workspace.ApprovalPrompt:
		if o.PromptInput == nil || o.PromptOutput == nil {
			return fmt.Errorf("approval required for %s", action)
		}
		if _, err := fmt.Fprintf(o.PromptOutput, "Approve %s %s? [y/N] ", action, target); err != nil {
			return err
		}
		var answer string
		if _, err := fmt.Fscanln(o.PromptInput, &answer); err != nil && err != io.EOF {
			return err
		}
		answer = strings.ToLower(strings.TrimSpace(answer))
		if answer != "y" && answer != "yes" {
			return fmt.Errorf("approval denied for %s", action)
		}
		return nil
	default:
		return fmt.Errorf("unknown approval mode: %s", mode)
	}
}

func truncateOutput(s string, max int) (string, bool) {
	if max <= 0 || len(s) <= max {
		return s, false
	}
	const suffix = "\n... (truncated)"
	if max <= len(suffix) {
		return s[:max], true
	}
	return s[:max-len(suffix)] + suffix, true
}

func jsonError(toolName string, err error) Result {
	return Result{Tool: toolName, OK: false, Error: err.Error()}
}

func resolvePath(opts Options, path string) (string, string, error) {
	root, err := opts.root()
	if err != nil {
		return "", "", err
	}
	resolved, err := opts.Policy.ResolvePath(path)
	if err != nil {
		return "", "", err
	}
	return root, resolved, nil
}

func displayPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return rel
	}
	return path
}

func isBinary(data []byte) bool {
	n := len(data)
	if n > 512 {
		n = 512
	}
	if n == 0 {
		return false
	}
	nonPrintable := 0
	for _, b := range data[:n] {
		if b == 0 {
			return true
		}
		if b < 7 || (b > 14 && b < 32) || b == 127 {
			nonPrintable++
		}
	}
	return nonPrintable*10 > n*3
}

type readFileArgs struct {
	Path       string `json:"path"`
	OffsetLine int    `json:"offset_line,omitempty"`
	LimitLines int    `json:"limit_lines,omitempty"`
}

type readFileTool struct{ opts Options }

func (t readFileTool) Name() string { return "read_file" }
func (t readFileTool) Spec() Spec {
	return Spec{
		Name:        t.Name(),
		Description: "Read a text file inside the workspace.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"offset_line":{"type":"integer","minimum":1},"limit_lines":{"type":"integer","minimum":1}},"required":["path"]}`),
	}
}
func (t readFileTool) Run(_ context.Context, call Call) Result {
	var args readFileArgs
	if err := json.Unmarshal(call.Args, &args); err != nil {
		return jsonError(t.Name(), err)
	}
	if args.Path == "" {
		return jsonError(t.Name(), fmt.Errorf("path is required"))
	}
	root, path, err := resolvePath(t.opts, args.Path)
	if err != nil {
		return jsonError(t.Name(), err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return jsonError(t.Name(), err)
	}
	if info.IsDir() {
		return jsonError(t.Name(), fmt.Errorf("path is a directory"))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return jsonError(t.Name(), err)
	}
	if isBinary(data) {
		return jsonError(t.Name(), fmt.Errorf("file is binary"))
	}
	out := string(data)
	if args.OffsetLine > 0 || args.LimitLines > 0 {
		lines := strings.Split(out, "\n")
		start := 0
		if args.OffsetLine > 1 {
			start = args.OffsetLine - 1
			if start > len(lines) {
				start = len(lines)
			}
		}
		end := len(lines)
		if args.LimitLines > 0 && start+args.LimitLines < end {
			end = start + args.LimitLines
		}
		out = strings.Join(lines[start:end], "\n")
	}
	out, truncated := truncateOutput(out, t.opts.maxOutputBytes())
	meta := map[string]string{"path": displayPath(root, path)}
	if truncated {
		meta["truncated"] = "true"
	}
	return Result{Tool: t.Name(), OK: true, Output: out, Meta: meta}
}

type listDirArgs struct {
	Path string `json:"path,omitempty"`
}

type listDirTool struct{ opts Options }

func (t listDirTool) Name() string { return "list_dir" }
func (t listDirTool) Spec() Spec {
	return Spec{
		Name:        t.Name(),
		Description: "List files and directories at a path inside the workspace.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
	}
}
func (t listDirTool) Run(_ context.Context, call Call) Result {
	var args listDirArgs
	if err := json.Unmarshal(call.Args, &args); err != nil {
		return jsonError(t.Name(), err)
	}
	if args.Path == "" {
		args.Path = "."
	}
	root, path, err := resolvePath(t.opts, args.Path)
	if err != nil {
		return jsonError(t.Name(), err)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return jsonError(t.Name(), err)
	}
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			name += "/"
		}
		lines = append(lines, name)
	}
	sort.Strings(lines)
	out, truncated := truncateOutput(strings.Join(lines, "\n"), t.opts.maxOutputBytes())
	meta := map[string]string{"path": displayPath(root, path)}
	if truncated {
		meta["truncated"] = "true"
	}
	return Result{Tool: t.Name(), OK: true, Output: out, Meta: meta}
}

type findFilesArgs struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path,omitempty"`
}

type findFilesTool struct{ opts Options }

func (t findFilesTool) Name() string { return "find_files" }
func (t findFilesTool) Spec() Spec {
	return Spec{
		Name:        t.Name(),
		Description: "Find files recursively by glob or substring pattern.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string"},"path":{"type":"string"}},"required":["pattern"]}`),
	}
}
func (t findFilesTool) Run(_ context.Context, call Call) Result {
	var args findFilesArgs
	if err := json.Unmarshal(call.Args, &args); err != nil {
		return jsonError(t.Name(), err)
	}
	if args.Pattern == "" {
		return jsonError(t.Name(), fmt.Errorf("pattern is required"))
	}
	if args.Path == "" {
		args.Path = "."
	}
	root, base, err := resolvePath(t.opts, args.Path)
	if err != nil {
		return jsonError(t.Name(), err)
	}
	matches := []string{}
	walkErr := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(base, path)
		if relErr != nil {
			rel = filepath.Base(path)
		}
		if matchPattern(args.Pattern, filepath.Base(path), rel) {
			matches = append(matches, displayPath(root, path))
		}
		return nil
	})
	if walkErr != nil {
		return jsonError(t.Name(), walkErr)
	}
	sort.Strings(matches)
	out, truncated := truncateOutput(strings.Join(matches, "\n"), t.opts.maxOutputBytes())
	meta := map[string]string{"path": displayPath(root, base)}
	if truncated {
		meta["truncated"] = "true"
	}
	return Result{Tool: t.Name(), OK: true, Output: out, Meta: meta}
}

func matchPattern(pattern, name, rel string) bool {
	if ok, _ := filepath.Match(pattern, name); ok {
		return true
	}
	if ok, _ := filepath.Match(pattern, rel); ok {
		return true
	}
	return strings.Contains(name, pattern) || strings.Contains(rel, pattern)
}

type grepArgs struct {
	Pattern    string `json:"pattern"`
	Path       string `json:"path,omitempty"`
	MaxMatches int    `json:"max_matches,omitempty"`
}

type grepTool struct{ opts Options }

func (t grepTool) Name() string { return "grep" }
func (t grepTool) Spec() Spec {
	return Spec{
		Name:        t.Name(),
		Description: "Search recursively for a regular expression in text files.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string"},"path":{"type":"string"},"max_matches":{"type":"integer","minimum":1}},"required":["pattern"]}`),
	}
}
func (t grepTool) Run(_ context.Context, call Call) Result {
	var args grepArgs
	if err := json.Unmarshal(call.Args, &args); err != nil {
		return jsonError(t.Name(), err)
	}
	if args.Pattern == "" {
		return jsonError(t.Name(), fmt.Errorf("pattern is required"))
	}
	re, err := regexp.Compile(args.Pattern)
	if err != nil {
		return jsonError(t.Name(), err)
	}
	if args.Path == "" {
		args.Path = "."
	}
	if args.MaxMatches <= 0 {
		args.MaxMatches = 200
	}
	root, base, err := resolvePath(t.opts, args.Path)
	if err != nil {
		return jsonError(t.Name(), err)
	}
	var matches []string
	appendFileMatches := func(path string) error {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if isBinary(data) {
			return nil
		}
		for i, line := range strings.Split(string(data), "\n") {
			if re.MatchString(line) {
				matches = append(matches, fmt.Sprintf("%s:%d:%s", displayPath(root, path), i+1, line))
				if len(matches) >= args.MaxMatches {
					return errMaxMatches
				}
			}
		}
		return nil
	}
	info, err := os.Stat(base)
	if err != nil {
		return jsonError(t.Name(), err)
	}
	if info.IsDir() {
		err = filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			return appendFileMatches(path)
		})
	} else {
		err = appendFileMatches(base)
	}
	if err != nil && err != errMaxMatches {
		return jsonError(t.Name(), err)
	}
	out, truncated := truncateOutput(strings.Join(matches, "\n"), t.opts.maxOutputBytes())
	meta := map[string]string{"path": displayPath(root, base)}
	if err == errMaxMatches {
		meta["limited"] = "true"
	}
	if truncated {
		meta["truncated"] = "true"
	}
	return Result{Tool: t.Name(), OK: true, Output: out, Meta: meta}
}

var errMaxMatches = fmt.Errorf("max matches reached")

type runCommandArgs struct {
	Command string `json:"command"`
}

type runCommandTool struct{ opts Options }

func (t runCommandTool) Name() string { return "run_command" }
func (t runCommandTool) Spec() Spec {
	return Spec{
		Name:        t.Name(),
		Description: "Run a shell command in the workspace root, subject to policy checks.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}`),
	}
}
func (t runCommandTool) Run(ctx context.Context, call Call) Result {
	var args runCommandArgs
	if err := json.Unmarshal(call.Args, &args); err != nil {
		return jsonError(t.Name(), err)
	}
	if args.Command == "" {
		return jsonError(t.Name(), fmt.Errorf("command is required"))
	}
	if err := workspace.CheckCommand(args.Command); err != nil {
		return jsonError(t.Name(), err)
	}
	root, err := t.opts.root()
	if err != nil {
		return jsonError(t.Name(), err)
	}
	if err := t.opts.approve("run command", args.Command); err != nil {
		return jsonError(t.Name(), err)
	}
	cmd := exec.CommandContext(ctx, "sh", "-lc", args.Command)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	output, truncated := truncateOutput(string(out), t.opts.maxOutputBytes())
	meta := map[string]string{"cwd": root}
	if truncated {
		meta["truncated"] = "true"
	}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			meta["exit_code"] = fmt.Sprintf("%d", exitErr.ExitCode())
		}
		return Result{Tool: t.Name(), OK: false, Output: output, Error: err.Error(), Meta: meta}
	}
	return Result{Tool: t.Name(), OK: true, Output: output, Meta: meta}
}

type writeFileArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type writeFileTool struct{ opts Options }

func (t writeFileTool) Name() string { return "write_file" }
func (t writeFileTool) Spec() Spec {
	return Spec{
		Name:        t.Name(),
		Description: "Write a file inside the workspace, creating parent directories if needed.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`),
	}
}
func (t writeFileTool) Run(_ context.Context, call Call) Result {
	var args writeFileArgs
	if err := json.Unmarshal(call.Args, &args); err != nil {
		return jsonError(t.Name(), err)
	}
	if args.Path == "" {
		return jsonError(t.Name(), fmt.Errorf("path is required"))
	}
	root, path, err := resolvePath(t.opts, args.Path)
	if err != nil {
		return jsonError(t.Name(), err)
	}
	if err := t.opts.approve("write file", displayPath(root, path)); err != nil {
		return jsonError(t.Name(), err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return jsonError(t.Name(), err)
	}
	mode := fs.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.WriteFile(path, []byte(args.Content), mode); err != nil {
		return jsonError(t.Name(), err)
	}
	return Result{Tool: t.Name(), OK: true, Output: fmt.Sprintf("wrote %d bytes to %s", len(args.Content), displayPath(root, path))}
}

type replaceInFileArgs struct {
	Path    string `json:"path"`
	OldText string `json:"old_text"`
	NewText string `json:"new_text"`
}

type replaceInFileTool struct{ opts Options }

func (t replaceInFileTool) Name() string { return "replace_in_file" }
func (t replaceInFileTool) Spec() Spec {
	return Spec{
		Name:        t.Name(),
		Description: "Replace exactly one matching text block inside a file.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"old_text":{"type":"string"},"new_text":{"type":"string"}},"required":["path","old_text","new_text"]}`),
	}
}
func (t replaceInFileTool) Run(_ context.Context, call Call) Result {
	var args replaceInFileArgs
	if err := json.Unmarshal(call.Args, &args); err != nil {
		return jsonError(t.Name(), err)
	}
	if args.Path == "" {
		return jsonError(t.Name(), fmt.Errorf("path is required"))
	}
	root, path, err := resolvePath(t.opts, args.Path)
	if err != nil {
		return jsonError(t.Name(), err)
	}
	if err := t.opts.approve("replace in file", displayPath(root, path)); err != nil {
		return jsonError(t.Name(), err)
	}
	if err := ReplaceFile(path, args.OldText, args.NewText); err != nil {
		return jsonError(t.Name(), err)
	}
	return Result{Tool: t.Name(), OK: true, Output: fmt.Sprintf("updated %s", displayPath(root, path))}
}
