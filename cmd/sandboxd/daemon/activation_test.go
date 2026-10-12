package daemon

import (
	"net"
	"os"
	"strconv"
	"testing"
)

func TestActivatedListenerAcceptsUnixSocket(t *testing.T) {
	path := t.TempDir() + "/control.sock"
	source, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	file, err := source.(*net.UnixListener).File()
	if err != nil {
		t.Fatal(err)
	}
	listener, err := ActivatedListener(strconv.Itoa(os.Getpid()), "1", os.Getpid(), file)
	if err != nil {
		t.Fatalf("use activated listener: %v", err)
	}
	defer listener.Close()
	if listener.Addr().Network() != "unix" {
		t.Fatalf("listener network = %q, want unix", listener.Addr().Network())
	}
}

func TestActivatedListenerRejectsInvalidEnvironment(t *testing.T) {
	for _, test := range []struct {
		name, listenPID, listenFDS string
	}{
		{name: "wrong pid", listenPID: "1", listenFDS: "1"},
		{name: "invalid pid", listenPID: "bad", listenFDS: "1"},
		{name: "wrong descriptor count", listenFDS: "2"},
		{name: "invalid descriptor count", listenFDS: "bad"},
	} {
		t.Run(test.name, func(t *testing.T) {
			file, err := os.Open(os.DevNull)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ActivatedListener(test.listenPID, test.listenFDS, os.Getpid(), file); err == nil {
				t.Fatal("expected activation validation error")
			}
		})
	}
}

func TestActivatedListenerRejectsNonUnixSocket(t *testing.T) {
	source, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	file, err := source.(*net.TCPListener).File()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ActivatedListener(strconv.Itoa(os.Getpid()), "1", os.Getpid(), file); err == nil {
		t.Fatal("expected non-Unix socket error")
	}
}
