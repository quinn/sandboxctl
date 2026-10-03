package overlayinit

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const persistenceMarker = ".sandboxctl-phase1-overlay-proof"
const persistenceValue = "overlay-state-was-retained-v1\n"

type Mount struct {
	Source string
	Target string
	Type   string
	Flags  uintptr
	Data   string
}

func OverlayMounts() []Mount {
	return []Mount{
		{Source: "/dev/vda", Target: "/lower", Type: "squashfs", Flags: syscall.MS_RDONLY},
		{Source: "/dev/vdb", Target: "/state", Type: "ext4"},
		{Source: "overlay", Target: "/newroot", Type: "overlay", Data: "lowerdir=/lower,upperdir=/state/upper,workdir=/state/work"},
	}
}

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
	retained, err := VerifyPersistence("/newroot")
	if err != nil {
		return fmt.Errorf("verify overlay state: %w", err)
	}
	if retained {
		fmt.Fprintln(os.Stdout, "PHASE1 OVERLAY: retained the marker from the previous boot")
	} else {
		fmt.Fprintln(os.Stdout, "PHASE1 OVERLAY: created a marker on the new state disk")
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

func mountFS(mount Mount) error {
	return syscall.Mount(mount.Source, mount.Target, mount.Type, mount.Flags, mount.Data)
}

// VerifyPersistence creates a marker on first boot and recognizes it on later boots.
func VerifyPersistence(root string) (bool, error) {
	dir := filepath.Join(root, "var", "lib")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	path := filepath.Join(dir, persistenceMarker)
	data, err := os.ReadFile(path)
	if err == nil {
		if string(data) != persistenceValue {
			return false, errors.New("the overlay proof marker has unexpected content")
		}
		return true, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if err := os.WriteFile(path, []byte(persistenceValue), 0o600); err != nil {
		return false, err
	}
	syscall.Sync()
	return false, nil
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
