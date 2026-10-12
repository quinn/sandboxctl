package ssh

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"sandboxctl/internal/cmdutil"
)

const maxCodexAuthBytes = 1 << 20

// CopyCodexAuth replaces the guest login file with the operator's Codex login.
func (m Manager) CopyCodexAuth(ctx context.Context, name, address string) error {
	if err := cmdutil.ValidateSandboxName(name); err != nil {
		return err
	}
	ip := net.ParseIP(address)
	if ip == nil || ip.To4() == nil {
		return errors.New("the guest SSH address must be an IPv4 address")
	}
	data, err := readCodexAuth()
	if err != nil {
		return err
	}
	if err := waitForSSH(ctx, net.JoinHostPort(ip.String(), "22")); err != nil {
		return err
	}
	command := exec.CommandContext(ctx, "/usr/bin/ssh", codexAuthArguments(name, len(data))...)
	command.Stdin = bytes.NewReader(data)
	command.Stdout = io.Discard
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("copy the Codex login file over SSH: %w", ctx.Err())
		}
		return fmt.Errorf("copy the Codex login file over SSH: %w", err)
	}
	return nil
}

func readCodexAuth() ([]byte, error) {
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("get the Codex home directory: %w", err)
		}
		home = filepath.Join(userHome, ".codex")
	}
	path := filepath.Join(home, "auth.json")
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("open the host Codex login file: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect the host Codex login file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("the host Codex login file must be a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxCodexAuthBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read the host Codex login file: %w", err)
	}
	if len(data) == 0 || len(data) > maxCodexAuthBytes {
		return nil, errors.New("the host Codex login file must contain 1 byte to 1 MiB")
	}
	return data, nil
}

func waitForSSH(ctx context.Context, address string) error {
	dialer := net.Dialer{Timeout: 3 * time.Second}
	for {
		connection, err := dialer.DialContext(ctx, "tcp", address)
		if err == nil {
			_ = connection.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for guest SSH: %w", ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// WaitReady waits for guest SSH and verifies completed guest bootstrap.
func (m Manager) WaitReady(ctx context.Context, name, address string) error {
	if err := cmdutil.ValidateSandboxName(name); err != nil {
		return err
	}
	ip := net.ParseIP(address)
	if ip == nil || ip.To4() == nil {
		return errors.New("the guest SSH address must be an IPv4 address")
	}
	if err := waitForSSH(ctx, net.JoinHostPort(ip.String(), "22")); err != nil {
		return err
	}
	command := exec.CommandContext(ctx, "/usr/bin/ssh", "-T", "-o", "ConnectTimeout=5", "-o", "ConnectionAttempts=1", "--", "sandbox-"+name, "test -r /var/lib/sandboxctl/bootstrap-complete")
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("check guest SSH readiness: %w", err)
	}
	return nil
}

func codexAuthArguments(name string, size int) []string {
	remote := fmt.Sprintf(`set -eu
umask 077
test -r /var/lib/sandboxctl/bootstrap-complete
test ! -L "$HOME/.codex"
mkdir -p -- "$HOME/.codex"
chmod 0700 -- "$HOME/.codex"
cd -- "$HOME/.codex"
temporary=$(mktemp .auth.json.XXXXXX)
trap 'rm -f -- "$temporary"' EXIT HUP INT TERM
cat > "$temporary"
test "$(wc -c < "$temporary")" -eq %d
chmod 0600 -- "$temporary"
mv -fT -- "$temporary" auth.json
trap - EXIT HUP INT TERM`, size)
	return []string{
		"-T",
		"-o", "ConnectTimeout=5",
		"-o", "ConnectionAttempts=1",
		"-o", "PasswordAuthentication=no",
		"-o", "KbdInteractiveAuthentication=no",
		"--", "sandbox-" + name, remote,
	}
}
