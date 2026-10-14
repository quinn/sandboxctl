package forward

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"io"
	"net"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func testECDSAKey(t *testing.T) ssh.Signer {
	t.Helper()
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}
func testRSAKey(t *testing.T) ssh.Signer {
	t.Helper()
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}
func TestHostKeyAlgorithmsUseOpenSSHOrder(t *testing.T) {
	binary, err := exec.LookPath("ssh")
	if err != nil {
		t.Skip("OpenSSH client is unavailable")
	}
	// Read the default order without the operator's SSH configuration.
	output, err := exec.Command(binary, "-G", "-F", "/dev/null", "localhost").Output()
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(line, "hostkeyalgorithms ") {
			order = strings.Split(strings.TrimPrefix(line, "hostkeyalgorithms "), ",")
			break
		}
	}
	if len(order) == 0 {
		t.Fatal("OpenSSH did not report its host-key order")
	}
	supported := make(map[string]bool)
	for _, algorithm := range ssh.SupportedAlgorithms().HostKeys {
		supported[algorithm] = true
	}
	var expected []string
	for _, algorithm := range order {
		if supported[algorithm] {
			expected = append(expected, algorithm)
		}
	}
	if got := HostKeyAlgorithms(); !reflect.DeepEqual(got, expected) {
		t.Fatalf("order=%v; OpenSSH supported order=%v", got, expected)
	}
}
func TestHostKeyAlgorithmsPromoteTrustedKeys(t *testing.T) {
	ed := testHostKey(t).PublicKey()
	ec := testECDSAKey(t).PublicKey()
	rsa := testRSAKey(t).PublicKey()
	for _, test := range []struct {
		name  string
		keys  []ssh.PublicKey
		first []string
	}{
		{name: "Ed25519", keys: []ssh.PublicKey{ed}, first: []string{ssh.KeyAlgoED25519}},
		{name: "ECDSA", keys: []ssh.PublicKey{ec}, first: []string{ssh.KeyAlgoECDSA256}},
		{name: "RSA signatures", keys: []ssh.PublicKey{rsa}, first: []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256}},
		{name: "RSA certificate signatures", keys: []ssh.PublicKey{&ssh.Certificate{Key: rsa, CertType: ssh.HostCert}}, first: []string{ssh.CertAlgoRSASHA512v01, ssh.CertAlgoRSASHA256v01}},
		{name: "multiple trusted keys", keys: []ssh.PublicKey{rsa, ec, ed}, first: []string{ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256, ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256}},
		{name: "duplicate and nil", keys: []ssh.PublicKey{nil, ec, ec}, first: []string{ssh.KeyAlgoECDSA256}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := HostKeyAlgorithms(test.keys...)
			if !reflect.DeepEqual(got[:len(test.first)], test.first) {
				t.Fatalf("order=%v; first=%v", got, test.first)
			}
			defaultOrder := HostKeyAlgorithms()
			var remaining []string
			preferred := make(map[string]bool)
			for _, algorithm := range test.first {
				preferred[algorithm] = true
			}
			for _, algorithm := range defaultOrder {
				if !preferred[algorithm] {
					remaining = append(remaining, algorithm)
				}
			}
			if !reflect.DeepEqual(got[len(test.first):], remaining) {
				t.Fatalf("fallback order changed: %v", got)
			}
			seen := make(map[string]bool)
			for _, algorithm := range got {
				if seen[algorithm] || algorithm == ssh.KeyAlgoRSA || algorithm == ssh.CertAlgoRSAv01 {
					t.Fatalf("duplicate or SHA-1 algorithm: %s", algorithm)
				}
				seen[algorithm] = true
			}
		})
	}
}
func TestDaemonSelectsPinnedKeyFromMultiKeyServer(t *testing.T) {
	ed, ec, rsa := testHostKey(t), testECDSAKey(t), testRSAKey(t)
	for _, pin := range []ssh.Signer{ed, ec, rsa} {
		t.Run(pin.PublicKey().Type(), func(t *testing.T) {
			m, instance, _ := testManager(t)
			// Go's default selects ECDSA from this server, even when Ed25519 is pinned.
			guest := startGuest(t, ec, rsa, ed)
			if pin.PublicKey().Type() == ssh.KeyAlgoED25519 {
				// Reproduce the original failure with Go's default algorithm ordering.
				signer, err := m.signer(instance.Name)
				if err != nil {
					t.Fatal(err)
				}
				old, err := Dial(context.Background(), guest.address, &ssh.ClientConfig{User: "dev", Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: ssh.FixedHostKey(pin.PublicKey())})
				if err == nil {
					old.Close()
					t.Fatal("Go's default unexpectedly selected the pinned Ed25519 key")
				}
				if !strings.Contains(err.Error(), "host key mismatch") {
					t.Fatalf("unexpected baseline failure: %v", err)
				}
			}

			m.dial = func(ctx context.Context, _ string, config *ssh.ClientConfig) (*ssh.Client, error) {
				return Dial(ctx, guest.address, config)
			}
			added, err := m.Add(instance, 80, nil, publicKey(pin), false)
			if err != nil {
				t.Fatal(err)
			}
			saved := loadInstance(t, m)
			waitActive(t, m, saved)
			local, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", fmtPort(added.Mapping.LastHostPort)), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer local.Close()
			local.SetDeadline(time.Now().Add(time.Second))
			if _, err = local.Write([]byte("pinned")); err != nil {
				t.Fatal(err)
			}
			data := make([]byte, 6)
			if _, err = io.ReadFull(local, data); err != nil || string(data) != "pinned" {
				t.Fatalf("proxy=%q err=%v", data, err)
			}
			// Restoration uses the same host-key preference as initial registration.
			m.Suspend(instance.Name)
			if err = m.Restore(saved); err != nil {
				t.Fatal(err)
			}
			waitActive(t, m, loadInstance(t, m))
		})
	}
}
func TestDaemonRejectsChangedPinnedKeyOnMultiKeyServer(t *testing.T) {
	guest := startGuest(t, testECDSAKey(t), testRSAKey(t), testHostKey(t))
	m, instance, _ := testManager(t)
	m.dial = func(ctx context.Context, _ string, config *ssh.ClientConfig) (*ssh.Client, error) {
		return Dial(ctx, guest.address, config)
	}
	if _, err := m.Add(instance, 80, nil, publicKey(testHostKey(t)), false); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		statuses := m.List(loadInstance(t, m))
		if statuses[0].State == "error" && strings.Contains(statuses[0].Error, "host key mismatch") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("changed host key was accepted: %+v", m.List(loadInstance(t, m)))
}
