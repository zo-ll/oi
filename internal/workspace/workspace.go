package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ApprovalMode controls mutation policy.
type ApprovalMode string

const (
	ApprovalPrompt ApprovalMode = "prompt"
	ApprovalAuto   ApprovalMode = "auto"
	ApprovalNever  ApprovalMode = "never"
)

// Policy defines workspace access policy.
type Policy struct {
	Root             string
	ApprovalMode     ApprovalMode
	AllowOutsideRoot bool
}

// DetectRoot resolves the current workspace root.
func DetectRoot(cwd string) (string, error) {
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		return resolved, nil
	}
	return abs, nil
}

// ResolvePath resolves a user path against the workspace policy.
func (p Policy) ResolvePath(path string) (string, error) {
	root, err := DetectRoot(p.Root)
	if err != nil {
		return "", err
	}
	var target string
	if filepath.IsAbs(path) {
		target = filepath.Clean(path)
	} else {
		target = filepath.Join(root, path)
	}
	resolved, err := evalPathAllowMissing(target)
	if err != nil {
		return "", err
	}
	if p.AllowOutsideRoot {
		return resolved, nil
	}
	if !within(root, resolved) {
		return "", fmt.Errorf("path escapes workspace root")
	}
	return resolved, nil
}

// CheckCommand blocks obviously dangerous shell patterns before any approval
// prompt. It is a last-resort guard; auto-approval uses IsReadOnlyCommand's
// allowlist instead of this blocklist.
func CheckCommand(cmd string) error {
	trimmed := strings.TrimSpace(cmd)
	if trimmed == "" {
		return fmt.Errorf("empty command")
	}
	for _, segment := range splitShellSegments(trimmed) {
		fields := strings.Fields(segment)
		if len(fields) == 0 {
			continue
		}
		name := filepath.Base(fields[0])
		if strings.HasPrefix(name, "mkfs") {
			return fmt.Errorf("blocked command: %s", name)
		}
		switch name {
		case "sudo", "doas", "fdisk", "mount", "umount":
			return fmt.Errorf("blocked command: %s", name)
		case "rm":
			if rmForceRecursiveRoot(fields[1:]) {
				return fmt.Errorf("blocked command: rm recursive force root")
			}
		case "chmod":
			if hasFlag(fields[1:], "R") && hasArg(fields[1:], "777") && hasRootTarget(fields[1:]) {
				return fmt.Errorf("blocked command: chmod recursive root")
			}
		case "chown":
			if hasFlag(fields[1:], "R") && hasRootTarget(fields[1:]) {
				return fmt.Errorf("blocked command: chown recursive root")
			}
		case "dd":
			if hasArgPrefix(fields[1:], "of=/dev/") || hasArgPrefix(fields[1:], "if=/dev/zero") {
				return fmt.Errorf("blocked command: dd device write")
			}
		}
	}
	blocked := []string{
		":(){:|:&};:",
	}
	for _, frag := range blocked {
		if strings.Contains(trimmed, frag) {
			return fmt.Errorf("blocked command pattern: %s", frag)
		}
	}
	return nil
}

func IsReadOnlyCommand(cmd string) bool {
	trimmed := strings.TrimSpace(cmd)
	trimmed = strings.ReplaceAll(trimmed, "2>&1", "")
	trimmed = strings.ReplaceAll(trimmed, "1>&2", "")
	if trimmed == "" || strings.Contains(trimmed, ">") || strings.Contains(trimmed, "<") || hasShellExpansion(trimmed) {
		return false
	}
	for _, segment := range splitShellSegments(trimmed) {
		fields := strings.Fields(segment)
		if len(fields) == 0 {
			continue
		}
		name := filepath.Base(fields[0])
		if name == "xargs" {
			name = xargsCommandName(fields[1:])
			if name == "" {
				return false
			}
		}
		switch name {
		case "cat", "cut", "diff", "du", "echo", "file", "find", "git", "go", "grep", "head", "ls", "printf", "pwd", "rg", "sort", "stat", "tail", "tree", "uname", "wc", "which":
		default:
			return false
		}
		if name == "go" && len(fields) > 1 {
			switch fields[1] {
			case "env", "list", "version":
			default:
				return false
			}
		}
		if name == "git" && len(fields) > 1 {
			switch fields[1] {
			case "branch", "diff", "log", "ls-files", "remote", "rev-parse", "show", "status":
			default:
				return false
			}
		}
		if !readOnlyArgs(name, fields[1:]) {
			return false
		}
	}
	return true
}

func hasShellExpansion(cmd string) bool {
	return strings.ContainsAny(cmd, "`$~")
}

func readOnlyArgs(name string, args []string) bool {
	for _, arg := range args {
		if unsafeReadOnlyArg(arg) {
			return false
		}
	}
	switch name {
	case "find":
		for _, arg := range args {
			switch arg {
			case "-delete", "-exec", "-execdir", "-ok", "-okdir":
				return false
			}
		}
	}
	return true
}

func unsafeReadOnlyArg(arg string) bool {
	arg = strings.Trim(arg, `"'`)
	if arg == "" {
		return false
	}
	if filepath.IsAbs(arg) || strings.HasPrefix(arg, "~") || strings.Contains(arg, "$") {
		return true
	}
	for _, part := range strings.FieldsFunc(arg, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return true
		}
	}
	return false
}

func rmForceRecursiveRoot(args []string) bool {
	force := false
	recursive := false
	for _, arg := range args {
		if arg == "--" {
			continue
		}
		if strings.HasPrefix(arg, "-") {
			if strings.Contains(arg, "f") {
				force = true
			}
			if strings.Contains(arg, "r") || strings.Contains(arg, "R") {
				recursive = true
			}
			continue
		}
		if force && recursive && rootLikeTarget(arg) {
			return true
		}
	}
	return false
}

func rootLikeTarget(arg string) bool {
	arg = strings.Trim(arg, `"'`)
	return arg == "/" || arg == "/*" || arg == "$HOME" || arg == "~"
}

func hasFlag(args []string, flag string) bool {
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") && strings.Contains(arg, flag) {
			return true
		}
	}
	return false
}

func hasArg(args []string, want string) bool {
	for _, arg := range args {
		if strings.Trim(arg, `"'`) == want {
			return true
		}
	}
	return false
}

func hasRootTarget(args []string) bool {
	for _, arg := range args {
		if rootLikeTarget(arg) {
			return true
		}
	}
	return false
}

func hasArgPrefix(args []string, prefix string) bool {
	for _, arg := range args {
		if strings.HasPrefix(strings.Trim(arg, `"'`), prefix) {
			return true
		}
	}
	return false
}

func xargsCommandName(args []string) string {
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return filepath.Base(arg)
	}
	return ""
}

func splitShellSegments(cmd string) []string {
	parts := strings.FieldsFunc(cmd, func(r rune) bool {
		return r == '|' || r == ';' || r == '&' || r == '\n' || r == '\r'
	})
	out := parts[:0]
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func within(root, target string) bool {
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	if root == target {
		return true
	}
	prefix := root + string(os.PathSeparator)
	return strings.HasPrefix(target, prefix)
}

func evalPathAllowMissing(path string) (string, error) {
	path = filepath.Clean(path)
	parts := []string{}
	cur := path
	for {
		if _, err := os.Lstat(cur); err == nil {
			resolved, err := filepath.EvalSymlinks(cur)
			if err != nil {
				return "", err
			}
			for i := len(parts) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, parts[i])
			}
			return filepath.Clean(resolved), nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			abs, err := filepath.Abs(path)
			if err != nil {
				return "", err
			}
			return abs, nil
		}
		parts = append(parts, filepath.Base(cur))
		cur = parent
	}
}
