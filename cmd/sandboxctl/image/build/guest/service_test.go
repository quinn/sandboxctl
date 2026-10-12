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
		"[ \"$#\" -ne 1 ] || { [ \"$1\" != build-base ] && [ \"$1\" != build-dockerfile ]; }",
		"context archive digest does not match build settings",
		"--no-same-owner --no-same-permissions --no-overwrite-dir",
		"--opt \"target=$selected_target\"",
		"build-arg:$arg_name=$arg_value",
		"--frontend dockerfile.v0",
		"--source-policy-file \"$provision_context/source-policy.json\"",
		"--output \"type=oci,dest=$user_oci_tar\"",
		"--oci-layout \"sandboxctl-user-image=$user_oci\"",
		"oci-layout://sandboxctl-user-image@$user_digest",
		"applying sandboxctl guest provisioning",
		"--output \"type=local,dest=$rootfs\"",
		"BuildKit user image build failed; last 80 log lines follow",
		"guest provisioning build failed; last 80 log lines follow",
		"mksquashfs failed; first 60 log lines follow",
		"chroot \"$rootfs\" /usr/sbin/sshd -T",
		"rm -f -- \"$rootfs/etc/machine-id\"",
		"rm -f -- \"$rootfs/var/lib/sandboxctl/bootstrap-complete\"",
		"SSH server is not executable",
		"/usr/bin/mksquashfs",
		"-all-time 0",
		"chmod 0644 \"$output/root.squashfs\"",
		"chmod 0711 \"$output\"",
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
