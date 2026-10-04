package process

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Record identifies a process by its process ID and kernel start time.
type Record struct {
	PID       int    `json:"pid"`
	StartTime uint64 `json:"start_time"`
}

// Read loads and validates a process record from path.
func Read(path string) (Record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Record{}, err
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil || record.PID < 1 || record.StartTime == 0 {
		return Record{}, fmt.Errorf("the process record is not valid")
	}
	return record, nil
}

// Current returns the process record for the process with pid.
func Current(pid int) (Record, error) {
	start, err := procStartTime(pid)
	if err != nil {
		return Record{}, err
	}
	return Record{PID: pid, StartTime: start}, nil
}

// Write atomically stores record at path with private file permissions.
func Write(path string, record Record) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".process-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// RemoveIfCurrent removes path only when it still contains record.
func RemoveIfCurrent(path string, record Record) error {
	current, err := Read(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if current != record {
		return nil
	}
	return os.Remove(path)
}

// IsRunning reports whether record still identifies the same live process.
func IsRunning(record Record) bool {
	if record.PID < 1 || record.StartTime == 0 {
		return false
	}
	current, err := Current(record.PID)
	return err == nil && current.StartTime == record.StartTime
}

// Stop sends SIGTERM to the process identified by record.
func Stop(record Record) error {
	if !IsRunning(record) {
		return fmt.Errorf("process %d is not running", record.PID)
	}
	process, err := os.FindProcess(record.PID)
	if err != nil {
		return fmt.Errorf("find process %d: %w", record.PID, err)
	}
	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("stop process %d: %w", record.PID, err)
	}
	return nil
}

// WaitStopped waits up to timeout for the process identified by record to exit.
func WaitStopped(record Record, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !IsRunning(record) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return !IsRunning(record)
}

func procStartTime(pid int) (uint64, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, err
	}
	close := strings.LastIndex(string(data), ")")
	if close < 0 {
		return 0, errors.New("invalid process stat")
	}
	fields := strings.Fields(string(data)[close+1:])
	if len(fields) == 0 || fields[0] == "Z" {
		return 0, os.ErrProcessDone
	}
	if len(fields) <= 19 {
		return 0, errors.New("invalid process stat")
	}
	start, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return 0, err
	}
	return start, nil
}
