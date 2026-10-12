package overlayinit

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/vishvananda/netlink"
)

const bootstrapService = `[Unit]
Description=Configure this sandbox from Firecracker metadata
DefaultDependencies=no
After=local-fs.target
Before=ssh.socket ssh.service

[Service]
Type=oneshot
ExecStart=/usr/local/libexec/guest-bootstrap
StandardOutput=journal+console
StandardError=journal+console
RemainAfterExit=yes

[Install]
WantedBy=sockets.target
`

// Mount describes one filesystem mount used during the overlay boot.
type Mount struct {
	Source string
	Target string
	Type   string
	Flags  uintptr
	Data   string
}

// OverlayMounts returns the base, state, and overlay mount definitions.
func OverlayMounts() []Mount {
	return []Mount{
		{Source: "/dev/vda", Target: "/lower", Type: "squashfs", Flags: syscall.MS_RDONLY},
		{Source: "/dev/vdb", Target: "/state", Type: "ext4"},
		{Source: "overlay", Target: "/newroot", Type: "overlay", Data: "lowerdir=/lower,upperdir=/state/upper,workdir=/state/work"},
	}
}

// Run mounts the guest filesystems and transfers control to the guest init.
func Run() error {
	if os.Getpid() != 1 {
		return errors.New("overlay init must run as process 1")
	}
	for _, dir := range []string{"/proc", "/sys", "/dev", "/run"} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	for _, mount := range []Mount{
		{Source: "proc", Target: "/proc", Type: "proc", Flags: syscall.MS_NOSUID | syscall.MS_NODEV | syscall.MS_NOEXEC},
		{Source: "sysfs", Target: "/sys", Type: "sysfs", Flags: syscall.MS_NOSUID | syscall.MS_NODEV | syscall.MS_NOEXEC},
		{Source: "devtmpfs", Target: "/dev", Type: "devtmpfs", Flags: syscall.MS_NOSUID},
		{Source: "tmpfs", Target: "/run", Type: "tmpfs", Flags: syscall.MS_NOSUID | syscall.MS_NODEV},
	} {
		if err := mountFS(mount); err != nil {
			return fmt.Errorf("mount %s: %w", mount.Target, err)
		}
	}
	if err := attachConsole(); err != nil {
		return fmt.Errorf("attach the serial console: %w", err)
	}
	if err := waitForDevices(30 * time.Second); err != nil {
		return err
	}
	for _, dir := range []string{"/lower", "/state", "/newroot"} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	mounts := OverlayMounts()
	for _, mount := range mounts[:2] {
		if err := mountFS(mount); err != nil {
			return fmt.Errorf("mount %s on %s: %w", mount.Source, mount.Target, err)
		}
	}
	for _, dir := range []string{"/state/upper", "/state/work"} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	if err := mountFS(mounts[2]); err != nil {
		return fmt.Errorf("mount overlay: %w", err)
	}
	bootstrap, err := os.ReadFile("/guest-bootstrap")
	if err != nil {
		return fmt.Errorf("read guest bootstrap program: %w", err)
	}
	if err := InstallBootstrap("/newroot", bootstrap); err != nil {
		return fmt.Errorf("install guest bootstrap program: %w", err)
	}
	if err := addMMDSRoute(); err != nil {
		return fmt.Errorf("add the MMDS route: %w", err)
	}
	for _, dir := range []string{"/proc", "/sys", "/dev", "/run"} {
		newRootPath := filepath.Join("/newroot", dir[1:])
		if err := os.MkdirAll(newRootPath, 0o755); err != nil {
			return fmt.Errorf("create new root path %s: %w", dir, err)
		}
	}
	for _, dir := range []string{"/proc", "/sys", "/dev", "/run"} {
		if err := syscall.Mount(dir, filepath.Join("/newroot", dir), "", syscall.MS_MOVE, ""); err != nil {
			return fmt.Errorf("move %s into the new root: %w", dir, err)
		}
	}
	if err := syscall.Chdir("/newroot"); err != nil {
		return fmt.Errorf("change to the overlay root: %w", err)
	}
	// The initramfs root is rootfs. Linux does not allow pivot_root to detach it.
	// Move the overlay mount over rootfs, then make it this process's root.
	if err := syscall.Mount(".", "/", "", syscall.MS_MOVE, ""); err != nil {
		return fmt.Errorf("move the overlay over the initramfs root: %w", err)
	}
	if err := syscall.Chroot("."); err != nil {
		return fmt.Errorf("enter the overlay root: %w", err)
	}
	if err := syscall.Chdir("/"); err != nil {
		return fmt.Errorf("change to the overlay root: %w", err)
	}
	if _, err := os.Stat("/sbin/init"); err != nil {
		return fmt.Errorf("find guest init at /sbin/init: %w", err)
	}
	if err := syscall.Exec("/sbin/init", []string{"/sbin/init"}, os.Environ()); err != nil {
		return fmt.Errorf("start guest init: %w", err)
	}
	return nil
}

// InstallBootstrap places the fixed metadata bootstrap and service into root.
func InstallBootstrap(root string, binary []byte) error {
	if !filepath.IsAbs(root) || len(binary) == 0 || len(binary) > 32<<20 {
		return errors.New("guest bootstrap root or binary is invalid")
	}
	rootInfo, err := os.Lstat(root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("guest root must be a real directory")
	}
	libexec := filepath.Join(root, "usr", "local", "libexec")
	if err := ensureRealDirectory(root, filepath.Join(root, "usr")); err != nil {
		return err
	}
	if err := ensureRealDirectory(root, filepath.Join(root, "usr", "local")); err != nil {
		return err
	}
	if err := ensureRealDirectory(root, libexec); err != nil {
		return err
	}
	if err := writeBootstrapFile(filepath.Join(libexec, "guest-bootstrap"), binary, 0o755); err != nil {
		return fmt.Errorf("write guest bootstrap program: %w", err)
	}
	servicePath := filepath.Join(root, "etc", "systemd", "system", "guest-bootstrap.service")
	for _, directory := range []string{
		filepath.Join(root, "etc"),
		filepath.Join(root, "etc", "systemd"),
		filepath.Join(root, "etc", "systemd", "system"),
	} {
		if err := ensureRealDirectory(root, directory); err != nil {
			return err
		}
	}
	if err := writeBootstrapFile(servicePath, []byte(bootstrapService), 0o644); err != nil {
		return fmt.Errorf("write guest bootstrap service: %w", err)
	}
	wants := filepath.Join(root, "etc", "systemd", "system", "sockets.target.wants")
	if err := ensureRealDirectory(root, wants); err != nil {
		return err
	}
	linkPath := filepath.Join(wants, "guest-bootstrap.service")
	if target, err := os.Readlink(linkPath); err == nil {
		if target != "/etc/systemd/system/guest-bootstrap.service" {
			return errors.New("guest bootstrap service link has an unexpected target")
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if err := os.Symlink("/etc/systemd/system/guest-bootstrap.service", linkPath); err != nil {
			return fmt.Errorf("enable guest bootstrap service: %w", err)
		}
	} else {
		return fmt.Errorf("inspect guest bootstrap service link: %w", err)
	}
	return nil
}

func ensureRealDirectory(root, path string) error {
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("guest bootstrap path escapes the root")
	}
	current := root
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(current, 0o755); err != nil {
				return fmt.Errorf("create guest bootstrap directory: %w", err)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect guest bootstrap directory: %w", err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("guest bootstrap path contains a non-directory")
		}
	}
	return nil
}

func writeBootstrapFile(path string, content []byte, mode os.FileMode) error {
	directory := filepath.Dir(path)
	file, err := os.CreateTemp(directory, ".guest-bootstrap-")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func addMMDSRoute() error {
	interfaces, err := net.Interfaces()
	if err != nil {
		return err
	}
	for _, iface := range interfaces {
		if iface.Name == "eth0" {
			return netlink.RouteReplace(mmdsRoute(iface.Index))
		}
	}
	return nil
}

func mmdsRoute(interfaceIndex int) *netlink.Route {
	return &netlink.Route{
		LinkIndex: interfaceIndex,
		Scope:     netlink.SCOPE_LINK,
		Dst: &net.IPNet{
			IP:   net.ParseIP("169.254.169.254").To4(),
			Mask: net.CIDRMask(32, 32),
		},
	}
}

func mountFS(mount Mount) error {
	return syscall.Mount(mount.Source, mount.Target, mount.Type, mount.Flags, mount.Data)
}

func attachConsole() error {
	consoleFD, err := syscall.Open("/dev/console", syscall.O_RDWR, 0)
	if err != nil {
		return err
	}
	for target := 0; target <= 2; target++ {
		if err := syscall.Dup2(consoleFD, target); err != nil {
			if consoleFD > 2 {
				_ = syscall.Close(consoleFD)
			}
			return err
		}
	}
	if consoleFD > 2 {
		_ = syscall.Close(consoleFD)
	}
	return nil
}

func waitForDevices(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat("/dev/vda"); err == nil {
			if _, err := os.Stat("/dev/vdb"); err == nil {
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("Firecracker block devices /dev/vda and /dev/vdb did not appear within %s", timeout)
}

// Main runs the overlay init program and reports boot errors to the console.
func Main() {
	if os.Getpid() != 1 {
		_, _ = fmt.Fprintln(os.Stderr, "overlay-init: must run as process 1")
		os.Exit(1)
	}
	if err := Run(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "overlay-init: %v\n", err)
		for {
			time.Sleep(time.Hour)
		}
	}
}
