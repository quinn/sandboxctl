package disk

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const copyBlockSize = 1024 * 1024

// CloneSparse copies a regular file to a new private file and keeps zero blocks sparse.
func CloneSparse(source, destination string) error {
	pathInfo, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("inspect source drive: %w", err)
	}
	if !pathInfo.Mode().IsRegular() {
		return fmt.Errorf("source drive must be a regular file")
	}
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open source drive: %w", err)
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return fmt.Errorf("inspect source drive: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("source drive must be a regular file")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return fmt.Errorf("create destination directory: %w", err)
	}
	output, err := os.CreateTemp(filepath.Dir(destination), ".root-drive-*")
	if err != nil {
		return fmt.Errorf("create private root drive: %w", err)
	}
	temporary := output.Name()
	defer os.Remove(temporary)
	buffer := make([]byte, copyBlockSize)
	for {
		n, readErr := input.Read(buffer)
		if n > 0 {
			chunk := buffer[:n]
			if allZero(chunk) {
				if _, err := output.Seek(int64(n), io.SeekCurrent); err != nil {
					return fmt.Errorf("seek over empty drive block: %w", err)
				}
			} else if _, err := output.Write(chunk); err != nil {
				return fmt.Errorf("copy root drive: %w", err)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("read source drive: %w", readErr)
		}
	}
	if err := output.Truncate(info.Size()); err != nil {
		return fmt.Errorf("set root drive size: %w", err)
	}
	if err := output.Sync(); err != nil {
		return fmt.Errorf("sync root drive: %w", err)
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("close root drive: %w", err)
	}
	if err := os.Link(temporary, destination); err != nil {
		return fmt.Errorf("install private root drive: %w", err)
	}
	return nil
}

func allZero(data []byte) bool {
	for _, value := range data {
		if value != 0 {
			return false
		}
	}
	return true
}
