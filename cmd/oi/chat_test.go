package main

import (
	"path/filepath"
	"testing"
	"time"

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

func TestResolveSessionArgByIndexAndFilter(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()
	for _, s := range []*session.Session{
		{ID: "alpha", Provider: "p1", Model: "m1", CreatedAt: now.Add(-2 * time.Hour), UpdatedAt: now.Add(-time.Hour)},
		{ID: "beta", Provider: "p2", Model: "deepseek", CreatedAt: now.Add(-time.Hour), UpdatedAt: now},
	} {
		if _, err := session.Save(dir, s); err != nil {
			t.Fatal(err)
		}
	}
	infos, err := filteredSessions(dir, "deep")
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || infos[0].ID != "beta" {
		t.Fatalf("infos = %+v", infos)
	}
	all, err := filteredSessions(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	path, err := resolveSessionArg(dir, all, "1")
	if err != nil {
		t.Fatal(err)
	}
	if path != all[0].Path {
		t.Fatalf("path = %q want %q", path, all[0].Path)
	}
}
