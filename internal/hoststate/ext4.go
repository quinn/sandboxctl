package hoststate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// Ext4StateDiskBuilder creates sparse ext4 disks for sandbox overlays.
type Ext4StateDiskBuilder struct{}

// Build creates a sparse disk owned by uid and gid and formats it as ext4.
func (Ext4StateDiskBuilder) Build(parent context.Context, path string, size int64, uid, gid uint32) error {
	if !filepath.IsAbs(path) || size < 1 {
		return errors.New("state disk path and size are invalid")
	}
	directory := filepath.Dir(path)
	if err := os.Mkdir(directory, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create instance directory: %w", err)
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return fmt.Errorf("inspect instance directory: %w", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !ok || int(stat.Uid) != os.Geteuid() {
		return errors.New("instance directory must be a real directory owned by sandboxd")
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return fmt.Errorf("set instance directory mode: %w", err)
	}
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_EXCL|syscall.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0o600)
	created := err == nil
	if errors.Is(err, syscall.EEXIST) {
		fd, err = syscall.Open(path, syscall.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	}
	if err != nil {
		return fmt.Errorf("open state disk: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	fileInfo, err := file.Stat()
	if err != nil {
		file.Close()
		if created {
			_ = os.Remove(path)
		}
		return fmt.Errorf("inspect state disk: %w", err)
	}
	fileStat, ok := fileInfo.Sys().(*syscall.Stat_t)
	if !fileInfo.Mode().IsRegular() || !ok {
		file.Close()
		if created {
			_ = os.Remove(path)
		}
		return errors.New("existing state disk has unexpected type or owner")
	}
	if !created {
		if fileInfo.Size() != size || fileStat.Uid != uid || fileStat.Gid != gid {
			file.Close()
			return errors.New("existing state disk has unexpected size or owner")
		}
		if err := file.Chmod(0o600); err != nil {
			file.Close()
			return fmt.Errorf("set state disk mode: %w", err)
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("close state disk: %w", err)
		}
		return checkExt4StateDisk(parent, path)
	}
	if err := file.Truncate(size); err != nil {
		file.Close()
		_ = os.Remove(path)
		return fmt.Errorf("size state disk: %w", err)
	}
	if err := file.Chown(int(uid), int(gid)); err != nil {
		file.Close()
		_ = os.Remove(path)
		return fmt.Errorf("set state disk owner: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		_ = os.Remove(path)
		return fmt.Errorf("set state disk mode: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		_ = os.Remove(path)
		return fmt.Errorf("sync state disk: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("close state disk: %w", err)
	}
	return formatExt4StateDisk(parent, path)
}

func formatExt4StateDisk(parent context.Context, path string) error {
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "/usr/bin/mkfs.ext4", "-q", "-F", path)
	command.Env = []string{"PATH=/usr/bin:/bin", "LC_ALL=C"}
	output, err := command.CombinedOutput()
	if err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("format state disk as ext4: %w: %s", err, string(output))
	}
	return nil
}

func checkExt4StateDisk(parent context.Context, path string) error {
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "/usr/bin/e2fsck", "-fn", path)
	command.Env = []string{"PATH=/usr/bin:/bin", "LC_ALL=C"}
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("validate existing ext4 state disk: %w: %s", err, string(output))
	}
	return nil
}
