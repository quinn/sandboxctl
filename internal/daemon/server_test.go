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
	t.Helper()
	socketPath := filepath.Join(t.TempDir(), "control.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	server := daemon.NewServer("test-version", nil)
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
