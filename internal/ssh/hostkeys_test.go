package ssh

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"sandboxctl/internal/config"
	"sandboxctl/internal/forward"

	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func TestForwardPreferencesUseKnownHostKeys(t *testing.T) {
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ecKey, err := gossh.NewSignerFromKey(ec)
	if err != nil {
		t.Fatal(err)
	}
	rsa, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	rsaKey, err := gossh.NewSignerFromKey(rsa)
	if err != nil {
		t.Fatal(err)
	}
	ed := forwardTestSigner(t)
	alias := "sandbox-instance-0123456789abcdef0123456789abcdef"
	for _, test := range []struct {
		name  string
		host  string
		keys  []gossh.PublicKey
		first []string
	}{
		{name: "unknown", host: alias},
		{name: "Ed25519", host: alias, keys: []gossh.PublicKey{ed.PublicKey()}, first: []string{gossh.KeyAlgoED25519}},
		{name: "ECDSA", host: alias, keys: []gossh.PublicKey{ecKey.PublicKey()}, first: []string{gossh.KeyAlgoECDSA256}},
		{name: "RSA", host: alias, keys: []gossh.PublicKey{rsaKey.PublicKey()}, first: []string{gossh.KeyAlgoRSASHA512, gossh.KeyAlgoRSASHA256}},
		{name: "hashed alias", host: knownhosts.HashHostname(alias), keys: []gossh.PublicKey{ecKey.PublicKey()}, first: []string{gossh.KeyAlgoECDSA256}},
		{name: "multiple keys", host: alias, keys: []gossh.PublicKey{rsaKey.PublicKey(), ecKey.PublicKey(), ed.PublicKey()}, first: []string{gossh.KeyAlgoED25519, gossh.KeyAlgoECDSA256, gossh.KeyAlgoRSASHA512, gossh.KeyAlgoRSASHA256}},
		{name: "other instance", host: "sandbox-instance-1123456789abcdef0123456789abcdef", keys: []gossh.PublicKey{ecKey.PublicKey()}},
		{name: "old address", host: "172.30.0.2", keys: []gossh.PublicKey{ecKey.PublicKey()}},
	} {
		t.Run(test.name, func(t *testing.T) {
			content := knownhosts.Line([]string{"unrelated"}, forwardTestSigner(t).PublicKey()) + "\n"
			for _, key := range test.keys {
				content += knownhosts.Line([]string{test.host}, key) + "\n"
			}
			path := filepath.Join(t.TempDir(), "known_hosts")
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			manager := Manager{Paths: config.Paths{KnownHosts: path}}
			got, err := manager.forwardHostKeyAlgorithms(alias, "172.30.0.2:22")
			if err != nil {
				t.Fatal(err)
			}
			if len(test.first) == 0 {
				if !reflect.DeepEqual(got, forward.HostKeyAlgorithms()) {
					t.Fatalf("unexpected host preferences: %v", got)
				}
			} else if !reflect.DeepEqual(got[:len(test.first)], test.first) {
				t.Fatalf("order=%v first=%v", got, test.first)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != content {
				t.Fatal("preference lookup changed trusted keys")
			}
		})
	}
}
func TestForwardPreferencesRejectMalformedKnownHosts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(path, []byte("invalid key data\n"), 0600); err != nil {
		t.Fatal(err)
	}
	manager := Manager{Paths: config.Paths{KnownHosts: path}}
	if _, err := manager.forwardHostKeyAlgorithms("sandbox-instance-0123456789abcdef0123456789abcdef", "172.30.0.2:22"); err == nil {
		t.Fatal("ignored malformed trust data")
	}
}
