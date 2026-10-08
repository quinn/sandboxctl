package runtime

import (
	"encoding/base64"
	"testing"

	firecracker "github.com/firecracker-microvm/firecracker-go-sdk"
	"sandboxctl/internal/hoststate"
)

func TestBuildGuestMetadataContainsInstanceKeyAndNetwork(t *testing.T) {
	instance := supervisorTestInstance()
	metadata, err := BuildGuestMetadata(instance, supervisorTestPublicKey())
	if err != nil {
		t.Fatal(err)
	}
	guest := metadata.Sandboxctl
	if guest.SchemaVersion != 1 || guest.Hostname != instance.Name || guest.SSHUser != "dev" {
		t.Fatalf("guest identity = %#v", guest)
	}
	if guest.SSHAuthorizedKey != supervisorTestPublicKey() {
		t.Fatalf("SSH key = %q", guest.SSHAuthorizedKey)
	}
	if guest.Network.Address != "172.30.0.2/30" || guest.Network.Gateway != "172.30.0.1" || guest.Network.Interface != "eth0" {
		t.Fatalf("guest network = %#v", guest.Network)
	}
}

func TestAddGuestMetadataHandlerRunsAfterMMDSConfiguration(t *testing.T) {
	machine := &firecracker.Machine{Handlers: firecracker.Handlers{
		FcInit: firecracker.HandlerList{}.Append(firecracker.ConfigMmdsHandler),
	}}
	if err := addGuestMetadataHandler(machine, GuestMetadata{}); err != nil {
		t.Fatal(err)
	}
	if !machine.Handlers.FcInit.Has(firecracker.ConfigMmdsHandlerName) || !machine.Handlers.FcInit.Has(firecracker.SetMetadataHandlerName) {
		t.Fatal("machine handlers do not configure MMDS and then set guest metadata")
	}
}

func TestAddGuestMetadataHandlerRejectsMissingMMDSConfiguration(t *testing.T) {
	machine := &firecracker.Machine{}
	if err := addGuestMetadataHandler(machine, GuestMetadata{}); err == nil {
		t.Fatal("metadata handler was accepted without MMDS configuration")
	}
}

func TestBuildGuestMetadataRejectsInvalidNetworkAndKey(t *testing.T) {
	tests := []struct {
		name   string
		change func(*hoststate.Instance, *string)
	}{
		{name: "bad subnet", change: func(instance *hoststate.Instance, _ *string) { instance.Subnet = "172.30.0.0/24" }},
		{name: "guest outside subnet", change: func(instance *hoststate.Instance, _ *string) { instance.GuestIP = "172.30.1.2" }},
		{name: "gateway equals guest", change: func(instance *hoststate.Instance, _ *string) { instance.HostIP = instance.GuestIP }},
		{name: "multiline key", change: func(_ *hoststate.Instance, key *string) { *key += "\nssh-ed25519 invalid" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			instance := supervisorTestInstance()
			key := supervisorTestPublicKey()
			test.change(&instance, &key)
			if _, err := BuildGuestMetadata(instance, key); err == nil {
				t.Fatal("BuildGuestMetadata() accepted invalid input")
			}
		})
	}
}

func supervisorTestPublicKey() string {
	data := append([]byte{0, 0, 0, 11}, []byte("ssh-ed25519")...)
	data = append(data, 0, 0, 0, 32)
	data = append(data, make([]byte, 32)...)
	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString(data) + " test"
}
