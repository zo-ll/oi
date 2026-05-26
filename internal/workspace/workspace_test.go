package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePathInsideRoot(t *testing.T) {
	root := t.TempDir()
	p := Policy{Root: root}
	got, err := p.ResolvePath("dir/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "dir", "file.txt")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestResolvePathRejectsEscape(t *testing.T) {
	root := t.TempDir()
	p := Policy{Root: root}
	if _, err := p.ResolvePath("../escape.txt"); err == nil {
		t.Fatal("expected escape rejection")
	}
}

func TestResolvePathRejectsSymlinkEscape(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	p := Policy{Root: root}
	if _, err := p.ResolvePath("link/file.txt"); err == nil {
		t.Fatal("expected symlink escape rejection")
	}
}

func TestCheckCommandBlocksDangerousPatterns(t *testing.T) {
	if err := CheckCommand("rm -rf /"); err == nil {
		t.Fatal("expected blocked command")
	}
	if err := CheckCommand("git status"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
