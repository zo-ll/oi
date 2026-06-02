package retrieval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShouldUse(t *testing.T) {
	if ShouldUse("hello there") {
		t.Fatal("unexpected retrieval for chit-chat")
	}
	if !ShouldUse("where is RunOnce implemented in runtime.go") {
		t.Fatal("expected retrieval for code question")
	}
}

func TestBuildContextFindsRelevantGoSnippet(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "service.go")
	content := "package demo\n\nfunc ValidateSessionToken(raw string) bool {\n\treturn raw != \"\"\n}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, notice, err := BuildContext(root, "where is ValidateSessionToken implemented", nil)
	if err != nil {
		t.Fatal(err)
	}
	if notice.SnippetCount == 0 || notice.FileCount != 1 {
		t.Fatalf("notice = %+v", notice)
	}
	if !strings.Contains(ctx, "service.go") || !strings.Contains(ctx, "ValidateSessionToken") {
		t.Fatalf("ctx = %q", ctx)
	}
}

func TestBuildContextSkipsWhenHeuristicDoesNotMatch(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, notice, err := BuildContext(root, "hello there", nil)
	if err != nil {
		t.Fatal(err)
	}
	if ctx != "" || !notice.Skipped {
		t.Fatalf("ctx=%q notice=%+v", ctx, notice)
	}
}

func TestBuildContextBoostsRecentPaths(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "recent.go"), []byte("package demo\n\nfunc HelperThing() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "other.go"), []byte("package demo\n\nfunc HelperThing() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, notice, err := BuildContext(root, "where is HelperThing implemented", []string{"recent.go"})
	if err != nil {
		t.Fatal(err)
	}
	if notice.SnippetCount == 0 || !strings.Contains(ctx, "recent.go") {
		t.Fatalf("ctx=%q notice=%+v", ctx, notice)
	}
}
