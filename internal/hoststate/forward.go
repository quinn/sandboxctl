package hoststate

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// LoadForwardKey reads the daemon-owned forwarding private key.
func (s Store) LoadForwardKey(name string) ([]byte, error) {
	if _, err := s.Load(name); err != nil {
		return nil, err
	}
	directory, err := s.forwardDirectory(name)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(directory, "forward_ed25519")
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || !ok || int(stat.Uid) != os.Geteuid() || info.Mode().Perm() != 0600 || info.Size() > 16384 {
		return nil, errors.New("forwarding key must be a private regular file owned by sandboxd")
	}
	return io.ReadAll(io.LimitReader(file, 16385))
}

// SaveForwardKey writes a forwarding key under the fixed instance path.
// The caller must serialize this operation with instance changes.
func (s Store) SaveForwardKey(name string, data []byte) error {
	if _, err := s.Load(name); err != nil {
		return err
	}
	if err := s.Paths.Ensure(); err != nil {
		return err
	}
	if len(data) == 0 || len(data) > 16384 {
		return errors.New("invalid forwarding key size")
	}
	directory, err := s.forwardDirectory(name)
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(directory, "forward_ed25519"), data, 0600)
}

func (s Store) forwardDirectory(name string) (string, error) {
	directory := filepath.Join(s.Paths.Instances, name)
	info, err := os.Lstat(directory)
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || !ok || int(stat.Uid) != os.Geteuid() || info.Mode().Perm() != 0700 {
		return "", errors.New("instance directory must be private and owned by sandboxd")
	}
	return directory, nil
}
