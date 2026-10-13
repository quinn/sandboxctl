package cmdutil

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandDebugLogging(t *testing.T) {
	switch os.Getenv("SANDBOXCTL_LOG_CHILD") {
	case "dispatch":
		os.Args = []string{"sandboxctl", "probe"}
		MainDispatch("sandboxctl-")
		return
	case "handler":
		os.Args = []string{"sandboxctl-probe"}
		Main(func([]string) error {
			fmt.Fprintln(os.Stdout, "command output")
			if os.Getenv("SANDBOXCTL_LOG_FAIL") == "1" {
				return errors.New("probe failure")
			}
			return nil
		})
		os.Exit(0)
		return
	}
	directory := t.TempDir()
	executable := filepath.Join(directory, "sandboxctl-probe")
	quotedBinary := "'" + strings.ReplaceAll(os.Args[0], "'", "'\"'\"'") + "'"
	script := "#!/bin/sh\nexport SANDBOXCTL_LOG_CHILD=handler\nexec " + quotedBinary + " -test.run=^TestCommandDebugLogging$\n"
	if err := os.WriteFile(executable, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, debug := range []string{"", "false", "TRUE", "1", "true"} {
		for _, fail := range []string{"0", "1"} {
			t.Run("DEBUG="+debug+"/failure="+fail, func(t *testing.T) {
				command := exec.Command(os.Args[0], "-test.run=^TestCommandDebugLogging$")
				command.Env = []string{"PATH=" + directory + ":/usr/bin:/bin", "SANDBOXCTL_LOG_CHILD=dispatch", "SANDBOXCTL_LOG_FAIL=" + fail}
				if debug != "" {
					command.Env = append(command.Env, "DEBUG="+debug)
				}
				var stdout, stderr bytes.Buffer
				command.Stdout, command.Stderr = &stdout, &stderr
				err := command.Run()
				if fail == "1" {
					var exit *exec.ExitError
					if !errors.As(err, &exit) || exit.ExitCode() != 1 {
						t.Fatalf("failure exit = %v", err)
					}
					if !strings.Contains(stderr.String(), "error: probe failure\n") {
						t.Fatal("the command error was suppressed")
					}
				} else if err != nil {
					t.Fatal(err)
				}
				if stdout.String() != "command output\n" {
					t.Fatalf("command output = %q", stdout.String())
				}
				messages := []string{"dispatching command", "command started"}
				if fail == "1" {
					messages = append(messages, "command failed")
				} else {
					messages = append(messages, "command completed")
				}
				for _, message := range messages {
					if strings.Contains(stderr.String(), "DEBUG "+message) != (debug == "true") {
						t.Fatalf("unexpected logs for DEBUG=%q: %s", debug, stderr.String())
					}
				}
				if debug != "true" && fail == "0" && stderr.Len() != 0 {
					t.Fatalf("unexpected diagnostics: %s", stderr.String())
				}
			})
		}
	}
}
