// Copyright 2023 FoundryLabs, Inc.
// Derived from the E2B runtime init setup and modified for sandboxctl.
// See third_party/licenses/e2b-apache-2.0.txt.
package distro

import "fmt"

// BootSetup returns the idempotent systemd commands required by a sandbox guest.
func BootSetup(profile Profile) ([]string, error) {
	canonical, err := ForID(profile.ID)
	if err != nil {
		return nil, err
	}
	if canonical.InitSystem != InitSystemd {
		return nil, fmt.Errorf("unsupported init system %q", canonical.InitSystem)
	}
	return []string{
		"systemctl --root=/ mask serial-getty@ttyS0.service",
		"systemctl --root=/ mask systemd-networkd-wait-online.service",
		"systemctl --root=/ mask systemd-firstboot.service",
		"systemctl --root=/ enable " + canonical.SSHService + ".service",
		"systemctl --root=/ enable sandboxctl-bootstrap.service",
	}, nil
}
