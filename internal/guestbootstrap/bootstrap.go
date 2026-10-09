// Package guestbootstrap configures a guest from its Firecracker metadata.
package guestbootstrap

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	metadataAddress = "http://169.254.169.254"
	metadataLimit   = 64 << 10
	commandLimit    = 1 << 20
)

// Metadata contains the fixed guest configuration supplied by sandboxd.
type Metadata struct {
	Sandboxctl struct {
		SchemaVersion    int    `json:"schema_version"`
		Hostname         string `json:"hostname"`
		SSHUser          string `json:"ssh_user"`
		SSHAuthorizedKey string `json:"ssh_authorized_key"`
		Network          struct {
			Address   string `json:"address"`
			Gateway   string `json:"gateway"`
			Interface string `json:"interface"`
		} `json:"network"`
	} `json:"sandboxctl"`
}

// DecodeMetadata decodes and validates one bounded MMDS document.
func DecodeMetadata(data []byte) (Metadata, error) {
	if len(data) == 0 || len(data) > metadataLimit {
		return Metadata{}, errors.New("metadata size is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var metadata Metadata
	if err := decoder.Decode(&metadata); err != nil {
		return Metadata{}, fmt.Errorf("decode guest metadata: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Metadata{}, errors.New("guest metadata must contain one JSON value")
	}
	if err := Validate(metadata); err != nil {
		return Metadata{}, err
	}
	return metadata, nil
}

// Validate checks the fields that control guest identity and access.
func Validate(metadata Metadata) error {
	guest := metadata.Sandboxctl
	if guest.SchemaVersion != 1 {
		return errors.New("guest metadata schema version is unsupported")
	}
	if !validHostname(guest.Hostname) || guest.SSHUser != "dev" {
		return errors.New("guest hostname or SSH user is invalid")
	}
	if !validEd25519PublicKey(guest.SSHAuthorizedKey) {
		return errors.New("guest SSH public key is invalid")
	}
	address, err := netip.ParsePrefix(guest.Network.Address)
	if err != nil || !address.Addr().Is4() || address.Bits() != 30 {
		return errors.New("guest address must use an IPv4 /30 network")
	}
	address = address.Masked()
	guestAddress, err := netip.ParsePrefix(guest.Network.Address)
	if err != nil || guestAddress.Addr() != address.Addr().Next().Next() {
		return errors.New("guest address does not match the allocated network")
	}
	gateway, err := netip.ParseAddr(guest.Network.Gateway)
	if err != nil || !gateway.Is4() || gateway != address.Addr().Next() {
		return errors.New("guest gateway does not match the allocated network")
	}
	if guest.Network.Interface != "eth0" {
		return errors.New("guest network interface is invalid")
	}
	return nil
}

// Run fetches MMDS data and applies the fixed guest setup.
func Run(ctx context.Context) error {
	metadata, err := fetchMetadata(ctx)
	if err != nil {
		return err
	}
	return apply(ctx, metadata)
}

func fetchMetadata(parent context.Context) (Metadata, error) {
	client := &http.Client{
		Timeout:   5 * time.Second,
		Transport: &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 2 * time.Second}).DialContext},
	}
	var lastErr error
	for attempt := 0; attempt < 30; attempt++ {
		metadata, err := requestMetadata(parent, client)
		if err == nil {
			return metadata, nil
		}
		lastErr = err
		select {
		case <-parent.Done():
			return Metadata{}, parent.Err()
		case <-time.After(time.Second):
		}
	}
	return Metadata{}, fmt.Errorf("read Firecracker metadata after 30 attempts: %w", lastErr)
}

func requestMetadata(parent context.Context, client *http.Client) (Metadata, error) {
	return requestMetadataAt(parent, client, metadataAddress)
}

func requestMetadataAt(parent context.Context, client *http.Client, address string) (Metadata, error) {
	request, err := http.NewRequestWithContext(parent, http.MethodPut, address+"/latest/api/token", nil)
	if err != nil {
		return Metadata{}, err
	}
	request.Header.Set("X-metadata-token-ttl-seconds", "60")
	response, err := client.Do(request)
	if err != nil {
		return Metadata{}, fmt.Errorf("request metadata token: %w", err)
	}
	tokenBytes, readErr := io.ReadAll(io.LimitReader(response.Body, 4097))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		return Metadata{}, errors.Join(readErr, closeErr)
	}
	if response.StatusCode != http.StatusOK || len(tokenBytes) == 0 || len(tokenBytes) > 4096 {
		return Metadata{}, errors.New("metadata token response is invalid")
	}
	request, err = http.NewRequestWithContext(parent, http.MethodGet, address+"/latest/meta-data/", nil)
	if err != nil {
		return Metadata{}, err
	}
	request.Header.Set("X-metadata-token", string(tokenBytes))
	request.Header.Set("Accept", "application/json")
	response, err = client.Do(request)
	if err != nil {
		return Metadata{}, fmt.Errorf("request guest metadata: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Metadata{}, fmt.Errorf("metadata request returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, metadataLimit+1))
	if err != nil {
		return Metadata{}, fmt.Errorf("read guest metadata: %w", err)
	}
	if len(data) > metadataLimit {
		return Metadata{}, errors.New("guest metadata exceeds the size limit")
	}
	return DecodeMetadata(data)
}

func apply(ctx context.Context, metadata Metadata) error {
	guest := metadata.Sandboxctl
	if err := verifyNetwork(ctx, guest.Network); err != nil {
		return err
	}
	firstBoot, err := firstBoot("/")
	if err != nil {
		return err
	}
	uid, gid, err := ensureAccount(ctx, guest.SSHUser)
	if err != nil {
		return err
	}
	if err := ensureGuestSudo(ctx); err != nil {
		return err
	}
	if firstBoot {
		if err := removeImageHostKeys("/"); err != nil {
			return err
		}
	}
	if err := run(ctx, "/usr/bin/ssh-keygen", "-A"); err != nil {
		return fmt.Errorf("create guest SSH host keys: %w", err)
	}
	if err := configureSSH(ctx, guest.SSHAuthorizedKey, uid, gid); err != nil {
		return err
	}
	if err := writeHostname("/", guest.Hostname); err != nil {
		return err
	}
	if err := run(ctx, "/usr/bin/hostnamectl", "set-hostname", guest.Hostname); err != nil {
		return fmt.Errorf("set guest hostname: %w", err)
	}
	if err := os.MkdirAll("/var/lib/sandboxctl", 0o755); err != nil {
		return fmt.Errorf("create guest state directory: %w", err)
	}
	marker, err := os.CreateTemp("/var/lib/sandboxctl", ".bootstrap-complete-")
	if err != nil {
		return fmt.Errorf("create bootstrap marker: %w", err)
	}
	markerPath := marker.Name()
	defer os.Remove(markerPath)
	if _, err := fmt.Fprintf(marker, "schema_version=%d\nhostname=%s\n", guest.SchemaVersion, guest.Hostname); err != nil {
		_ = marker.Close()
		return fmt.Errorf("write bootstrap marker: %w", err)
	}
	if err := marker.Chmod(0o644); err != nil {
		_ = marker.Close()
		return fmt.Errorf("set bootstrap marker mode: %w", err)
	}
	if err := marker.Sync(); err != nil {
		_ = marker.Close()
		return fmt.Errorf("sync bootstrap marker: %w", err)
	}
	if err := marker.Close(); err != nil {
		return fmt.Errorf("close bootstrap marker: %w", err)
	}
	if err := os.Rename(markerPath, "/var/lib/sandboxctl/bootstrap-complete"); err != nil {
		return fmt.Errorf("publish bootstrap marker: %w", err)
	}
	return nil
}

func firstBoot(root string) (bool, error) {
	path := filepath.Join(root, "var/lib/sandboxctl/bootstrap-complete")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect bootstrap marker: %w", err)
	}
	if !info.Mode().IsRegular() {
		return false, errors.New("bootstrap marker is not a regular file")
	}
	return false, nil
}

func removeImageHostKeys(root string) error {
	paths, err := filepath.Glob(filepath.Join(root, "etc/ssh/ssh_host_*"))
	if err != nil {
		return fmt.Errorf("find image SSH host keys: %w", err)
	}
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove image SSH host key %s: %w", filepath.Base(path), err)
		}
	}
	return nil
}

func writeHostname(root, hostname string) error {
	if !validHostname(hostname) {
		return errors.New("guest hostname is invalid")
	}
	etc := filepath.Join(root, "etc")
	file, err := os.CreateTemp(etc, ".sandboxctl-hostname-")
	if err != nil {
		return fmt.Errorf("create hostname file: %w", err)
	}
	path := file.Name()
	defer os.Remove(path)
	if _, err := io.WriteString(file, hostname+"\n"); err != nil {
		_ = file.Close()
		return fmt.Errorf("write hostname file: %w", err)
	}
	if err := file.Chmod(0o644); err != nil {
		_ = file.Close()
		return fmt.Errorf("set hostname file mode: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync hostname file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close hostname file: %w", err)
	}
	if err := os.Rename(path, filepath.Join(etc, "hostname")); err != nil {
		return fmt.Errorf("publish hostname file: %w", err)
	}
	return nil
}

func ensureAccount(ctx context.Context, name string) (int, int, error) {
	output, err := commandOutput(ctx, "/usr/bin/getent", "passwd", name)
	if err != nil {
		if err := run(ctx, "/usr/sbin/useradd", "--create-home", "--home-dir", "/home/dev", "--shell", "/bin/bash", "--groups", "sudo", name); err != nil {
			return 0, 0, fmt.Errorf("create guest SSH account: %w", err)
		}
		output, err = commandOutput(ctx, "/usr/bin/getent", "passwd", name)
	}
	if err != nil {
		return 0, 0, fmt.Errorf("read guest SSH account: %w", err)
	}
	fields := strings.Split(strings.TrimSpace(string(output)), ":")
	if len(fields) != 7 || fields[0] != name || fields[5] != "/home/dev" {
		return 0, 0, errors.New("guest SSH account has an invalid home directory")
	}
	uid, uidErr := strconv.Atoi(fields[2])
	gid, gidErr := strconv.Atoi(fields[3])
	if uidErr != nil || gidErr != nil || uid < 1 || gid < 1 {
		return 0, 0, errors.New("guest SSH account has an invalid identity")
	}
	if err := run(ctx, "/usr/sbin/usermod", "--append", "--groups", "sudo", name); err != nil {
		return 0, 0, fmt.Errorf("add guest SSH account to sudo group: %w", err)
	}
	if err := os.MkdirAll(fields[5]+"/.ssh", 0o700); err != nil {
		return 0, 0, fmt.Errorf("create guest SSH directory: %w", err)
	}
	if err := os.Chown(fields[5], uid, gid); err != nil {
		return 0, 0, fmt.Errorf("set guest home ownership: %w", err)
	}
	if err := os.Chown(fields[5]+"/.ssh", uid, gid); err != nil {
		return 0, 0, fmt.Errorf("set guest SSH directory ownership: %w", err)
	}
	if err := os.Chmod(fields[5]+"/.ssh", 0o700); err != nil {
		return 0, 0, fmt.Errorf("set guest SSH directory mode: %w", err)
	}
	randomPassword, err := commandOutput(ctx, "/usr/bin/openssl", "rand", "-hex", "32")
	if err != nil {
		return 0, 0, fmt.Errorf("create guest account password hash: %w", err)
	}
	passwordHash, err := commandOutput(ctx, "/usr/bin/openssl", "passwd", "-6", strings.TrimSpace(string(randomPassword)))
	if err != nil {
		return 0, 0, fmt.Errorf("create guest account password hash: %w", err)
	}
	if err := run(ctx, "/usr/sbin/usermod", "--password", strings.TrimSpace(string(passwordHash)), name); err != nil {
		return 0, 0, fmt.Errorf("set guest account password hash: %w", err)
	}
	return uid, gid, nil
}

func ensureGuestSudo(ctx context.Context) error {
	if !executableFile("/usr/bin/sudo") || !executableFile("/usr/sbin/visudo") {
		return nil
	}
	if err := os.MkdirAll("/etc/sudoers.d", 0o755); err != nil {
		return fmt.Errorf("create guest sudoers directory: %w", err)
	}
	path := "/etc/sudoers.d/90-sandboxctl-dev"
	if err := os.WriteFile(path, []byte("dev ALL=(ALL) NOPASSWD: ALL\n"), 0o440); err != nil {
		return fmt.Errorf("write guest sudo policy: %w", err)
	}
	if err := os.Chmod(path, 0o440); err != nil {
		return fmt.Errorf("set guest sudo policy mode: %w", err)
	}
	if err := run(ctx, "/usr/sbin/visudo", "-cf", path); err != nil {
		return fmt.Errorf("validate guest sudo policy: %w", err)
	}
	return nil
}

func configureSSH(ctx context.Context, publicKey string, uid, gid int) error {
	configDir := "/etc/ssh/sshd_config.d"
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return fmt.Errorf("create SSH configuration directory: %w", err)
	}
	mainConfig := "/etc/ssh/sshd_config"
	config, err := os.ReadFile(mainConfig)
	if err != nil {
		return fmt.Errorf("read SSH server configuration: %w", err)
	}
	if !strings.Contains(string(config), "/etc/ssh/sshd_config.d/") {
		config = append([]byte("Include /etc/ssh/sshd_config.d/*.conf\n"), config...)
		if err := os.WriteFile(mainConfig, config, 0o644); err != nil {
			return fmt.Errorf("enable SSH configuration fragments: %w", err)
		}
	}
	dropin := []byte("PermitRootLogin no\nPermitEmptyPasswords no\nPasswordAuthentication no\nKbdInteractiveAuthentication no\nPubkeyAuthentication yes\n")
	if err := os.WriteFile(filepath.Join(configDir, "00-sandboxctl.conf"), dropin, 0o644); err != nil {
		return fmt.Errorf("write SSH authentication policy: %w", err)
	}
	if err := os.MkdirAll("/run/sshd", 0o755); err != nil {
		return fmt.Errorf("create SSH runtime directory: %w", err)
	}
	if err := run(ctx, "/usr/sbin/sshd", "-t"); err != nil {
		return fmt.Errorf("validate SSH configuration: %w", err)
	}
	home := "/home/dev/.ssh"
	file, err := os.CreateTemp(home, ".authorized_keys-")
	if err != nil {
		return fmt.Errorf("create authorized key file: %w", err)
	}
	keyPath := file.Name()
	defer os.Remove(keyPath)
	if _, err := io.WriteString(file, strings.TrimSpace(publicKey)+"\n"); err != nil {
		_ = file.Close()
		return fmt.Errorf("write authorized key: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("set authorized key mode: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync authorized key: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close authorized key: %w", err)
	}
	if err := os.Chown(keyPath, uid, gid); err != nil {
		return fmt.Errorf("set authorized key ownership: %w", err)
	}
	if err := os.Rename(keyPath, filepath.Join(home, "authorized_keys")); err != nil {
		return fmt.Errorf("publish authorized key: %w", err)
	}
	return nil
}

func verifyNetwork(ctx context.Context, network struct {
	Address   string `json:"address"`
	Gateway   string `json:"gateway"`
	Interface string `json:"interface"`
}) error {
	address, err := commandOutput(ctx, "/usr/sbin/ip", "-o", "-4", "address", "show", "dev", network.Interface)
	if err != nil {
		return fmt.Errorf("read guest IPv4 address: %w", err)
	}
	if !strings.Contains(string(address), " "+network.Address+" ") {
		return errors.New("guest IPv4 address does not match metadata")
	}
	route, err := commandOutput(ctx, "/usr/sbin/ip", "route", "show", "default", "dev", network.Interface)
	if err != nil {
		return fmt.Errorf("read guest default route: %w", err)
	}
	if !strings.Contains(string(route), "via "+network.Gateway) {
		return errors.New("guest default route does not match metadata")
	}
	return nil
}

func commandOutput(ctx context.Context, program string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, program, args...)
	output, err := command.CombinedOutput()
	if len(output) > commandLimit {
		return nil, errors.New("guest command output exceeds the size limit")
	}
	if err != nil {
		return nil, fmt.Errorf("%s failed: %w: %s", filepath.Base(program), err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

func run(ctx context.Context, program string, args ...string) error {
	_, err := commandOutput(ctx, program, args...)
	return err
}

func executableFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}

func validHostname(value string) bool {
	if len(value) < 1 || len(value) > 63 || value[0] == '-' || value[0] == '.' || value[len(value)-1] == '-' || value[len(value)-1] == '.' {
		return false
	}
	previous := byte('.')
	for _, char := range value {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '.') {
			return false
		}
		if (char == '-' && (previous == '.' || previous == '-')) || (char == '.' && (previous == '.' || previous == '-')) {
			return false
		}
		previous = byte(char)
	}
	return true
}

func validEd25519PublicKey(value string) bool {
	fields := strings.Fields(value)
	if len(fields) != 2 && len(fields) != 3 || fields[0] != "ssh-ed25519" {
		return false
	}
	data, err := base64.StdEncoding.DecodeString(fields[1])
	return err == nil && len(data) == 51 && data[3] == 11 && string(data[4:15]) == "ssh-ed25519"
}

// MetadataURL returns the fixed link-local metadata endpoint for diagnostics.
func MetadataURL() string { return metadataAddress }

// NewHTTPClient creates the bounded client used for local metadata requests.
