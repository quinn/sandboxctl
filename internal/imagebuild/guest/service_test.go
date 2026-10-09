package guest

import (
	"os/exec"
	"strings"
	"testing"
)

func TestImageBuilderServiceIsValidPOSIXShell(t *testing.T) {
	script, err := ImageBuilderService()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("sh", "-n")
	command.Stdin = strings.NewReader(string(script))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("image builder service has invalid shell syntax: %v\n%s", err, output)
	}
}

func TestImageBuilderServiceUsesFixedBuildAndFinalizationSteps(t *testing.T) {
	script, err := ImageBuilderService()
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"[ \"$#\" -ne 1 ] || [ \"$1\" != build ]",
		"--frontend dockerfile.v0",
		"--output \"type=local,dest=$rootfs\"",
		"chroot \"$rootfs\" /usr/sbin/sshd -T",
		"rm -f -- \"$rootfs/etc/machine-id\"",
		"rm -f -- \"$rootfs/var/lib/sandboxctl/bootstrap-complete\"",
		"SSH server or bootstrap service is not executable",
		"/usr/bin/mksquashfs",
		"-repro-time 0",
	} {
		if !strings.Contains(string(script), required) {
			t.Errorf("image builder service is missing required step %q", required)
		}
	}
	for _, forbidden := range []string{"unsquashfs", "mount -o loop", "mount -t overlay"} {
		if strings.Contains(string(script), forbidden) {
			t.Errorf("image builder service contains forbidden host extraction step %q", forbidden)
		}
	}
}
