package daemon_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"sandboxctl/internal/controlpb"
	"sandboxctl/internal/daemon"
	"sandboxctl/internal/hoststate"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestServerValidatesMetadataAndReturnsRequestID(t *testing.T) {
	connection := startServer(t)
	service := controlpb.NewControlServiceClient(connection)

	requestID := "0123456789abcdef0123456789abcdef"
	ctx, cancel := callContext(daemon.APIVersion, requestID)
	defer cancel()
	var responseMetadata metadata.MD
	response, err := service.Ping(ctx, &controlpb.PingRequest{}, grpc.Header(&responseMetadata))
	if err != nil {
		t.Fatalf("Ping(): %v", err)
	}
	if response.GetStatus() != "ok" {
		t.Fatalf("Ping() status = %q, want ok", response.GetStatus())
	}
	if got := responseMetadata.Get(daemon.RequestIDMetadataKey); len(got) != 1 || got[0] != requestID {
		t.Fatalf("response request ID = %v, want %q", got, requestID)
	}
}

func TestServerRejectsInvalidMetadata(t *testing.T) {
	connection := startServer(t)
	service := controlpb.NewControlServiceClient(connection)
	tests := []struct {
		name      string
		api       string
		requestID string
		wantCode  codes.Code
	}{
		{name: "unsupported API version", api: "v2", requestID: "0123456789abcdef0123456789abcdef", wantCode: codes.FailedPrecondition},
		{name: "missing request ID", api: daemon.APIVersion, wantCode: codes.InvalidArgument},
		{name: "invalid request ID", api: daemon.APIVersion, requestID: "bad-id", wantCode: codes.InvalidArgument},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := callContext(test.api, test.requestID)
			defer cancel()
			_, err := service.Ping(ctx, &controlpb.PingRequest{})
			if got := status.Code(err); got != test.wantCode {
				t.Fatalf("Ping() error code = %s, want %s (error: %v)", got, test.wantCode, err)
			}
		})
	}
	ctx := metadata.AppendToOutgoingContext(context.Background(),
		daemon.APIVersionMetadataKey, daemon.APIVersion,
		daemon.RequestIDMetadataKey, "0123456789abcdef0123456789abcdef",
	)
	if _, err := service.Ping(ctx, &controlpb.PingRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("Ping() without a deadline returned %v, want InvalidArgument", err)
	}
}

func TestServerListsAndInspectsSavedInstances(t *testing.T) {
	paths := hoststate.PathsAt(filepath.Join(t.TempDir(), "state"))
	store := hoststate.Store{Paths: paths}
	for _, name := range []string{"dev2", "dev1"} {
		id, err := hoststate.NewID()
		if err != nil {
			t.Fatal(err)
		}
		instance := daemonTestInstance(id, name)
		if err := store.Save(instance); err != nil {
			t.Fatalf("save %s: %v", name, err)
		}
	}
	connection := startServerWithInstances(t, store)
	service := controlpb.NewControlServiceClient(connection)

	listContext, cancel := callContext(daemon.APIVersion, "11111111111111111111111111111111")
	listed, err := service.ListInstances(listContext, &controlpb.ListInstancesRequest{})
	cancel()
	if err != nil {
		t.Fatalf("ListInstances(): %v", err)
	}
	if len(listed.GetInstances()) != 2 || listed.GetInstances()[0].GetName() != "dev1" || listed.GetInstances()[1].GetName() != "dev2" {
		t.Fatalf("ListInstances() = %v, want instances ordered dev1, dev2", listed.GetInstances())
	}
	if listed.GetInstances()[0].GetHostUid() != 300001 || listed.GetInstances()[0].GetStateDiskBytes() == 0 {
		t.Fatalf("ListInstances() omitted instance fields: %v", listed.GetInstances()[0])
	}

	inspectContext, cancel := callContext(daemon.APIVersion, "22222222222222222222222222222222")
	inspected, err := service.InspectInstance(inspectContext, &controlpb.InspectInstanceRequest{Name: "dev1"})
	cancel()
	if err != nil {
		t.Fatalf("InspectInstance(): %v", err)
	}
	if inspected.GetInstance().GetName() != "dev1" || inspected.GetInstance().GetSshAlias() != "sandbox-dev1" {
		t.Fatalf("InspectInstance() = %v", inspected.GetInstance())
	}
}

func TestInspectInstanceReportsInvalidAndMissingNames(t *testing.T) {
	paths := hoststate.PathsAt(filepath.Join(t.TempDir(), "state"))
	connection := startServerWithInstances(t, hoststate.Store{Paths: paths})
	service := controlpb.NewControlServiceClient(connection)
	for _, test := range []struct {
		name string
		code codes.Code
	}{
		{name: "../etc", code: codes.InvalidArgument},
		{name: "missing", code: codes.NotFound},
	} {
		ctx, cancel := callContext(daemon.APIVersion, "33333333333333333333333333333333")
		_, err := service.InspectInstance(ctx, &controlpb.InspectInstanceRequest{Name: test.name})
		cancel()
		if got := status.Code(err); got != test.code {
			t.Errorf("InspectInstance(%q) code = %s, want %s (%v)", test.name, got, test.code, err)
		}
	}
}

func TestListenAndServeUsesSocketPermissions(t *testing.T) {
	socketDir := filepath.Join(t.TempDir(), "runtime", "sandboxctl")
	socketPath := filepath.Join(socketDir, "control.sock")
	ctx, cancel := context.WithCancel(context.Background())
	server := daemon.NewServer("test-version", nil)
	serveDone := make(chan error, 1)
	go func() { serveDone <- daemon.ListenAndServe(ctx, socketPath, server) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-serveDone:
			if err != nil {
				t.Errorf("stop daemon: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("daemon did not stop")
		}
	})

	deadline := time.Now().Add(2 * time.Second)
	var socketInfo os.FileInfo
	var err error
	for time.Now().Before(deadline) {
		socketInfo, err = os.Lstat(socketPath)
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("stat control socket: %v", err)
	}
	if socketInfo.Mode()&os.ModeSocket == 0 || socketInfo.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode = %v, want socket mode 0600", socketInfo.Mode())
	}
	directoryInfo, err := os.Stat(socketDir)
	if err != nil {
		t.Fatalf("stat socket directory: %v", err)
	}
	if directoryInfo.Mode().Perm() != 0o700 {
		t.Fatalf("socket directory mode = %v, want 0700", directoryInfo.Mode().Perm())
	}
}

func startServer(t *testing.T) *grpc.ClientConn {
	return startServerWithInstances(t, nil)
}

func startServerWithInstances(t *testing.T, instances daemon.InstanceStore) *grpc.ClientConn {
	t.Helper()
	socketPath := filepath.Join(t.TempDir(), "control.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	server := daemon.NewServerWithInstanceStore("test-version", nil, instances)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
		_ = os.Remove(socketPath)
	})

	connection, err := grpc.NewClient(
		"unix://"+filepath.ToSlash(socketPath),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial test daemon: %v", err)
	}
	connection.Connect()
	dialContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for {
		state := connection.GetState()
		if state == connectivity.Ready {
			break
		}
		if state == connectivity.Shutdown || !connection.WaitForStateChange(dialContext, state) {
			_ = connection.Close()
			t.Fatalf("dial test daemon: connection state %s: %v", state, dialContext.Err())
		}
	}
	t.Cleanup(func() { _ = connection.Close() })
	return connection
}

func daemonTestInstance(id, name string) hoststate.Instance {
	return hoststate.Instance{
		SchemaVersion:  hoststate.InstanceSchemaVersion,
		ID:             id,
		Name:           name,
		CreatedAt:      time.Date(2026, time.October, 7, 12, 0, 0, 0, time.UTC),
		VCPUs:          2,
		MemoryMiB:      4096,
		StateDiskBytes: 20 * 1024 * 1024 * 1024,
		Image:          "default",
		ImageVersion:   "sha256:abc",
		HostUID:        300001,
		HostGID:        300001,
		Subnet:         "172.30.0.0/30",
		HostIP:         "172.30.0.1",
		GuestIP:        "172.30.0.2",
		TapDevice:      "sc000001",
		GuestMAC:       "06:00:ac:10:00:02",
		SSHUser:        "dev",
		SSHAlias:       "sandbox-" + name,
	}
}

func callContext(apiVersion, requestID string) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	pairs := []string{daemon.APIVersionMetadataKey, apiVersion}
	if requestID != "" {
		pairs = append(pairs, daemon.RequestIDMetadataKey, requestID)
	}
	return metadata.AppendToOutgoingContext(ctx, pairs...), cancel
}

func ExampleNewRequestID() {
	requestID, err := daemon.NewRequestID()
	if err != nil {
		fmt.Println("error")
		return
	}
	fmt.Println(len(requestID))
	// Output: 32
}
