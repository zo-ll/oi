package session

import (
	"path/filepath"
	"testing"
	"time"
)

func TestListSessionsSortedByUpdatedAt(t *testing.T) {
	dir := t.TempDir()
	first := &Session{ID: "first", Provider: "p1", Model: "m1", CreatedAt: time.Now().UTC().Add(-2 * time.Hour), UpdatedAt: time.Now().UTC().Add(-time.Hour)}
	second := &Session{ID: "second", Provider: "p2", Model: "m2", CreatedAt: time.Now().UTC().Add(-time.Hour), UpdatedAt: time.Now().UTC()}
	if _, err := Save(dir, first); err != nil {
		t.Fatal(err)
	}
	if _, err := Save(dir, second); err != nil {
		t.Fatal(err)
	}
	items, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("len = %d", len(items))
	}
	if items[0].ID != "second" || items[1].ID != "first" {
		t.Fatalf("order = %+v", items)
	}
	if items[0].Path != filepath.Join(dir, "second.json") {
		t.Fatalf("path = %q", items[0].Path)
	}
}
