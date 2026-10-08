package client

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"sandboxctl/internal/controlpb"
	"sandboxctl/internal/daemon"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

const (
	// DefaultSocketPath is the system socket for the privileged daemon.
	DefaultSocketPath = "/run/sandboxctl/control.sock"
	// DialTimeout is the maximum time Dial waits for the daemon connection.
	DialTimeout = 5 * time.Second
)

// Dial connects to sandboxd over its Unix domain socket.
func Dial(ctx context.Context, socketPath string) (*grpc.ClientConn, controlpb.ControlServiceClient, error) {
	dialContext, cancel := context.WithTimeout(ctx, DialTimeout)
	defer cancel()
	absSocketPath, err := filepath.Abs(socketPath)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve sandboxd socket path: %w", err)
	}
	connection, err := grpc.NewClient(
		"unix://"+filepath.ToSlash(absSocketPath),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(clientInterceptor),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallSendMsgSize(daemon.MaxMessageSize),
			grpc.MaxCallRecvMsgSize(daemon.MaxMessageSize),
		),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("connect to sandboxd: %w", err)
	}
	connection.Connect()
	for {
		state := connection.GetState()
		if state == connectivity.Ready {
			break
		}
		if state == connectivity.Shutdown {
			_ = connection.Close()
			return nil, nil, fmt.Errorf("connect to sandboxd: connection entered state %s", state)
		}
		if !connection.WaitForStateChange(dialContext, state) {
			_ = connection.Close()
			return nil, nil, fmt.Errorf("connect to sandboxd: %w", dialContext.Err())
		}
	}

	return connection, controlpb.NewControlServiceClient(connection), nil
}

func clientInterceptor(ctx context.Context, method string, request, response any, connection *grpc.ClientConn, invoke grpc.UnaryInvoker, options ...grpc.CallOption) error {
	requestID, err := daemon.NewRequestID()
	if err != nil {
		return err
	}
	outgoing, _ := metadata.FromOutgoingContext(ctx)
	outgoing = outgoing.Copy()
	if outgoing == nil {
		outgoing = metadata.MD{}
	}
	outgoing.Set(daemon.APIVersionMetadataKey, daemon.APIVersion)
	outgoing.Set(daemon.RequestIDMetadataKey, requestID)
	ctx = metadata.NewOutgoingContext(ctx, outgoing)
	return invoke(ctx, method, request, response, connection, options...)
}
