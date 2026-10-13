package guest

import (
	"archive/tar"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
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
		"--output \"type=tar,dest=$rootfs_tar\"",
		"--extract --numeric-owner --same-owner --same-permissions --directory \"$rootfs\" --file \"$rootfs_tar\"",
		"provisioned filesystem extraction failed",
		"dev_uid=$(chroot \"$rootfs\" /usr/bin/id -u dev)",
		"guest home or SSH directory ownership is invalid",
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
	for _, forbidden := range []string{"type=local,dest=$rootfs", "unsquashfs", "mount -o loop", "mount -t overlay"} {
		if strings.Contains(string(script), forbidden) {
			t.Errorf("image builder service contains forbidden host extraction step %q", forbidden)
		}
	}
}

func TestImageBuilderRootfsExtractionPreservesMetadata(t *testing.T) {
	script, err := ImageBuilderService()
	if err != nil {
		t.Fatal(err)
	}
	var extraction string
	for _, line := range strings.Split(string(script), "\n") {
		if strings.HasPrefix(line, "if ! /usr/bin/tar --extract ") {
			extraction = strings.TrimSuffix(strings.TrimPrefix(line, "if ! "), "; then")
			break
		}
	}
	if extraction == "" {
		t.Fatal("root filesystem extraction command is missing")
	}
	uid, gid := os.Getuid(), os.Getgid()
	if uid == 0 {
		uid, gid = 1001, 1001
	}
	root := t.TempDir()
	archivePath := filepath.Join(root, "rootfs.tar")
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	writer := tar.NewWriter(file)
	for _, header := range []*tar.Header{
		{Name: "home/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "home/dev/", Typeflag: tar.TypeDir, Mode: 0o750},
		{Name: "home/dev/.codex/", Typeflag: tar.TypeDir, Mode: 0o700},
		{Name: "home/dev/.codex/AGENTS.md", Typeflag: tar.TypeReg, Mode: 0o600, Size: 5},
		{Name: "home/dev/probe", Typeflag: tar.TypeReg, Mode: 0o755, Size: 5},
		{Name: "home/dev/rules", Typeflag: tar.TypeSymlink, Mode: 0o777, Linkname: ".codex/AGENTS.md"},
		{Name: "home/dev/rules-copy", Typeflag: tar.TypeLink, Mode: 0o600, Linkname: "home/dev/.codex/AGENTS.md"},
	} {
		// Archive names must not override the guest's numeric UID and GID.
		header.Uid, header.Gid, header.Uname, header.Gname = uid, gid, "root", "root"
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Size > 0 {
			if _, err := writer.Write([]byte("rules")); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	rootfs := filepath.Join(root, "rootfs")
	if err := os.Mkdir(rootfs, 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("sh", "-eu", "-c", extraction)
	command.Env = append(os.Environ(), "rootfs="+rootfs, "rootfs_tar="+archivePath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("extract provisioned filesystem: %v: %s", err, output)
	}
	for path, mode := range map[string]os.FileMode{
		"home/dev":                  0o750,
		"home/dev/.codex":           0o700,
		"home/dev/.codex/AGENTS.md": 0o600,
		"home/dev/probe":            0o755,
		"home/dev/rules-copy":       0o600,
	} {
		info, err := os.Stat(filepath.Join(rootfs, path))
		if err != nil {
			t.Fatal(err)
		}
		stat := info.Sys().(*syscall.Stat_t)
		if int(stat.Uid) != uid || int(stat.Gid) != gid || info.Mode().Perm() != mode {
			t.Errorf("%s: owner %d:%d, mode %o; want %d:%d, %o", path, stat.Uid, stat.Gid, info.Mode().Perm(), uid, gid, mode)
		}
	}
	if target, err := os.Readlink(filepath.Join(rootfs, "home/dev/rules")); err != nil || target != ".codex/AGENTS.md" {
		t.Fatalf("symbolic link = %q, %v", target, err)
	}
	info, err := os.Stat(filepath.Join(rootfs, "home/dev/.codex/AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	copyInfo, err := os.Stat(filepath.Join(rootfs, "home/dev/rules-copy"))
	if err != nil || !os.SameFile(info, copyInfo) {
		t.Fatalf("hard link was not preserved: %v", err)
	}
}
