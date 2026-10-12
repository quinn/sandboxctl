// Package taskgit exchanges one Git task with a sandbox through operator SSH.
package taskgit

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"sandboxctl/internal/controlpb"
	sshconfig "sandboxctl/internal/ssh"
)

const maxOutput = 4 << 20

// Runner executes Git and SSH as the operator.
type Runner interface {
	Git(context.Context, string, ...string) ([]byte, error)
	SSH(context.Context, string, string) ([]byte, error)
	Ready(context.Context, *controlpb.Instance) error
}

// SystemRunner uses the installed Git and OpenSSH programs.
type SystemRunner struct{ SSHManager sshconfig.Manager }

func (r SystemRunner) Git(ctx context.Context, directory string, args ...string) ([]byte, error) {
	argv := []string{"-c", "core.hooksPath=/dev/null", "-c", "fetch.fsckObjects=true", "-C", directory}
	argv = append(argv, args...)
	command := exec.CommandContext(ctx, "/usr/bin/git", argv...)
	// Repository selection belongs to this operation, not the caller's shell.
	for _, variable := range os.Environ() {
		if !strings.HasPrefix(variable, "GIT_") {
			command.Env = append(command.Env, variable)
		}
	}
	command.Env = append(command.Env, "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	if len(args) > 0 && (args[0] == "push" || args[0] == "fetch") {
		command.Stderr = os.Stderr
	}
	return capture(ctx, command)
}

func (r SystemRunner) SSH(ctx context.Context, name, script string) ([]byte, error) {
	command := exec.CommandContext(ctx, "/usr/bin/ssh", "-T", "-o", "ConnectTimeout=5", "-o", "ConnectionAttempts=1", "-o", "PasswordAuthentication=no", "-o", "KbdInteractiveAuthentication=no", "--", "sandbox-"+name, script)
	command.Stderr = os.Stderr
	return capture(ctx, command)
}

func (r SystemRunner) Ready(ctx context.Context, instance *controlpb.Instance) error {
	return r.SSHManager.WaitReady(ctx, instance.GetName(), instance.GetGuestIp())
}

func capture(ctx context.Context, command *exec.Cmd) ([]byte, error) {
	stdout, stderr := &limitedBuffer{}, &limitedBuffer{}
	command.Stdout = stdout
	if command.Stderr != nil {
		command.Stderr = io.MultiWriter(command.Stderr, stderr)
	} else {
		command.Stderr = stderr
	}
	command.WaitDelay = 2 * time.Second
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%s failed: %w: %s", command.Path, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(data []byte) (int, error) {
	if b.Len()+len(data) > maxOutput {
		return 0, fmt.Errorf("command output exceeds %d bytes", maxOutput)
	}
	return b.Buffer.Write(data)
}

func quote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }

var _ io.Writer = (*limitedBuffer)(nil)
