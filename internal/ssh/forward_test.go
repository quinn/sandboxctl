package ssh

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sandboxctl/internal/config"
	"sandboxctl/internal/forward"

	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func forwardTestSigner(t *testing.T) gossh.Signer {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := gossh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func TestForwardHostTrustUsesStableInstanceAlias(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "known_hosts")
	paths := config.Paths{KnownHosts: path}
	key := forwardTestSigner(t).PublicKey()
	alias := "sandbox-instance-0123456789abcdef0123456789abcdef"
	content := knownhosts.Line([]string{alias}, key) + "\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	manager := Manager{Paths: paths}
	for _, ip := range []string{"172.30.0.2", "172.30.0.6"} {
		address := &net.TCPAddr{IP: net.ParseIP(ip), Port: 22}
		if err := manager.verifyForwardHost(alias, address, key); err != nil {
			t.Fatal(err)
		}
	}
	changed := forwardTestSigner(t).PublicKey()
	if err := manager.verifyForwardHost(alias, &net.TCPAddr{IP: net.ParseIP("172.30.0.2"), Port: 22}, changed); err == nil {
		t.Fatal("accepted a changed host key")
	}
	saved, err := os.ReadFile(path)
	if err != nil || string(saved) != content {
		t.Fatal("changed managed trust on rejection")
	}
}
func TestForwardKeyScriptKeepsPrimaryKeysAndValidatesPolicy(t *testing.T) {
	key := strings.TrimSpace(string(gossh.MarshalAuthorizedKey(forwardTestSigner(t).PublicKey())))
	script := forwardKeyScript(key)
	for _, want := range []string{`restrict,port-forwarding,command="/usr/bin/false",permitopen="127.0.0.1:*"`, "sandboxctl_forward_keys", "sshd -t", "sshd -T", "AllowTcpForwarding local", "systemctl reload ssh.service"} {
		if !strings.Contains(script, want) {
			t.Fatalf("missing %s", want)
		}
	}
	for _, bad := range []string{`>"$HOME/.ssh/authorized_keys"`, "PRIVATE KEY"} {
		if strings.Contains(script, bad) {
			t.Fatalf("unsafe script %s", bad)
		}
	}
	command := exec.Command("sh", "-n")
	command.Stdin = strings.NewReader(script)
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("invalid guest script: %v %s", err, out)
	}
}

func TestRestrictedForwardKeyWithOpenSSH(t *testing.T) {
	binary, err := exec.LookPath("sshd")
	if err != nil {
		t.Skip("OpenSSH server is unavailable")
	}
	operator, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := gossh.MarshalPrivateKey(private, "test host")
	if err != nil {
		t.Fatal(err)
	}
	hostPath := filepath.Join(dir, "host_key")
	if err = os.WriteFile(hostPath, pem.EncodeToMemory(block), 0600); err != nil {
		t.Fatal(err)
	}
	hostSigner, err := gossh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	ecPrivate, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ecBlock, err := gossh.MarshalPrivateKey(ecPrivate, "test ECDSA host")
	if err != nil {
		t.Fatal(err)
	}
	ecPath := filepath.Join(dir, "ecdsa_host_key")
	if err = os.WriteFile(ecPath, pem.EncodeToMemory(ecBlock), 0600); err != nil {
		t.Fatal(err)
	}

	key := forwardTestSigner(t)
	authPath := filepath.Join(dir, "authorized_keys")
	line := `restrict,port-forwarding,command="/usr/bin/false",permitopen="127.0.0.1:*" ` + strings.TrimSpace(string(gossh.MarshalAuthorizedKey(key.PublicKey()))) + "\n"
	if err = os.WriteFile(authPath, []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	reserved, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := reserved.Addr().(*net.TCPAddr).Port
	reserved.Close()
	configPath := filepath.Join(dir, "sshd_config")
	config := fmt.Sprintf("Port %d\nListenAddress 127.0.0.1\nHostKey %s\nHostKey %s\nPidFile %s\nAuthorizedKeysFile %s\nStrictModes no\nUsePAM no\nPasswordAuthentication no\nKbdInteractiveAuthentication no\nPermitRootLogin yes\nAllowUsers %s\nAllowTcpForwarding local\nLogLevel ERROR\n", port, hostPath, ecPath, filepath.Join(dir, "pid"), authPath, operator.Username)
	if err = os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(dir, "sshd.log")
	output, err := os.Create(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	command := exec.Command(binary, "-D", "-e", "-f", configPath)
	command.Stderr = output
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { command.Process.Kill(); command.Wait() })
	var client *gossh.Client
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	for ctx.Err() == nil {
		client, err = forward.Dial(ctx, net.JoinHostPort("127.0.0.1", fmt.Sprint(port)), &gossh.ClientConfig{User: operator.Username, Auth: []gossh.AuthMethod{gossh.PublicKeys(key)}, HostKeyCallback: gossh.FixedHostKey(hostSigner.PublicKey()), HostKeyAlgorithms: forward.HostKeyAlgorithms(hostSigner.PublicKey())})
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		data, _ := os.ReadFile(outputPath)
		t.Fatalf("connect with restricted key: %v\n%s", err, data)
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	if err = session.RequestPty("xterm", 24, 80, nil); err == nil {
		t.Fatal("restricted key obtained a terminal")
	}
	if err = session.Run("true"); err == nil {
		t.Fatal("restricted key executed a command")
	}
	session.Close()
	if listener, err := client.Listen("tcp", "127.0.0.1:0"); err == nil {
		listener.Close()
		t.Fatal("restricted key opened reverse forwarding")
	}
	echo, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		conn, err := echo.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		io.Copy(conn, conn)
	}()
	remote, err := client.Dial("tcp", echo.Addr().String())
	if err != nil {
		t.Fatalf("local forwarding rejected: %v", err)
	}
	defer remote.Close()
	remote.SetDeadline(time.Now().Add(time.Second))
	if _, err = remote.Write([]byte("ok")); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 2)
	if _, err = io.ReadFull(remote, data); err != nil || string(data) != "ok" {
		t.Fatalf("forward: %q %v", data, err)
	}
	if other, err := client.Dial("tcp", net.JoinHostPort("0.0.0.0", fmt.Sprint(echo.Addr().(*net.TCPAddr).Port))); err == nil {
		other.Close()
		t.Fatal("key allowed a non-loopback destination")
	}
}
