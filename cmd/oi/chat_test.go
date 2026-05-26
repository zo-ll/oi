package main

import (
	"path/filepath"
	"testing"
)

func TestResolveSessionPath(t *testing.T) {
	dir := "/tmp/sessions"
	if got := resolveSessionPath(dir, "abc"); got != filepath.Join(dir, "abc.json") {
		t.Fatalf("got %q", got)
	}
	if got := resolveSessionPath(dir, "abc.json"); got != filepath.Join(dir, "abc.json") {
		t.Fatalf("got %q", got)
	}
}

func TestValidateSessionName(t *testing.T) {
	if err := validateSessionName("good-name"); err != nil {
		t.Fatal(err)
	}
	if err := validateSessionName(""); err == nil {
		t.Fatal("expected error for empty name")
	}
	if err := validateSessionName("bad/name"); err == nil {
		t.Fatal("expected error for path separator")
	}
}
