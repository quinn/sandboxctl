package client

import (
	"context"
	"net"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"sandboxctl/internal/controlpb"
	"sandboxctl/internal/controlsettings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type clientTestService struct {
	controlpb.UnimplementedControlServiceServer
}

func (clientTestService) Ping(context.Context, *controlpb.PingRequest) (*controlpb.PingResponse, error) {
	return &controlpb.PingResponse{Status: "ok"}, nil
}

func (clientTestService) HostStatus(context.Context, *controlpb.HostStatusRequest) (*controlpb.HostStatusResponse, error) {
	return &controlpb.HostStatusResponse{DaemonVersion: "test-version", RuntimeReady: true}, nil
}

func TestClientCallsDaemonOverUnixSocket(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "control.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	controlpb.RegisterControlServiceServer(server, clientTestService{})
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		select {
		case err := <-serveDone:
			if err != nil && err != grpc.ErrServerStopped {
				t.Errorf("stop control test server: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("daemon did not stop")
		}
	})

	var connection *grpc.ClientConn
	var service controlpb.ControlServiceClient
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
		controlsettings.APIVersionMetadataKey, "stale-version",
		controlsettings.RequestIDMetadataKey, "stale-request-id",
	))
	requestIDs := make([]string, 0, 2)
	invoke := func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		outgoing, ok := metadata.FromOutgoingContext(ctx)
		if !ok {
			t.Fatal("outgoing metadata is missing")
		}
		if versions := outgoing.Get(controlsettings.APIVersionMetadataKey); len(versions) != 1 || versions[0] != controlsettings.APIVersion {
			t.Fatalf("API-version metadata = %v", versions)
		}
		ids := outgoing.Get(controlsettings.RequestIDMetadataKey)
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
