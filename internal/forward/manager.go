// Package forward manages daemon-owned SSH tunnels to guest loopback ports.
package forward

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"sandboxctl/internal/hoststate"

	"golang.org/x/crypto/ssh"
)

// Status contains a saved mapping and its current tunnel state.
type Status struct {
	Mapping      hoststate.Forward
	State, Error string
}

// Manager serializes forwarding and lifecycle operations against one store.
type Manager struct {
	Store hoststate.Store
	// Running must verify the persisted process identity after daemon restart.
	Running  func(string) bool
	dial     func(context.Context, string, *ssh.ClientConfig) (*ssh.Client, error)
	gate     chan struct{}
	sessions map[string]*tunnel
	errors   map[string]string
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
}

// New creates a manager. Run must be called before the control server starts.
func New(store hoststate.Store, running func(string) bool) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{Store: store, Running: running, gate: make(chan struct{}, 1), sessions: make(map[string]*tunnel), errors: make(map[string]string), ctx: ctx, cancel: cancel, done: make(chan struct{}), dial: Dial}
}

// Lock serializes record mutations and VM lifecycle transitions.
func (m *Manager) Lock(ctx context.Context) (func(), error) {
	select {
	case m.gate <- struct{}{}:
		if ctx.Err() != nil {
			<-m.gate
			return nil, ctx.Err()
		}
		if m.ctx.Err() != nil {
			<-m.gate
			return nil, m.ctx.Err()
		}
		return func() { <-m.gate }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Run restores listeners for verified running instances until cancellation.
func (m *Manager) Run(ctx context.Context) {
	defer close(m.done)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		unlock, err := m.Lock(ctx)
		if err != nil {
			break
		}
		m.reconcile()
		unlock()
		select {
		case <-ctx.Done():
			m.cancel()
			goto finish
		case <-m.ctx.Done():
			goto finish
		case <-ticker.C:
		}
	}
finish:
	m.cancel()
	m.gate <- struct{}{}
	defer func() { <-m.gate }()
	for name := range m.sessions {
		m.Suspend(name)
	}
}

// Close ends all tunnels and waits for Run to release the listeners.
func (m *Manager) Close() { m.cancel(); <-m.done }

func (m *Manager) reconcile() {
	// Check live sessions even if another instance record cannot be read.
	for name, t := range m.sessions {
		current, err := m.Store.Load(name)
		if err != nil || current.ID != t.instance.ID || !m.Running(name) {
			m.Suspend(name)
		}
	}
	instances, err := m.Store.List()
	if err != nil {
		return
	}
	seen := make(map[string]bool)
	for _, instance := range instances {
		seen[instance.Name] = true
		current := m.sessions[instance.Name]
		if !m.Running(instance.Name) || len(instance.Forwards) == 0 || instance.ForwardHostKey == "" {
			m.Suspend(instance.Name)
			continue
		}
		if current != nil && (current.instance.ID != instance.ID || current.instance.ForwardHostKey != instance.ForwardHostKey) {
			m.Suspend(instance.Name)
			current = nil
		}
		if current != nil {
			continue
		}
		if err := m.Restore(instance); err != nil {
			m.errors[instance.Name] = err.Error()
		}
	}
	for name := range m.sessions {
		if !seen[name] {
			m.Suspend(name)
		}
	}
}

// Prepare returns only the public part of a dedicated forwarding key.
// Callers must hold Lock.
func (m *Manager) Prepare(instance hoststate.Instance) (string, error) {
	signer, err := m.signer(instance.Name)
	if errors.Is(err, fs.ErrNotExist) {
		_, key, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return "", err
		}
		block, err := ssh.MarshalPrivateKey(key, "sandboxctl forwarding "+instance.ID)
		if err != nil {
			return "", err
		}
		if err = m.Store.SaveForwardKey(instance.Name, pem.EncodeToMemory(block)); err != nil {
			return "", err
		}
		signer, err = m.signer(instance.Name)
		if err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))), nil
}

func (m *Manager) signer(name string) (ssh.Signer, error) {
	data, err := m.Store.LoadForwardKey(name)
	if err != nil {
		return nil, err
	}
	return ssh.ParsePrivateKey(data)
}

// ParseHostKey validates one public host key without authorized-key options.
func ParseHostKey(value string) (ssh.PublicKey, error) {
	if len(value) > 16384 {
		return nil, errors.New("guest host key is too large")
	}
	key, _, options, rest, err := ssh.ParseAuthorizedKey([]byte(value))
	if err != nil || len(options) != 0 || len(strings.TrimSpace(string(rest))) != 0 {
		return nil, errors.New("invalid guest host key")
	}
	return key, nil
}

// Restore binds saved listeners and starts one SSH connection worker.
// Callers must hold Lock.
func (m *Manager) Restore(instance hoststate.Instance) error {
	if len(instance.Forwards) == 0 {
		return nil
	}
	if instance.ForwardHostKey == "" {
		return errors.New("guest host key requires trust; run sandboxctl forward")
	}
	if m.sessions[instance.Name] != nil {
		return nil
	}
	signer, err := m.signer(instance.Name)
	if err != nil {
		return err
	}
	key, err := ParseHostKey(instance.ForwardHostKey)
	if err != nil {
		return err
	}
	t := newTunnel(m.ctx, instance, signer, key, m.dial)
	mappings := append([]hoststate.Forward(nil), instance.Forwards...)
	for i := range mappings {
		listener, err := bind(mappings[i].LastHostPort)
		if err != nil {
			t.close()
			return err
		}
		mappings[i].LastHostPort = uint16(listener.Addr().(*net.TCPAddr).Port)
		t.add(mappings[i].GuestPort, listener)
	}
	instance.Forwards = mappings
	if err = m.Store.Save(instance); err != nil {
		t.close()
		return err
	}
	t.instance = instance
	m.sessions[instance.Name] = t
	delete(m.errors, instance.Name)
	t.start()
	return nil
}

// Add saves a mapping after the CLI verifies the guest host key.
// Callers must hold Lock.
func (m *Manager) Add(instance hoststate.Instance, guest uint16, preferred *uint16, hostKey string, replace bool) (Status, error) {
	key, err := ParseHostKey(hostKey)
	if err != nil {
		return Status{}, err
	}
	hostKey = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
	if instance.ForwardHostKey != "" && instance.ForwardHostKey != hostKey && !replace {
		return Status{}, errors.New("guest host key changed; verify it and use --replace-host-key")
	}
	signer, err := m.signer(instance.Name)
	if err != nil {
		return Status{}, err
	}
	index := -1
	mapping := hoststate.Forward{GuestPort: guest}
	for i, item := range instance.Forwards {
		if item.GuestPort == guest {
			index = i
			mapping = item
			break
		}
	}
	if index < 0 && len(instance.Forwards) >= 32 {
		return Status{}, errors.New("sandbox already has 32 port mappings")
	}
	if preferred != nil {
		mapping.PreferredHostPort = *preferred
	}
	t := m.sessions[instance.Name]
	if index >= 0 && t != nil && instance.ForwardHostKey == hostKey && (preferred == nil || mapping.PreferredHostPort == instance.Forwards[index].PreferredHostPort) {
		if t.has(guest) {
			return m.status(instance, mapping), nil
		}
	}
	port := mapping.LastHostPort
	if preferred != nil {
		port = *preferred
	} else if port == 0 {
		port = mapping.PreferredHostPort
		if port == 0 && guest >= 1024 {
			port = guest
		}
	}
	listener, err := bind(port)
	if err != nil {
		return Status{}, err
	}
	mapping.LastHostPort = uint16(listener.Addr().(*net.TCPAddr).Port)
	instance.Forwards = append([]hoststate.Forward(nil), instance.Forwards...)
	if index < 0 {
		instance.Forwards = append(instance.Forwards, mapping)
	} else {
		instance.Forwards[index] = mapping
	}
	instance.ForwardHostKey = hostKey
	// Prepare every replacement listener before committing the new record.
	var replacement *tunnel
	if t == nil || t.instance.ForwardHostKey != hostKey {
		replacement = newTunnel(m.ctx, instance, signer, key, m.dial)
		replacement.add(guest, listener)
		for i := range instance.Forwards {
			other := &instance.Forwards[i]
			if other.GuestPort == guest {
				continue
			}
			otherListener, bindErr := bind(other.LastHostPort)
			if bindErr != nil {
				replacement.close()
				return Status{}, bindErr
			}
			other.LastHostPort = uint16(otherListener.Addr().(*net.TCPAddr).Port)
			replacement.add(other.GuestPort, otherListener)
		}
	}
	if err = m.Store.Save(instance); err != nil {
		if replacement != nil {
			replacement.close()
		} else {
			listener.Close()
		}
		return Status{}, err
	}
	if replacement != nil {
		if t != nil {
			m.Suspend(instance.Name)
		}
		t = replacement
		t.instance = instance
		m.sessions[instance.Name] = t
		t.start()
	} else {
		t.remove(guest)
		t.add(guest, listener)
		t.instance = instance
	}

	delete(m.errors, instance.Name)
	return m.status(instance, mapping), nil
}

// Suspend closes live connections but retains the saved mappings.
// Callers must hold Lock.
func (m *Manager) Suspend(name string) {
	if t := m.sessions[name]; t != nil {
		t.close()
		delete(m.sessions, name)
	}
	delete(m.errors, name)
}

// Remove deletes one mapping and closes its active connections.
// Callers must hold Lock.
func (m *Manager) Remove(instance hoststate.Instance, guest uint16) error {
	items := make([]hoststate.Forward, 0, len(instance.Forwards))
	found := false
	for _, mapping := range instance.Forwards {
		if mapping.GuestPort == guest {
			found = true
		} else {
			items = append(items, mapping)
		}
	}
	if !found {
		return fs.ErrNotExist
	}
	instance.Forwards = items
	if err := m.Store.Save(instance); err != nil {
		return err
	}
	if t := m.sessions[instance.Name]; t != nil {
		t.remove(guest)
		t.instance = instance
		if len(items) == 0 {
			m.Suspend(instance.Name)
		}
	}
	return nil
}

// List reports the mappings and current tunnel state. Callers must hold Lock.
func (m *Manager) List(instance hoststate.Instance) []Status {
	statuses := make([]Status, 0, len(instance.Forwards))
	for _, mapping := range instance.Forwards {
		statuses = append(statuses, m.status(instance, mapping))
	}
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].Mapping.GuestPort < statuses[j].Mapping.GuestPort })
	return statuses
}
func (m *Manager) status(instance hoststate.Instance, mapping hoststate.Forward) Status {
	result := Status{Mapping: mapping, State: "stopped"}
	if t := m.sessions[instance.Name]; t != nil {
		t.mu.Lock()
		defer t.mu.Unlock()
		result.State = "connecting"
		if t.client != nil {
			result.State = "active"
		}
		if t.lastError != "" {
			result.State = "error"
			result.Error = t.lastError
		}
		if t.listeners[mapping.GuestPort] == nil {
			result.State = "error"
			result.Error = "listener is unavailable"
		}
	} else if m.Running(instance.Name) {
		result.State = "error"
		result.Error = m.errors[instance.Name]
		if result.Error == "" {
			result.Error = "forwarding is not ready; run sandboxctl forward to verify guest access"
		}
	}
	return result
}

// bind attempts the requested port and falls back only for an occupied port.
func bind(port uint16) (net.Listener, error) {
	if port != 0 && port < 1024 {
		return nil, errors.New("host port must range from 1024 to 65535")
	}
	listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port))))
	if err != nil && port != 0 && errors.Is(err, syscall.EADDRINUSE) {
		listener, err = net.Listen("tcp4", "127.0.0.1:0")
	}
	if err != nil {
		return nil, fmt.Errorf("bind host port: %w", err)
	}
	if listener.Addr().(*net.TCPAddr).Port < 1024 {
		listener.Close()
		return nil, errors.New("selected host port is below 1024")
	}
	return listener, nil
}
