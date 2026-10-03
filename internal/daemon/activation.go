package daemon

import (
	"fmt"
	"net"
	"os"
	"strconv"
)

// ActivatedListener validates and wraps the stream socket passed by systemd.
// It takes ownership of socketFile.
func ActivatedListener(listenPID, listenFDS string, pid int, socketFile *os.File) (net.Listener, error) {
	if socketFile == nil {
		return nil, fmt.Errorf("systemd socket file is missing")
	}
	defer socketFile.Close()
	activatedPID, err := strconv.Atoi(listenPID)
	if err != nil || activatedPID != pid {
		return nil, fmt.Errorf("systemd socket activation PID does not match")
	}
	fileCount, err := strconv.Atoi(listenFDS)
	if err != nil || fileCount != 1 {
		return nil, fmt.Errorf("systemd must pass exactly one socket")
	}
	listener, err := net.FileListener(socketFile)
	if err != nil {
		return nil, fmt.Errorf("read systemd socket: %w", err)
	}
	if _, ok := listener.(*net.UnixListener); !ok {
		_ = listener.Close()
		return nil, fmt.Errorf("systemd socket must use Unix domain sockets")
	}
	return listener, nil
}
