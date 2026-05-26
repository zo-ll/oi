package tool

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReplaceString(t *testing.T) {
	got, err := ReplaceString("hello world", "world", "oi")
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello oi" {
		t.Fatalf("got %q", got)
	}
}

func TestReplaceStringRequiresUniqueMatch(t *testing.T) {
	if _, err := ReplaceString("x x", "x", "y"); err == nil {
		t.Fatal("expected uniqueness error")
	}
}

func TestReplaceFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("alpha beta gamma"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceFile(path, "beta", "oi"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "alpha oi gamma" {
		t.Fatalf("got %q", string(data))
	}
}
