// Package tool (continued) — in-place string replacement helper used by
// write_file when it detects a replace_string call pattern.
package tool

import (
	"fmt"
	"os"
	"strings"
)

// ReplaceString replaces exactly one occurrence of oldText.
func ReplaceString(content, oldText, newText string) (string, error) {
	if oldText == "" {
		return "", fmt.Errorf("old text must not be empty")
	}
	count := strings.Count(content, oldText)
	switch {
	case count == 0:
		return "", fmt.Errorf("target text not found")
	case count > 1:
		return "", fmt.Errorf("target text is not unique")
	default:
		return strings.Replace(content, oldText, newText, 1), nil
	}
}

// ReplaceFile applies ReplaceString to a file while preserving file mode.
func ReplaceFile(path, oldText, newText string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	updated, err := ReplaceString(string(data), oldText, newText)
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(updated), info.Mode().Perm())
}
