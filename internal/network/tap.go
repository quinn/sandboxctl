// Package network manages host network devices for sandboxd.
package network

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"sandboxctl/internal/hoststate"
)

var tapNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,15}$`)

// CommandRunner runs one absolute host command without a shell.
type CommandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

// ExecRunner runs host commands with a fixed environment.
type ExecRunner struct{}

// Run executes path with arguments and a fixed system environment.
func (ExecRunner) Run(ctx context.Context, path string, arguments ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, path, arguments...)
	command.Env = []string{"PATH=/usr/bin:/usr/sbin:/bin:/sbin", "LANG=C", "LC_ALL=C"}
	return command.CombinedOutput()
}

// TapManager prepares and removes TAP devices for sandbox instances.
type TapManager struct {
	Runner CommandRunner
}

// Prepare creates or validates the instance TAP and assigns its host address.
func (manager TapManager) Prepare(ctx context.Context, instance hoststate.Instance) error {
	if err := validateInstanceNetwork(instance); err != nil {
		return err
	}
	runner := manager.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	_, addErr := runner.Run(ctx, "/usr/bin/ip", "tuntap", "add", "dev", instance.TapDevice, "mode", "tap", "user", strconv.Itoa(instance.HostUID), "group", strconv.Itoa(instance.HostGID))
	if addErr != nil {
		if err := manager.verifyExisting(ctx, runner, instance); err != nil {
			return errors.Join(fmt.Errorf("create TAP device %s: %w", instance.TapDevice, addErr), err)
		}
	}
	rollback := func(cause error) error {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, cleanupErr := runner.Run(cleanupCtx, "/usr/bin/ip", "tuntap", "del", "dev", instance.TapDevice, "mode", "tap")
		if cleanupErr != nil && !isMissingDevice(cleanupErr) {
			cause = errors.Join(cause, fmt.Errorf("remove incomplete TAP device %s: %w", instance.TapDevice, cleanupErr))
		}
		return cause
	}
	prefix, _ := netip.ParsePrefix(instance.Subnet)
	address := netip.MustParseAddr(instance.HostIP).String() + "/" + strconv.Itoa(prefix.Bits())
	if _, err := runner.Run(ctx, "/usr/bin/ip", "address", "replace", address, "dev", instance.TapDevice); err != nil {
		return rollback(fmt.Errorf("assign host address to TAP device %s: %w", instance.TapDevice, err))
	}
	if _, err := runner.Run(ctx, "/usr/bin/ip", "link", "set", "dev", instance.TapDevice, "up"); err != nil {
		return rollback(fmt.Errorf("set TAP device %s up: %w", instance.TapDevice, err))
	}
	return nil
}

// Remove removes the instance TAP after its Firecracker process has stopped.
func (manager TapManager) Remove(ctx context.Context, instance hoststate.Instance) error {
	if err := validateInstanceNetwork(instance); err != nil {
		return err
	}
	runner := manager.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	if err := manager.verifyExisting(ctx, runner, instance); err != nil {
		if isMissingDevice(err) {
			return nil
		}
		return err
	}
	if _, err := runner.Run(ctx, "/usr/bin/ip", "tuntap", "del", "dev", instance.TapDevice, "mode", "tap"); err != nil && !isMissingDevice(err) {
		return fmt.Errorf("remove TAP device %s: %w", instance.TapDevice, err)
	}
	return nil
}

func (manager TapManager) verifyExisting(ctx context.Context, runner CommandRunner, instance hoststate.Instance) error {
	output, err := runner.Run(ctx, "/usr/bin/ip", "tuntap", "show", "dev", instance.TapDevice)
	if err != nil {
		return fmt.Errorf("inspect TAP device %s: %w", instance.TapDevice, err)
	}
	uid, gid, ok := parseTapOwner(string(output), instance.TapDevice)
	if !ok || uid != instance.HostUID || gid != instance.HostGID {
		slog.Error("existing TAP owner does not match allocation",
			"device", instance.TapDevice,
			"expected_uid", instance.HostUID,
			"expected_gid", instance.HostGID,
			"parsed_uid", uid,
			"parsed_gid", gid,
			"recognized", ok,
			"output", strings.TrimSpace(string(output)),
		)
		return fmt.Errorf("TAP device %s does not match the allocated owner", instance.TapDevice)
	}
	addressOutput, err := runner.Run(ctx, "/usr/bin/ip", "-o", "-4", "address", "show", "dev", instance.TapDevice)
	if err != nil && !isMissingDevice(err) {
		return fmt.Errorf("inspect TAP address %s: %w", instance.TapDevice, err)
	}
	if err == nil && hasUnexpectedAddress(string(addressOutput), instance) {
		return fmt.Errorf("TAP device %s has an unexpected host address", instance.TapDevice)
	}
	return nil
}

func validateInstanceNetwork(instance hoststate.Instance) error {
	if !tapNamePattern.MatchString(instance.TapDevice) || instance.HostUID < 1 || instance.HostGID < 1 {
		return errors.New("sandbox TAP identity is invalid")
	}
	prefix, err := netip.ParsePrefix(instance.Subnet)
	if err != nil || !prefix.Addr().Is4() || prefix.Bits() != 30 || prefix.Masked() != prefix {
		return errors.New("sandbox network must use an aligned IPv4 /30 subnet")
	}
	host, guest := prefix.Addr().Next(), prefix.Addr().Next().Next()
	gotHost, err := netip.ParseAddr(instance.HostIP)
	if err != nil || gotHost != host {
		return errors.New("sandbox host address does not match its /30 subnet")
	}
	gotGuest, err := netip.ParseAddr(instance.GuestIP)
	if err != nil || gotGuest != guest {
		return errors.New("sandbox addresses do not match its /30 subnet")
	}
	return nil
}

func parseTapOwner(output, name string) (int, int, bool) {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 || strings.TrimSuffix(fields[0], ":") != name {
			continue
		}
		mode, user, group := false, -1, -1
		for index := 1; index < len(fields); index++ {
			switch fields[index] {
			case "tap":
				mode = true
			case "user":
				if index+1 >= len(fields) {
					return 0, 0, false
				}
				value, err := strconv.Atoi(fields[index+1])
				if err != nil {
					return 0, 0, false
				}
				user = value
			case "group":
				if index+1 >= len(fields) {
					return 0, 0, false
				}
				value, err := strconv.Atoi(fields[index+1])
				if err != nil {
					return 0, 0, false
				}
				group = value
			}
		}
		return user, group, mode && user >= 0 && group >= 0
	}
	return 0, 0, false
}

func hasUnexpectedAddress(output string, instance hoststate.Instance) bool {
	prefix, _ := netip.ParsePrefix(instance.Subnet)
	want, _ := netip.ParseAddr(instance.HostIP)
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		for index, field := range fields {
			if field == "inet" && index+1 < len(fields) {
				address, err := netip.ParsePrefix(fields[index+1])
				if err != nil || address.Addr() != want || address.Bits() != prefix.Bits() {
					return true
				}
			}
		}
	}
	return false
}

func isMissingDevice(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "does not exist") || strings.Contains(message, "cannot find device")
}
