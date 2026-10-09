package cmdutil

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// MainDispatch replaces the current process with the selected external command.
func MainDispatch(prefix string) {
	if len(os.Args) > 1 {
		slog.Info("dispatching command", "command", prefix+os.Args[1])
	}
	ExitOnError(Dispatch(prefix, os.Args[1:]))
}

// Main runs a command handler and reports an error with a nonzero exit status.
func Main(run func([]string) error) {
	command := filepath.Base(os.Args[0])
	started := time.Now()
	slog.Info("command started", "command", command)
	err := run(os.Args[1:])
	if err != nil {
		slog.Error("command failed", "command", command, "duration", time.Since(started), "error", err)
	} else {
		slog.Info("command completed", "command", command, "duration", time.Since(started))
	}
	ExitOnError(err)
}

// ExitOnError reports err and exits with status 1 when err is not nil.
func ExitOnError(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// Dispatch finds prefix+command through PATH and replaces the current process.
func Dispatch(prefix string, arguments []string) error {
	return dispatch(prefix, arguments, exec.LookPath, syscall.Exec, os.Environ())
}

func dispatch(prefix string, arguments []string, lookPath func(string) (string, error), replace func(string, []string, []string) error, environment []string) error {
	if len(arguments) == 0 {
		return errors.New("command is required")
	}
	commandName := prefix + arguments[0]
	executable, err := lookPath(commandName)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return fmt.Errorf("unknown command %q", arguments[0])
		}
		return fmt.Errorf("find command %q: %w", arguments[0], err)
	}
	argv := make([]string, 1, len(arguments))
	argv[0] = commandName
	argv = append(argv, arguments[1:]...)
	if err := replace(executable, argv, environment); err != nil {
		return fmt.Errorf("execute command %q: %w", commandName, err)
	}
	return nil
}
