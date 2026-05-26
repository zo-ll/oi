package main

import (
	"path/filepath"
	"testing"

	"github.com/zo-ll/oi/internal/agent"
	"github.com/zo-ll/oi/internal/config"
	"github.com/zo-ll/oi/internal/session"
	"github.com/zo-ll/oi/internal/workspace"
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

func TestSaveSessionNamedDoesNotMutateRollingSessionID(t *testing.T) {
	rt := &agent.Runtime{
		Policy: workspace.Policy{Root: t.TempDir()},
		Session: &session.Session{
			ID:       "rolling",
			Provider: "p",
			Model:    "m",
		},
	}
	if _, err := saveSessionNamed(rt, config.Selection{Provider: "p", Model: "m"}, "snapshot"); err != nil {
		t.Fatal(err)
	}
	if rt.Session.ID != "rolling" {
		t.Fatalf("session id mutated: %q", rt.Session.ID)
	}
}
