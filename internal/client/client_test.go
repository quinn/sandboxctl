package client

import (
	"context"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"sandboxctl/internal/controlpb"
	"sandboxctl/internal/daemon"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type testRuntime struct{ ready bool }

func (r testRuntime) Ready() bool { return r.ready }

func TestClientCallsDaemonOverUnixSocket(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "control.sock")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := daemon.NewServer("test-version", testRuntime{ready: true})
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

	var connection *grpc.ClientConn
	var service controlpb.ControlServiceClient
	var err error
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		connection, service, err = Dial(context.Background(), socketPath)
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("connect to daemon: %v", err)
	}
	defer connection.Close()

	callContext, cancelCall := context.WithTimeout(context.Background(), DialTimeout)
	defer cancelCall()
	ping, err := service.Ping(callContext, &controlpb.PingRequest{})
	if err != nil {
		t.Fatalf("ping daemon: %v", err)
	}
	if ping.GetStatus() != "ok" {
		t.Fatalf("ping status = %q, want ok", ping.GetStatus())
	}

	callContext, cancelCall = context.WithTimeout(context.Background(), DialTimeout)
	defer cancelCall()
	host, err := service.HostStatus(callContext, &controlpb.HostStatusRequest{})
	if err != nil {
		t.Fatalf("get host status: %v", err)
	}
	if host.GetDaemonVersion() != "test-version" || !host.GetRuntimeReady() {
		t.Fatalf("unexpected host status: %+v", host)
	}
}

func TestClientInterceptorSetsFreshRequestMetadata(t *testing.T) {
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(
		daemon.APIVersionMetadataKey, "stale-version",
		daemon.RequestIDMetadataKey, "stale-request-id",
	))
	requestIDs := make([]string, 0, 2)
	invoke := func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		outgoing, ok := metadata.FromOutgoingContext(ctx)
		if !ok {
			t.Fatal("outgoing metadata is missing")
		}
		if versions := outgoing.Get(daemon.APIVersionMetadataKey); len(versions) != 1 || versions[0] != daemon.APIVersion {
			t.Fatalf("API-version metadata = %v", versions)
		}
		ids := outgoing.Get(daemon.RequestIDMetadataKey)
		if len(ids) != 1 || !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(ids[0]) {
			t.Fatalf("request-ID metadata = %v", ids)
		}
		requestIDs = append(requestIDs, ids[0])
		return nil
	}
	for range 2 {
		if err := clientInterceptor(ctx, controlpb.ControlService_Ping_FullMethodName, nil, nil, nil, invoke); err != nil {
			t.Fatalf("client interceptor: %v", err)
		}
	}
	if requestIDs[0] == requestIDs[1] {
		t.Fatal("client reused a request ID")
	}
}
