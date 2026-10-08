package runtime

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"

	firecracker "github.com/firecracker-microvm/firecracker-go-sdk"
	"sandboxctl/internal/hoststate"
)

// BuildGuestMetadata validates and builds the data supplied to the guest through MMDS.
func BuildGuestMetadata(instance hoststate.Instance, publicKey string) (GuestMetadata, error) {
	if instance.Name == "" || instance.SSHUser != "dev" {
		return GuestMetadata{}, errors.New("sandbox hostname or SSH user is invalid")
	}
	key := strings.TrimSpace(publicKey)
	if !strings.HasPrefix(key, "ssh-ed25519 ") || strings.ContainsAny(key, "\r\n") {
		return GuestMetadata{}, errors.New("guest SSH public key is invalid")
	}
	pool, err := netip.ParsePrefix(instance.Subnet)
	if err != nil || !pool.Addr().Is4() || pool.Bits() != 30 {
		return GuestMetadata{}, errors.New("sandbox network must be an IPv4 /30 subnet")
	}
	pool = pool.Masked()
	guest, err := netip.ParseAddr(instance.GuestIP)
	if err != nil || !guest.Is4() || guest != pool.Addr().Next().Next() {
		return GuestMetadata{}, errors.New("guest address does not match the sandbox allocation")
	}
	gateway, err := netip.ParseAddr(instance.HostIP)
	if err != nil || !gateway.Is4() || gateway != pool.Addr().Next() {
		return GuestMetadata{}, errors.New("host gateway does not match the sandbox allocation")
	}
	metadata := GuestMetadata{}
	metadata.Sandboxctl.SchemaVersion = 1
	metadata.Sandboxctl.Hostname = instance.Name
	metadata.Sandboxctl.SSHUser = instance.SSHUser
	metadata.Sandboxctl.SSHAuthorizedKey = key
	metadata.Sandboxctl.Network.Address = fmt.Sprintf("%s/%d", guest, pool.Bits())
	metadata.Sandboxctl.Network.Gateway = gateway.String()
	metadata.Sandboxctl.Network.Interface = "eth0"
	return metadata, nil
}

func guestIPConfiguration(instance hoststate.Instance) (*net.IPNet, net.IP, error) {
	pool, err := netip.ParsePrefix(instance.Subnet)
	if err != nil || !pool.Addr().Is4() || pool.Bits() != 30 {
		return nil, nil, errors.New("sandbox network must be an IPv4 /30 subnet")
	}
	pool = pool.Masked()
	guest, err := netip.ParseAddr(instance.GuestIP)
	if err != nil || !guest.Is4() || guest != pool.Addr().Next().Next() {
		return nil, nil, errors.New("guest address does not match the sandbox allocation")
	}
	gateway, err := netip.ParseAddr(instance.HostIP)
	if err != nil || !gateway.Is4() || gateway != pool.Addr().Next() {
		return nil, nil, errors.New("host gateway does not match the sandbox allocation")
	}
	mask := net.CIDRMask(pool.Bits(), 32)
	return &net.IPNet{IP: net.IP(guest.AsSlice()), Mask: mask}, net.IP(gateway.AsSlice()), nil
}

func addGuestMetadataHandler(machine *firecracker.Machine, metadata GuestMetadata) error {
	if !machine.Handlers.FcInit.Has(firecracker.ConfigMmdsHandlerName) {
		return errors.New("Firecracker SDK does not configure MMDS")
	}
	machine.Handlers.FcInit = machine.Handlers.FcInit.AppendAfter(
		firecracker.ConfigMmdsHandlerName,
		firecracker.NewSetMetadataHandler(metadata),
	)
	return nil
}
