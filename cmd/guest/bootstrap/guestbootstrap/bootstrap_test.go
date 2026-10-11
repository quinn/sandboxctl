package guestbootstrap

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecodeMetadataValidatesGuestIdentityAndNetwork(t *testing.T) {
	metadata := validMetadata(t)
	data, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeMetadata(data)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Sandboxctl.Hostname != "builder-01" || decoded.Sandboxctl.SSHUser != "dev" || decoded.Sandboxctl.Network.Address != "172.30.0.2/30" {
		t.Fatalf("decoded metadata has unexpected fields: %#v", decoded.Sandboxctl)
	}
}

func TestDecodeMetadataRejectsInvalidFields(t *testing.T) {
	base := validMetadata(t)
	tests := []struct {
		name   string
		change func(*Metadata)
	}{
		{name: "schema", change: func(m *Metadata) { m.Sandboxctl.SchemaVersion = 2 }},
		{name: "hostname label", change: func(m *Metadata) { m.Sandboxctl.Hostname = "builder-.bad" }},
		{name: "SSH user", change: func(m *Metadata) { m.Sandboxctl.SSHUser = "root" }},
		{name: "key type", change: func(m *Metadata) { m.Sandboxctl.SSHAuthorizedKey = "ssh-rsa AAAA" }},
		{name: "guest address", change: func(m *Metadata) { m.Sandboxctl.Network.Address = "172.30.0.1/30" }},
		{name: "gateway", change: func(m *Metadata) { m.Sandboxctl.Network.Gateway = "172.30.0.2" }},
		{name: "interface", change: func(m *Metadata) { m.Sandboxctl.Network.Interface = "eth1" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			metadata := base
			test.change(&metadata)
			data, err := json.Marshal(metadata)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeMetadata(data); err == nil {
				t.Fatal("DecodeMetadata() accepted invalid metadata")
			}
		})
	}
	if _, err := DecodeMetadata([]byte(`{"sandboxctl":{},"unknown":true}`)); err == nil {
		t.Fatal("DecodeMetadata() accepted an unknown field")
	}
	if _, err := DecodeMetadata(make([]byte, metadataLimit+1)); err == nil {
		t.Fatal("DecodeMetadata() accepted oversized metadata")
	}
}

func TestRequestMetadataUsesTokenAndBoundsResponse(t *testing.T) {
	metadata := validMetadata(t)
	data, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/latest/api/token":
			if request.Method != http.MethodPut || request.Header.Get("X-metadata-token-ttl-seconds") != "60" {
				return response(http.StatusBadRequest, "invalid token request"), nil
			}
			return response(http.StatusOK, "test-token"), nil
		case "/latest/meta-data/":
			if request.Method != http.MethodGet || request.Header.Get("X-metadata-token") != "test-token" || request.Header.Get("Accept") != "application/json" {
				return response(http.StatusBadRequest, "invalid metadata request"), nil
			}
			return response(http.StatusOK, string(data)), nil
		default:
			return response(http.StatusNotFound, "not found"), nil
		}
	})
	client := &http.Client{Transport: transport}

	got, err := requestMetadataAt(context.Background(), client, "http://metadata.invalid")
	if err != nil {
		t.Fatal(err)
	}
	if got.Sandboxctl.SSHAuthorizedKey != metadata.Sandboxctl.SSHAuthorizedKey {
		t.Fatal("metadata request returned a different SSH key")
	}
}

func TestFirstBootRemovesImageHostKeysAndWritesHostname(t *testing.T) {
	root := t.TempDir()
	sshDir := filepath.Join(root, "etc/ssh")
	if err := os.MkdirAll(sshDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "etc/hosts"), []byte("127.0.0.1 localhost\n127.0.1.1 ubuntu-fc-uvm\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(sshDir, "ssh_host_ed25519_key")
	if err := os.WriteFile(keyPath, []byte("image-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := firstBoot(root)
	if err != nil || !first {
		t.Fatalf("firstBoot() = %t, %v; want true", first, err)
	}
	if err := removeImageHostKeys(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Fatalf("image host key still exists: %v", err)
	}
	if err := writeHostname(root, "sandbox-test"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, "etc/hostname"))
	if err != nil || string(got) != "sandbox-test\n" {
		t.Fatalf("hostname file = %q, %v", got, err)
	}
	hosts, err := os.ReadFile(filepath.Join(root, "etc/hosts"))
	if err != nil || !strings.Contains(string(hosts), "127.0.1.1\tsandbox-test\n") {
		t.Fatalf("hosts file = %q, %v", hosts, err)
	}
	if err := os.MkdirAll(filepath.Join(root, "var/lib/sandboxctl"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "var/lib/sandboxctl/bootstrap-complete"), []byte("complete"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err = firstBoot(root)
	if err != nil || first {
		t.Fatalf("firstBoot() after marker = %t, %v; want false", first, err)
	}
}

func TestConfigureDNSReplacesSystemdStubSymlink(t *testing.T) {
	root := t.TempDir()
	etc := filepath.Join(root, "etc")
	stub := filepath.Join(root, "run/systemd/resolve/stub-resolv.conf")
	if err := os.MkdirAll(filepath.Dir(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(etc, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stub, []byte("nameserver ::1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	resolver := filepath.Join(etc, "resolv.conf")
	if err := os.Symlink("../run/systemd/resolve/stub-resolv.conf", resolver); err != nil {
		t.Fatal(err)
	}
	if err := configureDNS(root); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(resolver)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o644 {
		t.Fatalf("resolver file mode = %v, want regular mode 0644", info.Mode())
	}
	data, err := os.ReadFile(resolver)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != guestResolver {
		t.Fatalf("resolver contents = %q, want %q", data, guestResolver)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func validMetadata(t *testing.T) Metadata {
	t.Helper()
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 0, 51)
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], 11)
	key = append(key, size[:]...)
	key = append(key, "ssh-ed25519"...)
	binary.BigEndian.PutUint32(size[:], uint32(len(publicKey)))
	key = append(key, size[:]...)
	key = append(key, publicKey...)
	metadata := Metadata{}
	metadata.Sandboxctl.SchemaVersion = 1
	metadata.Sandboxctl.Hostname = "builder-01"
	metadata.Sandboxctl.SSHUser = "dev"
	metadata.Sandboxctl.SSHAuthorizedKey = "ssh-ed25519 " + base64.StdEncoding.EncodeToString(key)
	metadata.Sandboxctl.Network.Address = "172.30.0.2/30"
	metadata.Sandboxctl.Network.Gateway = "172.30.0.1"
	metadata.Sandboxctl.Network.Interface = "eth0"
	return metadata
}
