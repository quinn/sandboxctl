package forward

import (
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"sandboxctl/internal/hoststate"

	"golang.org/x/crypto/ssh"
)

type tunnel struct {
	dial          func(context.Context, string, *ssh.ClientConfig) (*ssh.Client, error)
	instance      hoststate.Instance // Accessed only while the manager lock is held.
	address, user string
	signer        ssh.Signer
	hostKey       ssh.PublicKey
	ctx           context.Context
	cancel        context.CancelFunc
	mu            sync.Mutex
	client        *ssh.Client
	lastError     string
	listeners     map[uint16]*mappingListener
	slots         chan struct{}
	worker        sync.WaitGroup
}

type mappingListener struct {
	listener    net.Listener
	ctx         context.Context
	cancel      context.CancelFunc
	mu          sync.Mutex
	connections map[net.Conn]struct{}
	done        chan struct{}
	workers     sync.WaitGroup
}

func newTunnel(ctx context.Context, instance hoststate.Instance, signer ssh.Signer, key ssh.PublicKey, dial func(context.Context, string, *ssh.ClientConfig) (*ssh.Client, error)) *tunnel {
	ctx, cancel := context.WithCancel(ctx)
	return &tunnel{dial: dial, instance: instance, address: net.JoinHostPort(instance.GuestIP, "22"), user: instance.SSHUser, signer: signer, hostKey: key, ctx: ctx, cancel: cancel, listeners: make(map[uint16]*mappingListener), slots: make(chan struct{}, 128)}
}
func (t *tunnel) has(port uint16) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.listeners[port] != nil
}
func (t *tunnel) add(port uint16, listener net.Listener) {
	ctx, cancel := context.WithCancel(t.ctx)
	item := &mappingListener{listener: listener, ctx: ctx, cancel: cancel, connections: make(map[net.Conn]struct{}), done: make(chan struct{})}
	t.mu.Lock()
	t.listeners[port] = item
	t.mu.Unlock()
	go func() {
		defer close(item.done)
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			select {
			case t.slots <- struct{}{}:
			case <-ctx.Done():
				connection.Close()
				return
			default:
				connection.Close()
				continue
			}
			item.mu.Lock()
			if ctx.Err() != nil {
				item.mu.Unlock()
				connection.Close()
				<-t.slots
				return
			}
			item.connections[connection] = struct{}{}
			item.workers.Add(1)
			item.mu.Unlock()
			go func() {
				defer item.workers.Done()
				defer func() { <-t.slots }()
				defer connection.Close()
				defer func() { item.mu.Lock(); delete(item.connections, connection); item.mu.Unlock() }()
				t.proxy(item, connection, port)
			}()
		}
	}()
}
func (t *tunnel) remove(port uint16) {
	t.mu.Lock()
	item := t.listeners[port]
	delete(t.listeners, port)
	t.mu.Unlock()
	if item == nil {
		return
	}
	item.cancel()
	item.listener.Close()
	<-item.done // No workers can be added after Accept stops.
	item.mu.Lock()
	for conn := range item.connections {
		conn.Close()
	}
	item.mu.Unlock()
	item.workers.Wait()
}
func (t *tunnel) close() {
	t.cancel()
	t.mu.Lock()
	client := t.client
	ports := make([]uint16, 0, len(t.listeners))
	for port := range t.listeners {
		ports = append(ports, port)
	}
	t.mu.Unlock()
	if client != nil {
		client.Close()
	}
	for _, port := range ports {
		t.remove(port)
	}
	t.worker.Wait()
}
func (t *tunnel) start() { t.worker.Add(1); go func() { defer t.worker.Done(); t.connectLoop() }() }
func (t *tunnel) setError(err error) {
	t.mu.Lock()
	if err == nil {
		t.lastError = ""
	} else {
		t.lastError = err.Error()
	}
	t.mu.Unlock()
}

func (t *tunnel) connectLoop() {
	for t.ctx.Err() == nil {
		client, err := t.dial(t.ctx, t.address, &ssh.ClientConfig{User: t.user, Auth: []ssh.AuthMethod{ssh.PublicKeys(t.signer)}, HostKeyCallback: ssh.FixedHostKey(t.hostKey)})
		if err != nil {
			t.setError(err)
			if !pause(t.ctx, time.Second) {
				return
			}
			continue
		}
		t.mu.Lock()
		if t.ctx.Err() != nil {
			t.mu.Unlock()
			client.Close()
			return
		}
		t.client = client
		t.lastError = ""
		t.mu.Unlock()
		done := make(chan error, 1)
		go func() { done <- client.Wait() }()
		tick := time.NewTicker(15 * time.Second)
		running := true
		for running {
			select {
			case <-t.ctx.Done():
				running = false
			case err = <-done:
				running = false
			case <-tick.C:
				reply := make(chan error, 1)
				go func() { _, _, e := client.SendRequest("keepalive@openssh.com", true, nil); reply <- e }()
				timer := time.NewTimer(5 * time.Second)
				select {
				case err = <-reply:
					if err != nil {
						running = false
					}
				case <-timer.C:
					err = errors.New("SSH keepalive timed out")
					running = false
				case <-t.ctx.Done():
					running = false
				}
				timer.Stop()
			}
		}
		tick.Stop()
		client.Close()
		t.mu.Lock()
		t.client = nil
		if err != nil {
			t.lastError = err.Error()
		}
		t.mu.Unlock()
		if !pause(t.ctx, time.Second) {
			return
		}
	}
}
func pause(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// Dial makes a native SSH connection with a bounded, cancellable handshake.
func Dial(ctx context.Context, address string, config *ssh.ClientConfig) (*ssh.Client, error) {
	timeout := config.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	connection, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { connection.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	connection.SetDeadline(deadline)
	transport, channels, requests, err := ssh.NewClientConn(connection, address, config)
	if err != nil {
		connection.Close()
		return nil, err
	}
	if ctx.Err() != nil {
		transport.Close()
		return nil, ctx.Err()
	}
	connection.SetDeadline(time.Time{})
	return ssh.NewClient(transport, channels, requests), nil
}

func (t *tunnel) proxy(item *mappingListener, local net.Conn, port uint16) {
	t.mu.Lock()
	client := t.client
	t.mu.Unlock()
	if client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(item.ctx, 10*time.Second)
	remote, err := client.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port))))
	timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)
	cancel()
	if err != nil {
		if timedOut {
			client.Close()
		}
		return
	}
	defer remote.Close()
	stop := context.AfterFunc(item.ctx, func() { remote.Close(); local.Close() })
	defer stop()
	done := make(chan struct{})
	go func() { io.Copy(remote, local); closeWrite(remote); close(done) }()
	io.Copy(local, remote)
	closeWrite(local)
	// Closing either side also releases a stalled copy after connection removal.
	<-done
}
func closeWrite(connection net.Conn) {
	if half, ok := connection.(interface{ CloseWrite() error }); ok {
		half.CloseWrite()
	} else {
		connection.Close()
	}
}
