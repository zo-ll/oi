package workspace

import "testing"

func TestCheckCommandRejectsPrivilegeEscalationAndDiskOps(t *testing.T) {
	for _, cmd := range []string{
		"sudo ls",
		"doas ls",
		"dd if=/dev/zero of=/dev/sda",
		"mkfs.ext4 /dev/sda",
		"rm -r -f /",
		"rm -rf -- /",
		"rm -rf '/'",
		"rm -rf $HOME",
	} {
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
		`go list ./... 2>&1 | tail -40`,
		`ls -la ../tide 2>/dev/null; echo "---"; find / -type d -name tide 2>/dev/null | head -20`,
		`git status --short`,
		`cat go.mod && echo "---" && ls cmd/ internal/`,
		`cat /etc/passwd`,
		`cat ../secret`,
		`find / -name '*.pem'`,
		`printf hello`,
	} {
		if !IsReadOnlyCommand(cmd) {
			t.Fatalf("expected read-only: %s", cmd)
		}
	}
	for _, cmd := range []string{
		`echo hi > file`,
		`rm file`,
		"cat go.mod\nrm file",
		`cat $HOME/.ssh/id_rsa`,
		`cat ~/.ssh/id_rsa`,
		`cat "$(rm file)"`,
		"cat `rm file`",
		`find . -exec rm {} ;`,
		`find . -delete`,
		`env`,
		`env rm file`,
		`sed -i s/a/b/ file`,
		`awk 'BEGIN { system("rm file") }'`,
		`go test ./...`,
		`go mod tidy`,
		`git checkout main`,
	} {
		if IsReadOnlyCommand(cmd) {
			t.Fatalf("expected mutating: %s", cmd)
		}
	}
}
