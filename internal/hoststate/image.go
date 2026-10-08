package hoststate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// InstalledImages reads pinned versions from daemon-owned image manifests.
type InstalledImages struct {
	Paths Paths
}

// ResolveVersion returns the installed version recorded for name.
func (images InstalledImages) ResolveVersion(name string) (string, error) {
	if !safeName.MatchString(name) {
		return "", errors.New("invalid image name")
	}
	path := filepath.Join(images.Paths.Images, name, "manifest.json")
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%w: %q", ErrImageNotInstalled, name)
		}
		return "", fmt.Errorf("open installed image manifest: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("inspect image manifest: %w", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || !ok || int(stat.Uid) != os.Geteuid() || info.Mode().Perm()&0o022 != 0 {
		return "", errors.New("image manifest must be a regular file owned by sandboxd")
	}
	if info.Size() > 1<<20 {
		return "", errors.New("image manifest exceeds the size limit")
	}
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil {
		return "", fmt.Errorf("read image manifest: %w", err)
	}
	if len(data) > 1<<20 {
		return "", errors.New("image manifest exceeds the size limit")
	}
	var manifest struct {
		SchemaVersion int    `json:"schema_version"`
		Name          string `json:"name"`
		Version       string `json:"version"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "", fmt.Errorf("decode image manifest: %w", err)
	}
	if manifest.SchemaVersion != 1 || manifest.Name != name || manifest.Version == "" {
		return "", errors.New("image manifest identity or schema is invalid")
	}
	return manifest.Version, nil
}
