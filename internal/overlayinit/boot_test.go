package overlayinit

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/vishvananda/netlink"
)

func TestOverlayMountsUseReadOnlySquashFSAndWritableExt4(t *testing.T) {
	mounts := OverlayMounts()
	if len(mounts) != 3 {
		t.Fatalf("got %d mounts, want 3", len(mounts))
	}
	base := mounts[0]
	if base.Source != "/dev/vda" || base.Target != "/lower" || base.Type != "squashfs" || base.Flags&syscall.MS_RDONLY == 0 {
		t.Fatalf("invalid lower mount: %#v", base)
	}
	state := mounts[1]
	if state.Source != "/dev/vdb" || state.Target != "/state" || state.Type != "ext4" || state.Flags&syscall.MS_RDONLY != 0 {
		t.Fatalf("invalid state mount: %#v", state)
	}
	overlay := mounts[2]
	if overlay.Source != "overlay" || overlay.Target != "/newroot" || overlay.Type != "overlay" || overlay.Data != "lowerdir=/lower,upperdir=/state/upper,workdir=/state/work" {
		t.Fatalf("invalid overlay mount: %#v", overlay)
	}
}

func TestMMDSRouteTargetsLinkLocalAddressOnGuestInterface(t *testing.T) {
	route := mmdsRoute(7)
	if route.LinkIndex != 7 || route.Scope != netlink.SCOPE_LINK {
		t.Fatalf("route link and scope = %d, %d", route.LinkIndex, route.Scope)
	}
	if route.Dst == nil || route.Dst.String() != "169.254.169.254/32" {
		t.Fatalf("route destination = %v", route.Dst)
	}
}

func TestVerifyPersistenceDetectsRetainedAndReplacedState(t *testing.T) {
	stateRoot := t.TempDir()
	retained, err := VerifyPersistence(stateRoot)
	if err != nil || retained {
		t.Fatalf("first boot = %t, %v; want a new marker", retained, err)
	}
	retained, err = VerifyPersistence(stateRoot)
	if err != nil || !retained {
		t.Fatalf("second boot = %t, %v; want the retained marker", retained, err)
	}
	info, err := os.Stat(filepath.Join(stateRoot, "var", "lib", persistenceMarker))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("marker mode = %o, want 600", info.Mode().Perm())
	}

	replacedState := t.TempDir()
	retained, err = VerifyPersistence(replacedState)
	if err != nil || retained {
		t.Fatalf("replacement disk = %t, %v; want a new marker", retained, err)
	}
}

func TestVerifyPersistenceRejectsUnexpectedMarker(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "var", "lib", persistenceMarker)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("unexpected\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyPersistence(root); err == nil {
		t.Fatal("accepted a marker with unexpected contents")
	}
}

func TestInstallBootstrapWritesProgramAndEnablesService(t *testing.T) {
	root := t.TempDir()
	program := []byte("guest-bootstrap-test")
	if err := InstallBootstrap(root, program); err != nil {
		t.Fatal(err)
	}
	if err := InstallBootstrap(root, program); err != nil {
		t.Fatalf("InstallBootstrap() retry: %v", err)
	}
	programPath := filepath.Join(root, "usr/local/libexec/sandboxctl-bootstrap")
	got, err := os.ReadFile(programPath)
	if err != nil || string(got) != string(program) {
		t.Fatalf("bootstrap program = %q, %v", got, err)
	}
	programInfo, err := os.Stat(programPath)
	if err != nil || programInfo.Mode().Perm() != 0o755 {
		t.Fatalf("bootstrap program mode = %v, %v", programInfo, err)
	}
	servicePath := filepath.Join(root, "etc/systemd/system/sandboxctl-bootstrap.service")
	service, err := os.ReadFile(servicePath)
	if err != nil || string(service) != bootstrapService {
		t.Fatalf("bootstrap service = %q, %v", service, err)
	}
	linkPath := filepath.Join(root, "etc/systemd/system/sockets.target.wants/sandboxctl-bootstrap.service")
	target, err := os.Readlink(linkPath)
	if err != nil || target != "/etc/systemd/system/sandboxctl-bootstrap.service" {
		t.Fatalf("bootstrap service link = %q, %v", target, err)
	}
}

func TestInstallBootstrapRejectsSymlinkPath(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "usr")); err != nil {
		t.Fatal(err)
	}
	if err := InstallBootstrap(root, []byte("binary")); err == nil {
		t.Fatal("InstallBootstrap() followed a guest root symlink")
	}
}
