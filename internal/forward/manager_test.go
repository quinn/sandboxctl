package forward

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"sandboxctl/internal/hoststate"

	"golang.org/x/crypto/ssh"
)

func testManager(t *testing.T) (*Manager, hoststate.Instance, *atomic.Bool) {
	t.Helper()
	store := hoststate.Store{Paths: hoststate.PathsAt(filepath.Join(t.TempDir(), "state"))}
	instance := hoststate.Instance{SchemaVersion: hoststate.InstanceSchemaVersion, ID: "0123456789abcdef0123456789abcdef", Name: "dev1", CreatedAt: time.Now(), VCPUs: 1, MemoryMiB: 128, StateDiskBytes: 1, Image: "test", ImageVersion: "sha256:test", HostUID: 300001, HostGID: 300001, Subnet: "172.30.0.0/30", HostIP: "172.30.0.1", GuestIP: "172.30.0.2", TapDevice: "sc000001", GuestMAC: "06:00:ac:1e:00:02", SSHUser: "dev", SSHAlias: "sandbox-dev1", Extra: map[string]json.RawMessage{"future": json.RawMessage(`{"keep":true}`)}}
	if err := store.Save(instance); err != nil {
		t.Fatal(err)
	}
	running := &atomic.Bool{}
	running.Store(true)
	m := New(store, func(string) bool { return running.Load() })
	// Most state tests do not need a guest SSH server.
	m.dial = func(ctx context.Context, _ string, _ *ssh.ClientConfig) (*ssh.Client, error) {
		return nil, errors.New("test guest is offline")
	}
	t.Cleanup(func() {
		unlock, _ := m.Lock(context.Background())
		defer unlock()
		m.cancel()
		m.Suspend(instance.Name)
	})
	if _, err := m.Prepare(instance); err != nil {
		t.Fatal(err)
	}
	return m, instance, running
}
func testHostKey(t *testing.T) ssh.Signer {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}
func publicKey(key ssh.Signer) string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key.PublicKey())))
}
func loadInstance(t *testing.T, m *Manager) hoststate.Instance {
	t.Helper()
	instance, err := m.Store.Load("dev1")
	if err != nil {
		t.Fatal(err)
	}
	return instance
}

func TestMappingPersistenceCollisionAndRemoval(t *testing.T) {
	m, instance, _ := testManager(t)
	occupied, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	preferred := uint16(occupied.Addr().(*net.TCPAddr).Port)
	key := publicKey(testHostKey(t))
	got, err := m.Add(instance, 3000, &preferred, key, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.Mapping.LastHostPort == preferred || got.Mapping.LastHostPort < 1024 {
		t.Fatalf("no collision fallback: %+v", got)
	}
	saved := loadInstance(t, m)
	if len(saved.Forwards) != 1 || saved.Forwards[0].PreferredHostPort != preferred || saved.ForwardHostKey != key || len(saved.Extra) == 0 {
		t.Fatalf("bad saved mapping: %+v", saved)
	}
	repeated, err := m.Add(saved, 3000, nil, key, false)
	if err != nil || repeated.Mapping.LastHostPort != got.Mapping.LastHostPort {
		t.Fatalf("mapping changed: %+v %v", repeated, err)
	}
	// Stop preserves mappings and closes the bound socket.
	m.Suspend(instance.Name)
	if len(loadInstance(t, m).Forwards) != 1 {
		t.Fatal("stop discarded mapping")
	}
	clash, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", fmtPort(got.Mapping.LastHostPort)))
	if err != nil {
		t.Fatal(err)
	}
	defer clash.Close()
	if err = m.Restore(saved); err != nil {
		t.Fatal(err)
	}
	restarted := loadInstance(t, m)
	if restarted.Forwards[0].LastHostPort == got.Mapping.LastHostPort {
		t.Fatal("restart did not resolve collision")
	}
	if err = m.Remove(restarted, 3000); err != nil {
		t.Fatal(err)
	}
	if len(loadInstance(t, m).Forwards) != 0 || len(m.sessions) != 0 {
		t.Fatal("remove did not release mapping and tunnel")
	}
	if err = m.Remove(loadInstance(t, m), 3000); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing mapping: %v", err)
	}
}
func fmtPort(port uint16) string { return fmt.Sprint(port) }

func TestReconcileVerifiesRuntimeAndInstanceIdentity(t *testing.T) {
	m, instance, running := testManager(t)
	if _, err := m.Add(instance, 80, nil, publicKey(testHostKey(t)), false); err != nil {
		t.Fatal(err)
	}
	old := m.sessions[instance.Name]
	running.Store(false)
	m.reconcile()
	if len(m.sessions) != 0 {
		t.Fatal("stopped runtime retained listeners")
	}
	running.Store(true)
	m.reconcile()
	if m.sessions[instance.Name] == nil || m.sessions[instance.Name] == old {
		t.Fatal("did not restore verified runtime")
	}
	saved := loadInstance(t, m)
	saved.ID = "1123456789abcdef0123456789abcdef"
	saved.Forwards = nil
	saved.ForwardHostKey = ""
	if err := m.Store.Save(saved); err != nil {
		t.Fatal(err)
	}
	m.reconcile()
	if len(m.sessions) != 0 {
		t.Fatal("recreated instance inherited tunnel")
	}
}
func TestHostKeyChangeRequiresExplicitReplacement(t *testing.T) {
	m, instance, _ := testManager(t)
	key := publicKey(testHostKey(t))
	if _, err := m.Add(instance, 3000, nil, key, false); err != nil {
		t.Fatal(err)
	}
	saved := loadInstance(t, m)
	other := publicKey(testHostKey(t))
	if _, err := m.Add(saved, 3000, nil, other, false); err == nil {
		t.Fatal("accepted changed host key")
	}
	if loadInstance(t, m).ForwardHostKey != key {
		t.Fatal("changed host-key pin on rejection")
	}
	if _, err := m.Add(saved, 3000, nil, other, true); err != nil {
		t.Fatal(err)
	}
	if loadInstance(t, m).ForwardHostKey != other {
		t.Fatal("did not replace explicitly trusted key")
	}
}
func TestPrepareKeyStableAndPrivate(t *testing.T) {
	m, instance, _ := testManager(t)
	first, err := m.Prepare(instance)
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.Prepare(instance)
	if err != nil || first != second {
		t.Fatalf("key changed: %v", err)
	}
	info, err := os.Stat(filepath.Join(m.Store.Paths.Instances, instance.Name, "forward_ed25519"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatal("private key has unsafe mode")
	}
	if strings.Contains(first, "PRIVATE") {
		t.Fatal("Prepare returned private key")
	}
}
func TestParseHostKeyRejectsOptionsAndMultipleKeys(t *testing.T) {
	key := publicKey(testHostKey(t))
	for _, value := range []string{"", "invalid", "restrict " + key, key + "\n" + key, strings.Repeat("x", 16385)} {
		if _, err := ParseHostKey(value); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
	if _, err := ParseHostKey(key); err != nil {
		t.Fatal(err)
	}
}

// fakeGuest accepts native SSH direct-tcpip channels and echoes their bytes.
// It also records destinations and the number of shared SSH connections.
type fakeGuest struct {
	address      string
	key          ssh.Signer
	connections  atomic.Int32
	destinations chan string
}

func startGuest(t *testing.T) *fakeGuest {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	guest := &fakeGuest{address: listener.Addr().String(), key: testHostKey(t), destinations: make(chan string, 16)}
	config := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, _ ssh.PublicKey) (*ssh.Permissions, error) { return nil, nil }}
	config.AddHostKey(guest.key)
	var mu sync.Mutex
	active := make(map[net.Conn]bool)
	t.Cleanup(func() {
		listener.Close()
		mu.Lock()
		for conn := range active {
			conn.Close()
		}
		mu.Unlock()
	})
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			active[conn] = true
			mu.Unlock()
			go func() {
				defer conn.Close()
				defer func() { mu.Lock(); delete(active, conn); mu.Unlock() }()
				_, channels, requests, err := ssh.NewServerConn(conn, config)
				if err != nil {
					return
				}
				guest.connections.Add(1)
				go ssh.DiscardRequests(requests)
				for channel := range channels {
					if channel.ChannelType() != "direct-tcpip" {
						channel.Reject(ssh.UnknownChannelType, "unsupported channel")
						continue
					}
					var request struct {
						Host       string
						Port       uint32
						Origin     string
						OriginPort uint32
					}
					if err := ssh.Unmarshal(channel.ExtraData(), &request); err != nil {
						channel.Reject(ssh.Prohibited, "invalid destination")
						continue
					}
					guest.destinations <- net.JoinHostPort(request.Host, fmt.Sprint(request.Port))
					stream, requests, err := channel.Accept()
					if err != nil {
						continue
					}
					go ssh.DiscardRequests(requests)
					go func() { defer stream.Close(); io.Copy(stream, stream) }()
				}
			}()
		}
	}()
	return guest
}
func waitActive(t *testing.T, m *Manager, instance hoststate.Instance) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if m.List(instance)[0].State == "active" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("tunnel not active: %+v", m.List(instance))
}
func TestNativeSSHProxySharesConnectionAndClosesStreams(t *testing.T) {
	m, instance, _ := testManager(t)
	guest := startGuest(t)
	m.dial = func(ctx context.Context, _ string, config *ssh.ClientConfig) (*ssh.Client, error) {
		return Dial(ctx, guest.address, config)
	}
	if _, err := m.Add(instance, 3000, nil, publicKey(guest.key), false); err != nil {
		t.Fatal(err)
	}
	saved := loadInstance(t, m)
	if _, err := m.Add(saved, 3001, nil, publicKey(guest.key), false); err != nil {
		t.Fatal(err)
	}
	saved = loadInstance(t, m)
	waitActive(t, m, saved)
	var streams []net.Conn
	for _, mapping := range saved.Forwards {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", fmtPort(mapping.LastHostPort)), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		streams = append(streams, conn)
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		if _, err = conn.Write([]byte("hello")); err != nil {
			t.Fatal(err)
		}
		data := make([]byte, 5)
		if _, err = io.ReadFull(conn, data); err != nil || string(data) != "hello" {
			t.Fatalf("echo %q: %v", data, err)
		}
		select {
		case target := <-guest.destinations:
			if target != net.JoinHostPort("127.0.0.1", fmtPort(mapping.GuestPort)) {
				t.Fatalf("unsafe destination %s", target)
			}
		case <-time.After(time.Second):
			t.Fatal("missing destination")
		}
	}
	if guest.connections.Load() != 1 {
		t.Fatalf("opened %d SSH connections", guest.connections.Load())
	}
	if err := m.Remove(saved, 3000); err != nil {
		t.Fatal(err)
	}
	if _, err := streams[0].Read(make([]byte, 1)); err == nil {
		t.Fatal("removed mapping left stream open")
	}
	if _, err := streams[1].Write([]byte("ok")); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 2)
	if _, err := io.ReadFull(streams[1], data); err != nil {
		t.Fatal(err)
	}
	m.Suspend(instance.Name)
	if _, err := streams[1].Read(make([]byte, 1)); err == nil {
		t.Fatal("stop left stream open")
	}
}

func TestNativeSSHRejectsWrongHostKey(t *testing.T) {
	guest := startGuest(t)
	m, instance, _ := testManager(t)
	m.dial = func(ctx context.Context, _ string, config *ssh.ClientConfig) (*ssh.Client, error) {
		return Dial(ctx, guest.address, config)
	}
	if _, err := m.Add(instance, 3000, nil, publicKey(testHostKey(t)), false); err != nil {
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
	t.Fatalf("changed key was not rejected: %+v", m.List(loadInstance(t, m)))
}

func TestManagerRestartRestoresSavedMapping(t *testing.T) {
	original, instance, running := testManager(t)
	guest := startGuest(t)
	dial := func(ctx context.Context, _ string, config *ssh.ClientConfig) (*ssh.Client, error) {
		return Dial(ctx, guest.address, config)
	}
	original.dial = dial
	added, err := original.Add(instance, 80, nil, publicKey(guest.key), false)
	if err != nil {
		t.Fatal(err)
	}
	saved := loadInstance(t, original)
	waitActive(t, original, saved)
	original.Suspend(instance.Name)
	restarted := New(original.Store, func(string) bool { return running.Load() })
	restarted.dial = dial
	ctx, cancel := context.WithCancel(context.Background())
	go restarted.Run(ctx)
	t.Cleanup(func() { cancel(); restarted.Close() })
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		unlock, err := restarted.Lock(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		statuses := restarted.List(loadInstance(t, restarted))
		unlock()
		if len(statuses) == 1 && statuses[0].State == "active" {
			if statuses[0].Mapping.LastHostPort != added.Mapping.LastHostPort {
				t.Fatal("restart changed a free saved port")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("daemon restart did not restore saved mapping")
}

func TestDialCancelsIncompleteHandshake(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		io.Copy(io.Discard, conn)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	if client, err := Dial(ctx, listener.Addr().String(), &ssh.ClientConfig{User: "dev", HostKeyCallback: ssh.FixedHostKey(testHostKey(t).PublicKey())}); err == nil {
		client.Close()
		t.Fatal("accepted an incomplete handshake")
	}
	if time.Since(started) > time.Second {
		t.Fatal("handshake cancellation was delayed")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled handshake left a socket open")
	}
}

func TestFailedRecordWriteRetainsExistingMapping(t *testing.T) {
	m, instance, _ := testManager(t)
	key := publicKey(testHostKey(t))
	first, err := m.Add(instance, 3000, nil, key, false)
	if err != nil {
		t.Fatal(err)
	}
	reserved, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	preferred := uint16(reserved.Addr().(*net.TCPAddr).Port)
	reserved.Close()
	broken := loadInstance(t, m)
	broken.VCPUs = 0
	if _, err = m.Add(broken, 3001, &preferred, key, false); err == nil {
		t.Fatal("accepted invalid record")
	}
	saved := loadInstance(t, m)
	if len(saved.Forwards) != 1 || saved.Forwards[0].LastHostPort != first.Mapping.LastHostPort || !m.sessions[instance.Name].has(3000) {
		t.Fatal("failed write changed existing mapping")
	}
	reusable, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", fmtPort(preferred)))
	if err != nil {
		t.Fatalf("failed write leaked listener: %v", err)
	}
	reusable.Close()
}
func TestMappingLimit(t *testing.T) {
	m, instance, _ := testManager(t)
	for i := 0; i < 32; i++ {
		instance.Forwards = append(instance.Forwards, hoststate.Forward{GuestPort: uint16(i + 1), LastHostPort: uint16(40000 + i)})
	}
	if err := m.Store.Save(instance); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Add(instance, 3000, nil, publicKey(testHostKey(t)), false); err == nil {
		t.Fatal("accepted more than 32 mappings")
	}
}
