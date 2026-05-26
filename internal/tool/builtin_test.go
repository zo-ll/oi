package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zo-ll/oi/internal/workspace"
)

func TestBuiltinRegistryHasExpectedTools(t *testing.T) {
	r := NewBuiltinRegistry(Options{})
	names := []string{}
	for _, spec := range r.Specs() {
		names = append(names, spec.Name)
	}
	want := []string{"find_files", "grep", "list_dir", "read_file", "replace_in_file", "run_command", "write_file"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v want %v", names, want)
	}
}

func TestReadFileTool(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := readFileTool{opts: Options{Policy: workspace.Policy{Root: root}}}
	args, _ := json.Marshal(readFileArgs{Path: "a.txt", OffsetLine: 2, LimitLines: 1})
	res := tool.Run(context.Background(), Call{Name: tool.Name(), Args: args})
	if !res.OK || res.Output != "two" {
		t.Fatalf("result = %+v", res)
	}
}

func TestListDirTool(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := listDirTool{opts: Options{Policy: workspace.Policy{Root: root}}}
	args, _ := json.Marshal(listDirArgs{Path: "."})
	res := tool.Run(context.Background(), Call{Name: tool.Name(), Args: args})
	if !res.OK || !strings.Contains(res.Output, "dir/") || !strings.Contains(res.Output, "file.txt") {
		t.Fatalf("result = %+v", res)
	}
}

func TestFindFilesTool(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"one.go", "two.txt", "nested/three.go"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tool := findFilesTool{opts: Options{Policy: workspace.Policy{Root: root}}}
	args, _ := json.Marshal(findFilesArgs{Pattern: "*.go", Path: "."})
	res := tool.Run(context.Background(), Call{Name: tool.Name(), Args: args})
	if !res.OK || !strings.Contains(res.Output, "one.go") || !strings.Contains(res.Output, filepath.Join("nested", "three.go")) {
		t.Fatalf("result = %+v", res)
	}
}

func TestGrepTool(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta auth\ngamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := grepTool{opts: Options{Policy: workspace.Policy{Root: root}}}
	args, _ := json.Marshal(grepArgs{Pattern: "auth", Path: "."})
	res := tool.Run(context.Background(), Call{Name: tool.Name(), Args: args})
	if !res.OK || !strings.Contains(res.Output, "a.txt:2:beta auth") {
		t.Fatalf("result = %+v", res)
	}
}

func TestRunCommandToolApprovalAndExecution(t *testing.T) {
	root := t.TempDir()
	tool := runCommandTool{opts: Options{Policy: workspace.Policy{Root: root, ApprovalMode: workspace.ApprovalAuto}}}
	args, _ := json.Marshal(runCommandArgs{Command: "printf hello"})
	res := tool.Run(context.Background(), Call{Name: tool.Name(), Args: args})
	if !res.OK || res.Output != "hello" {
		t.Fatalf("result = %+v", res)
	}
}

func TestWriteAndReplaceTools(t *testing.T) {
	root := t.TempDir()
	opts := Options{Policy: workspace.Policy{Root: root, ApprovalMode: workspace.ApprovalAuto}}
	writeArgs, _ := json.Marshal(writeFileArgs{Path: "notes.txt", Content: "hello world"})
	writeRes := writeFileTool{opts: opts}.Run(context.Background(), Call{Name: "write_file", Args: writeArgs})
	if !writeRes.OK {
		t.Fatalf("write result = %+v", writeRes)
	}
	replaceArgs, _ := json.Marshal(replaceInFileArgs{Path: "notes.txt", OldText: "world", NewText: "oi"})
	replaceRes := replaceInFileTool{opts: opts}.Run(context.Background(), Call{Name: "replace_in_file", Args: replaceArgs})
	if !replaceRes.OK {
		t.Fatalf("replace result = %+v", replaceRes)
	}
	data, err := os.ReadFile(filepath.Join(root, "notes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello oi" {
		t.Fatalf("content = %q", string(data))
	}
}

func TestWriteFileToolDeniedByApprovalMode(t *testing.T) {
	root := t.TempDir()
	tool := writeFileTool{opts: Options{Policy: workspace.Policy{Root: root, ApprovalMode: workspace.ApprovalNever}}}
	args, _ := json.Marshal(writeFileArgs{Path: "x.txt", Content: "no"})
	res := tool.Run(context.Background(), Call{Name: tool.Name(), Args: args})
	if res.OK || !strings.Contains(res.Error, "forbids") {
		t.Fatalf("result = %+v", res)
	}
}
