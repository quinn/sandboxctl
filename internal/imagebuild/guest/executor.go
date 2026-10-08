// Package guest runs bounded build operations inside an isolated guest.
package guest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	maxCommandArguments = 256
	maxOutputBytes      = 4 << 20
	maxUploadBytes      = 32 << 30
	buildDirectory      = "/var/tmp/sandboxctl-build"
)

// Command identifies one fixed guest-side build operation.
type Command struct {
	Program string
	Args    []string
}

// Result contains bounded output from one guest command.
type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// GuestExecutor runs commands and uploads files to a builder guest.
type GuestExecutor interface {
	Run(context.Context, Command) (Result, error)
	Upload(context.Context, io.Reader, string, int64) error
}

// SSHConfig defines the host and private files for one builder SSH connection.
type SSHConfig struct {
	Target         string
	PrivateKeyPath string
	KnownHostsPath string
	SSHPath        string
}

// SSHExecutor runs fixed build operations over OpenSSH.
type SSHExecutor struct {
	config SSHConfig
}

// NewSSHExecutor validates the connection settings for a builder guest.
func NewSSHExecutor(config SSHConfig) (*SSHExecutor, error) {
	if !validTarget(config.Target) {
		return nil, errors.New("builder SSH target is invalid")
	}
	if !filepath.IsAbs(config.PrivateKeyPath) || !filepath.IsAbs(config.KnownHostsPath) {
		return nil, errors.New("builder SSH key and known-hosts paths must be absolute")
	}
	if config.SSHPath == "" {
		config.SSHPath = "/usr/bin/ssh"
	}
	if !filepath.IsAbs(config.SSHPath) {
		return nil, errors.New("builder SSH executable path must be absolute")
	}
	return &SSHExecutor{config: config}, nil
}

// Run executes one guest command and captures bounded standard output and error.
func (executor *SSHExecutor) Run(ctx context.Context, command Command) (Result, error) {
	remote, err := renderCommand(command)
	if err != nil {
		return Result{}, err
	}
	process := exec.CommandContext(ctx, executor.config.SSHPath, executor.sshArguments(remote)...)
	stdout := &boundedBuffer{limit: maxOutputBytes}
	stderr := &boundedBuffer{limit: maxOutputBytes}
	process.Stdout = stdout
	process.Stderr = stderr
	err = process.Run()
	result := Result{Stdout: stdout.bytes, Stderr: stderr.bytes, ExitCode: 0}
	if process.ProcessState != nil {
		result.ExitCode = process.ProcessState.ExitCode()
	}
	if stdout.exceeded || stderr.exceeded {
		return result, errors.New("builder command output exceeds the size limit")
	}
	if err != nil {
		return result, fmt.Errorf("run builder command: %w", err)
	}
	return result, nil
}

// Upload writes one file atomically below the builder's private build directory.
func (executor *SSHExecutor) Upload(ctx context.Context, source io.Reader, destination string, size int64) error {
	if source == nil || size < 0 || size > maxUploadBytes {
		return errors.New("builder upload size is invalid")
	}
	if !validDestination(destination) {
		return errors.New("builder upload destination is invalid")
	}
	temporary := destination + ".sandboxctl-upload"
	remote := "set -eu; umask 077; mkdir -p " + shellQuote(buildDirectory) +
		" " + shellQuote(filepath.Dir(destination)) +
		"; temporary=" + shellQuote(temporary) +
		"; trap 'rm -f -- \"$temporary\"' EXIT HUP INT TERM; cat >\"$temporary\"; " +
		"test \"$(wc -c <\"$temporary\")\" -eq " + fmt.Sprint(size) +
		"; chmod 0600 \"$temporary\"; mv -f -- \"$temporary\" " + shellQuote(destination) +
		"; trap - EXIT HUP INT TERM"
	process := exec.CommandContext(ctx, executor.config.SSHPath, executor.sshArguments(remote)...)
	process.Stdin = io.LimitReader(source, size)
	output := &boundedBuffer{limit: maxOutputBytes}
	process.Stderr = output
	if err := process.Run(); err != nil {
		return fmt.Errorf("upload builder file: %w", err)
	}
	if output.exceeded {
		return errors.New("builder upload output exceeds the size limit")
	}
	return nil
}

func (executor *SSHExecutor) sshArguments(remote string) []string {
	return []string{
		"-T", "-F", "/dev/null",
		"-i", executor.config.PrivateKeyPath,
		"-o", "IdentitiesOnly=yes",
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=yes",
		"-o", "PasswordAuthentication=no",
		"-o", "KbdInteractiveAuthentication=no",
		"-o", "GlobalKnownHostsFile=/dev/null",
		"-o", "UserKnownHostsFile=" + executor.config.KnownHostsPath,
		"-o", "ConnectTimeout=10",
		"--", executor.config.Target, remote,
	}
}

func renderCommand(command Command) (string, error) {
	if command.Program == "" || len(command.Args) > maxCommandArguments || strings.ContainsRune(command.Program, 0) {
		return "", errors.New("builder command is invalid")
	}
	parts := make([]string, 0, len(command.Args)+1)
	parts = append(parts, shellQuote(command.Program))
	for _, argument := range command.Args {
		if strings.ContainsRune(argument, 0) {
			return "", errors.New("builder command is invalid")
		}
		parts = append(parts, shellQuote(argument))
	}
	return "exec " + strings.Join(parts, " "), nil
}

func validTarget(target string) bool {
	if target == "" || len(target) > 255 || strings.HasPrefix(target, "-") || strings.ContainsAny(target, "\x00\r\n \t") {
		return false
	}
	return true
}

func validDestination(destination string) bool {
	if !filepath.IsAbs(destination) || filepath.Clean(destination) != destination || strings.ContainsRune(destination, 0) {
		return false
	}
	return strings.HasPrefix(destination, buildDirectory+"/") && !strings.Contains(destination, "/../")
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

type boundedBuffer struct {
	bytes    []byte
	limit    int
	exceeded bool
}

func (buffer *boundedBuffer) Write(value []byte) (int, error) {
	if len(buffer.bytes)+len(value) > buffer.limit {
		remaining := buffer.limit - len(buffer.bytes)
		if remaining > 0 {
			buffer.bytes = append(buffer.bytes, value[:remaining]...)
		}
		buffer.exceeded = true
		return len(value), nil
	}
	buffer.bytes = append(buffer.bytes, value...)
	return len(value), nil
}
