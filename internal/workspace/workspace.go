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

// CheckCommand blocks obviously dangerous shell patterns.
func CheckCommand(cmd string) error {
	trimmed := strings.TrimSpace(cmd)
	if trimmed == "" {
		return fmt.Errorf("empty command")
	}
	blocked := []string{
		"rm -rf /",
		"mkfs",
		"fdisk",
		"mount ",
		"umount ",
		"sudo ",
		"doas ",
		"chmod -R 777 /",
		"chown -R /",
		"dd if=",
		":(){:|:&};:",
	}
	for _, frag := range blocked {
		if strings.Contains(trimmed, frag) {
			return fmt.Errorf("blocked command pattern: %s", frag)
		}
	}
	return nil
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
