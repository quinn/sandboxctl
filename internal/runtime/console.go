package runtime

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

const consoleLogLimit = 4 << 20

var consoleLogLimitMessage = []byte("\n[sandboxctl] guest console log reached its size limit; further output was discarded\n")

type boundedConsoleLog struct {
	mu        sync.Mutex
	file      *os.File
	written   int
	truncated bool
}

func openConsoleLog(instancesDir, name string) (*boundedConsoleLog, string, error) {
	directory := filepath.Join(instancesDir, name)
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, "", fmt.Errorf("inspect sandbox instance directory for console log: %w", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !ok || int(stat.Uid) != os.Geteuid() || info.Mode().Perm()&0o077 != 0 {
		return nil, "", errors.New("sandbox instance directory is not private and daemon-owned")
	}
	path := filepath.Join(directory, "console.log")
	fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_TRUNC|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, "", fmt.Errorf("open private guest console log: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	info, err = file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, "", fmt.Errorf("inspect guest console log: %w", err)
	}
	stat, ok = info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || !ok || int(stat.Uid) != os.Geteuid() {
		_ = file.Close()
		return nil, "", errors.New("guest console log is not a regular daemon-owned file")
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, "", fmt.Errorf("set guest console log mode: %w", err)
	}
	return &boundedConsoleLog{file: file}, path, nil
}

func (log *boundedConsoleLog) Write(data []byte) (int, error) {
	log.mu.Lock()
	defer log.mu.Unlock()
	if log.truncated {
		return len(data), nil
	}
	contentLimit := consoleLogLimit - len(consoleLogLimitMessage)
	remaining := contentLimit - log.written
	if len(data) <= remaining {
		written, err := log.file.Write(data)
		log.written += written
		return written, err
	}
	if remaining > 0 {
		written, err := log.file.Write(data[:remaining])
		log.written += written
		if err != nil {
			return written, err
		}
	}
	written, err := log.file.Write(consoleLogLimitMessage)
	log.written += written
	log.truncated = true
	if err != nil {
		return len(data), err
	}
	return len(data), nil
}

func (log *boundedConsoleLog) Close() error {
	log.mu.Lock()
	defer log.mu.Unlock()
	return log.file.Close()
}

func (log *boundedConsoleLog) Stats() (int, bool) {
	log.mu.Lock()
	defer log.mu.Unlock()
	return log.written, log.truncated
}
