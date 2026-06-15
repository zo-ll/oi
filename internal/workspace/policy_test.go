package workspace

import "testing"

func TestCheckCommandRejectsPrivilegeEscalationAndDiskOps(t *testing.T) {
	for _, cmd := range []string{"sudo ls", "doas ls", "dd if=/dev/zero of=/tmp/x", "mkfs.ext4 /dev/sda"} {
		if err := CheckCommand(cmd); err == nil {
			t.Fatalf("expected block for %q", cmd)
		}
	}
}

func TestCheckCommandAllowsSimpleReadOnlyCommand(t *testing.T) {
	if err := CheckCommand("printf hello"); err != nil {
		t.Fatal(err)
	}
}

func TestIsReadOnlyCommand(t *testing.T) {
	for _, cmd := range []string{
		`pwd && ls -la`,
		`find . -name "*.go" | xargs wc -l | tail -1`,
		`go test ./... 2>&1 | tail -40`,
		`git status --short`,
		`cat go.mod && echo "---" && ls cmd/ internal/`,
	} {
		if !IsReadOnlyCommand(cmd) {
			t.Fatalf("expected read-only: %s", cmd)
		}
	}
	for _, cmd := range []string{
		`echo hi > file`,
		`rm file`,
		`go mod tidy`,
		`git checkout main`,
	} {
		if IsReadOnlyCommand(cmd) {
			t.Fatalf("expected mutating: %s", cmd)
		}
	}
}
