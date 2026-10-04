package cmdutil

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDispatchReplacesProcessAndPreservesProcessState(t *testing.T) {
	if os.Getenv("SANDBOXCTL_DISPATCH_CHILD") == "1" {
		if err := Dispatch("sandboxctl-", []string{"probe", "arg-one", "arg-two"}); err != nil {
			t.Fatalf("Dispatch() error = %v", err)
		}
		return
	}

	directory := t.TempDir()
	workDirectory := filepath.Join(directory, "work")
	if err := os.Mkdir(workDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(directory, "sandboxctl-probe")
	script := "#!/bin/sh\nprintf '%s|%s|%s|%s\\n' \"$1\" \"$2\" \"$SANDBOXCTL_DISPATCH_VALUE\" \"$PWD\"\ncat\nprintf 'child-stderr\\n' >&2\nexit 37\n"
	if err := os.WriteFile(executable, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestDispatchReplacesProcessAndPreservesProcessState$")
	command.Env = []string{
		"PATH=" + directory + ":/usr/bin:/bin",
		"SANDBOXCTL_DISPATCH_CHILD=1",
		"SANDBOXCTL_DISPATCH_VALUE=kept",
	}
	command.Dir = workDirectory
	command.Stdin = strings.NewReader("stdin-payload\n")
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	exitError, ok := err.(*exec.ExitError)
	if !ok || exitError.ExitCode() != 37 {
		t.Fatalf("subcommand exit = %v, want 37", err)
	}
	wantOutput := "arg-one|arg-two|kept|" + workDirectory + "\nstdin-payload\n"
	if string(output) != wantOutput {
		t.Fatalf("stdout = %q, want %q", output, wantOutput)
	}
	if stderr.String() != "child-stderr\n" {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestDispatchLooksUpExecutableThroughPATH(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, "sandboxctl-test")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory)
	var gotPath string
	err := dispatch("sandboxctl-", []string{"test"}, exec.LookPath,
		func(path string, _ []string, _ []string) error {
			gotPath = path
			return nil
		}, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != executable {
		t.Fatalf("executable = %q, want %q", gotPath, executable)
	}
}

func TestDispatchFindsCommandAndPassesArgumentsAndEnvironment(t *testing.T) {
	arguments := []string{"vm", "create", "foo", "--memory", "4G"}
	environment := []string{"PATH=/test/bin", "TOKEN=value"}
	var gotName, gotExecutable string
	var gotArgv, gotEnvironment []string
	err := dispatch("sandboxctl-", arguments,
		func(name string) (string, error) {
			gotName = name
			return "/test/bin/" + name, nil
		},
		func(path string, argv, env []string) error {
			gotExecutable = path
			gotArgv = append([]string(nil), argv...)
			gotEnvironment = append([]string(nil), env...)
			return nil
		}, environment,
	)
	if err != nil {
		t.Fatal(err)
	}
	if gotName != "sandboxctl-vm" || gotExecutable != "/test/bin/sandboxctl-vm" {
		t.Fatalf("lookup = %q, executable = %q", gotName, gotExecutable)
	}
	wantArgv := []string{"sandboxctl-vm", "create", "foo", "--memory", "4G"}
	if !reflect.DeepEqual(gotArgv, wantArgv) {
		t.Fatalf("argv = %#v, want %#v", gotArgv, wantArgv)
	}
	if !reflect.DeepEqual(gotEnvironment, environment) {
		t.Fatalf("environment = %#v, want %#v", gotEnvironment, environment)
	}
}

func TestDispatchReportsUnknownCommand(t *testing.T) {
	err := dispatch("sandboxctl-", []string{"missing"},
		func(string) (string, error) { return "", exec.ErrNotFound },
		func(string, []string, []string) error { t.Fatal("exec called for missing command"); return nil }, nil,
	)
	if err == nil || err.Error() != `unknown command "missing"` {
		t.Fatalf("error = %v", err)
	}
}

func TestDispatchReportsLookupAndExecutionErrors(t *testing.T) {
	lookupErr := errors.New("permission denied")
	err := dispatch("sandboxctl-", []string{"restricted"},
		func(string) (string, error) { return "", lookupErr },
		func(string, []string, []string) error { t.Fatal("exec called after lookup error"); return nil }, nil,
	)
	if err == nil || !strings.Contains(err.Error(), "find command \"restricted\": permission denied") {
		t.Fatalf("lookup error = %v", err)
	}

	execErr := errors.New("exec format error")
	err = dispatch("sandboxctl-", []string{"bad-format"},
		func(string) (string, error) { return "/bin/bad-format", nil },
		func(string, []string, []string) error { return execErr }, nil,
	)
	if err == nil || !strings.Contains(err.Error(), "execute command \"sandboxctl-bad-format\": exec format error") {
		t.Fatalf("execution error = %v", err)
	}
}

func TestDispatchRequiresACommand(t *testing.T) {
	err := dispatch("sandboxctl-", nil,
		func(string) (string, error) { t.Fatal("lookup called without a command"); return "", nil },
		func(string, []string, []string) error { t.Fatal("exec called without a command"); return nil }, nil,
	)
	if err == nil || err.Error() != "command is required" {
		t.Fatalf("error = %v", err)
	}
}
