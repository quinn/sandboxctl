package taskgit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"sandboxctl/internal/cmdutil"
)

var objectID = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
var instanceID = regexp.MustCompile(`^[0-9a-f]{32}$`)

// Record associates one sandbox identity with its operator's repository.
type Record struct {
	SchemaVersion int    `json:"schema_version"`
	Name          string `json:"name"`
	InstanceID    string `json:"instance_id"`
	Repository    string `json:"repository"`
	StartCommit   string `json:"start_commit"`
	AuthorName    string `json:"author_name"`
	AuthorEmail   string `json:"author_email"`
	Initialized   bool   `json:"initialized"`
}

func (m Manager) stateDirectory() string { return filepath.Join(m.Paths.StateDir, "tasks") }
func (m Manager) recordPath(name string) string {
	return filepath.Join(m.stateDirectory(), name+".json")
}

func (m Manager) load(name string) (*Record, error) {
	if err := cmdutil.ValidateSandboxName(name); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(m.recordPath(name), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the Git task record: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("the Git task record must be a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 64<<10 {
		return nil, errors.New("the Git task record exceeds the size limit")
	}
	var record Record
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return nil, fmt.Errorf("decode the Git task record: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("the Git task record contains extra data")
	}
	if record.SchemaVersion != 1 || record.Name != name || !instanceID.MatchString(record.InstanceID) || !objectID.MatchString(record.StartCommit) || !filepath.IsAbs(record.Repository) || strings.ContainsAny(record.Repository, "\x00\r\n") {
		return nil, errors.New("the Git task record contains invalid values")
	}
	return &record, nil
}

func privateDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("the task state path must be a directory")
	}
	return os.Chmod(path, 0o700)
}

func atomicFile(directory, destination string, data []byte) error {
	if err := privateDirectory(directory); err != nil {
		return err
	}
	file, err := os.CreateTemp(directory, ".task-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), destination); err != nil {
		return err
	}
	dir, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (m Manager) save(record *Record) error {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return atomicFile(m.stateDirectory(), m.recordPath(record.Name), append(data, '\n'))
}

func (m Manager) lock(name string) (func(), error) {
	if err := cmdutil.ValidateSandboxName(name); err != nil {
		return nil, err
	}
	if err := privateDirectory(m.stateDirectory()); err != nil {
		return nil, err
	}
	path := filepath.Join(m.stateDirectory(), name+".lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open the task lock: %w", err)
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		return nil, errors.New("the Git task lock must be a regular file")
	}
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("another Git task operation uses sandbox %s: %w", name, err)
	}
	return func() { _ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN); _ = file.Close() }, nil
}
