package network

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"sandboxctl/internal/hoststate"
)

type commandResult struct {
	output string
	err    error
}

type fakeRunner struct {
	results map[string]commandResult
	calls   [][]string
}

func (runner *fakeRunner) Run(_ context.Context, path string, arguments ...string) ([]byte, error) {
	call := append([]string{path}, arguments...)
	runner.calls = append(runner.calls, call)
	result, ok := runner.results[strings.Join(arguments, " ")]
	if !ok {
		return nil, errors.New("unexpected command")
	}
	return []byte(result.output), result.err
}

func TestTapManagerPrepareCreatesAndConfiguresTap(t *testing.T) {
	runner := &fakeRunner{results: map[string]commandResult{
		"tuntap add dev sc000001 mode tap user 200001 group 300001": {},
		"address replace 172.30.0.1/30 dev sc000001":                {},
		"link set dev sc000001 up":                                  {},
	}}
	manager := TapManager{Runner: runner}
	if err := manager.Prepare(context.Background(), testInstance()); err != nil {
		t.Fatalf("Prepare(): %v", err)
	}
	want := [][]string{
		{"/usr/bin/ip", "tuntap", "add", "dev", "sc000001", "mode", "tap", "user", "200001", "group", "300001"},
		{"/usr/bin/ip", "address", "replace", "172.30.0.1/30", "dev", "sc000001"},
		{"/usr/bin/ip", "link", "set", "dev", "sc000001", "up"},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("commands = %#v, want %#v", runner.calls, want)
	}
}

func TestTapManagerPrepareReusesOwnedTapWithoutAddress(t *testing.T) {
	runner := &fakeRunner{results: map[string]commandResult{
		"tuntap add dev sc000001 mode tap user 200001 group 300001": {err: errors.New("File exists")},
		"tuntap show dev sc000001":                                  {output: "tailscale0: tun vnet_hdr\nsc000001: tap persist user 200001 group 300001\n"},
		"-o -4 address show dev sc000001":                           {},
		"address replace 172.30.0.1/30 dev sc000001":                {},
		"link set dev sc000001 up":                                  {},
	}}
	if err := (TapManager{Runner: runner}).Prepare(context.Background(), testInstance()); err != nil {
		t.Fatalf("Prepare(): %v", err)
	}
}

func TestTapManagerRejectsInvalidAddressesBeforeCommands(t *testing.T) {
	runner := &fakeRunner{results: map[string]commandResult{}}
	instance := testInstance()
	instance.HostIP = "not-an-address"
	if err := (TapManager{Runner: runner}).Prepare(context.Background(), instance); err == nil {
		t.Fatal("Prepare() accepted an invalid host address")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("commands ran for invalid input: %#v", runner.calls)
	}
}

func TestTapManagerRejectsTapOwnedByAnotherUID(t *testing.T) {
	runner := &fakeRunner{results: map[string]commandResult{
		"tuntap add dev sc000001 mode tap user 200001 group 300001": {err: errors.New("File exists")},
		"tuntap show dev sc000001":                                  {output: "sc000001: tap persist user 200002 group 300001\n"},
	}}
	if err := (TapManager{Runner: runner}).Prepare(context.Background(), testInstance()); err == nil {
		t.Fatal("Prepare() accepted a TAP with a different owner")
	}
}

func TestTapManagerRemovesTapWhenAddressSetupFails(t *testing.T) {
	runner := &fakeRunner{results: map[string]commandResult{
		"tuntap add dev sc000001 mode tap user 200001 group 300001": {},
		"address replace 172.30.0.1/30 dev sc000001":                {err: errors.New("address rejected")},
		"tuntap del dev sc000001 mode tap":                          {},
	}}
	if err := (TapManager{Runner: runner}).Prepare(context.Background(), testInstance()); err == nil {
		t.Fatal("Prepare() accepted a failed address setup")
	}
	if got := runner.calls[len(runner.calls)-1]; !reflect.DeepEqual(got, []string{"/usr/bin/ip", "tuntap", "del", "dev", "sc000001", "mode", "tap"}) {
		t.Fatalf("rollback command = %#v", got)
	}
}

func TestTapManagerRemoveDeletesMatchingTap(t *testing.T) {
	runner := &fakeRunner{results: map[string]commandResult{
		"tuntap show dev sc000001":         {output: "sc000001: tap persist user 200001 group 300001\n"},
		"-o -4 address show dev sc000001":  {output: "5: sc000001 inet 172.30.0.1/30 scope global sc000001\n"},
		"tuntap del dev sc000001 mode tap": {},
	}}
	if err := (TapManager{Runner: runner}).Remove(context.Background(), testInstance()); err != nil {
		t.Fatalf("Remove(): %v", err)
	}
	if got := runner.calls[len(runner.calls)-1]; !reflect.DeepEqual(got, []string{"/usr/bin/ip", "tuntap", "del", "dev", "sc000001", "mode", "tap"}) {
		t.Fatalf("delete command = %#v", got)
	}
}

func TestTapManagerRemoveAllowsPartiallyConfiguredTap(t *testing.T) {
	runner := &fakeRunner{results: map[string]commandResult{
		"tuntap show dev sc000001":         {output: "sc000001: tap persist user 200001 group 300001\n"},
		"-o -4 address show dev sc000001":  {},
		"tuntap del dev sc000001 mode tap": {},
	}}
	if err := (TapManager{Runner: runner}).Remove(context.Background(), testInstance()); err != nil {
		t.Fatalf("Remove(): %v", err)
	}
}

func testInstance() hoststate.Instance {
	return hoststate.Instance{
		TapDevice: "sc000001", HostUID: 200001, HostGID: 300001,
		Subnet: "172.30.0.0/30", HostIP: "172.30.0.1", GuestIP: "172.30.0.2",
	}
}
