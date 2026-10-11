// Copyright 2023 FoundryLabs, Inc.
// Derived from the E2B runtime distro profile and modified for sandboxctl.
// See third_party/licenses/e2b-apache-2.0.txt.
//
// Package distro selects the supported guest provisioning profile from
// declared /etc/os-release data.
package distro

import (
	"bufio"
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// InitSystem identifies the guest init system.
type InitSystem string

const (
	// InitSystemd identifies systemd-based guests.
	InitSystemd InitSystem = "systemd"
)

// Profile defines the package and boot contract for a guest distribution.
type Profile struct {
	ID             string
	Packages       []string
	PackageQuery   string
	PackageInstall string
	InitSystem     InitSystem
	InitBinary     string
	SSHService     string
	AdminGroup     string
	CABundle       string
	CARefresh      string
}

var debianPackages = []string{
	"systemd",
	"systemd-sysv",
	"openssh-server",
	"sudo",
	"ca-certificates",
	"git",
	"iproute2",
	"iputils-ping",
	"curl",
	"jq",
	"openssl",
}

// ForID returns a copy of the provisioning profile for a supported os-release ID.
func ForID(id string) (Profile, error) {
	profile := Profile{
		Packages:       append([]string(nil), debianPackages...),
		PackageQuery:   `dpkg-query -W -f='${Status}' "$1" 2>/dev/null | grep -q "install ok installed"`,
		PackageInstall: `apt-get -q update && DEBIAN_FRONTEND=noninteractive DEBCONF_NOWARNINGS=yes apt-get -qq -o=Dpkg::Use-Pty=0 install -y --no-install-recommends "$@"`,
		InitSystem:     InitSystemd,
		InitBinary:     "/lib/systemd/systemd",
		SSHService:     "ssh",
		AdminGroup:     "sudo",
		CABundle:       "/etc/ssl/certs/ca-certificates.crt",
		CARefresh:      "update-ca-certificates",
	}

	switch strings.ToLower(id) {
	case "debian", "ubuntu":
		profile.ID = strings.ToLower(id)
		return profile, nil
	default:
		return Profile{}, fmt.Errorf("unsupported guest distribution ID %q; supported IDs are debian and ubuntu", id)
	}
}

// ParseOSRelease parses the assignment format used by /etc/os-release.
func ParseOSRelease(data []byte) (map[string]string, error) {
	values := make(map[string]string)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, raw, ok := strings.Cut(line, "=")
		if !ok || !validKey(key) {
			return nil, fmt.Errorf("invalid os-release assignment on line %d", lineNumber)
		}
		if _, exists := values[key]; exists {
			return nil, fmt.Errorf("duplicate os-release key %q on line %d", key, lineNumber)
		}
		value, err := parseValue(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid os-release value for %s on line %d: %w", key, lineNumber, err)
		}
		values[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read os-release: %w", err)
	}
	return values, nil
}

func validKey(key string) bool {
	if key == "" {
		return false
	}
	for index, char := range key {
		if index == 0 && char != '_' && (char < 'A' || char > 'Z') {
			return false
		}
		if (char < 'A' || char > 'Z') && char != '_' && (char < '0' || char > '9') {
			return false
		}
	}
	return true
}

func parseValue(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	switch raw[0] {
	case '\'':
		if len(raw) < 2 || raw[len(raw)-1] != '\'' {
			return "", fmt.Errorf("unterminated single-quoted value")
		}
		return raw[1 : len(raw)-1], nil
	case '"':
		value, err := strconv.Unquote(raw)
		if err != nil {
			return "", err
		}
		return value, nil
	default:
		if strings.ContainsAny(raw, " \t\"'\\") {
			return "", fmt.Errorf("unquoted value contains whitespace or quotes")
		}
		return raw, nil
	}
}

// FromOSRelease selects a profile from parsed /etc/os-release values.
func FromOSRelease(values map[string]string) (Profile, error) {
	id := strings.TrimSpace(values["ID"])
	if id == "" {
		return Profile{}, fmt.Errorf("os-release does not declare ID")
	}
	return ForID(id)
}
