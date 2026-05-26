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
