package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"time"

	controlpb "sandboxctl/internal/controlpb"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	APIVersionMetadataKey = "sandboxctl-api-version"
	RequestIDMetadataKey  = "x-request-id"
	APIVersion            = "v1"
	MaxMessageSize        = 1 << 20
	MaxOperationDuration  = 10 * time.Second
)

var requestIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// RuntimeStatus exposes only the status needed by the initial host API.
type RuntimeStatus interface {
	Ready() bool
}

// NewServer creates a gRPC server for the initial control API.
func NewServer(version string, runtime RuntimeStatus) *grpc.Server {
	server := grpc.NewServer(
		grpc.MaxRecvMsgSize(MaxMessageSize),
		grpc.MaxSendMsgSize(MaxMessageSize),
		grpc.UnaryInterceptor(unaryInterceptor),
	)
	controlpb.RegisterControlServiceServer(server, &service{version: version, runtime: runtime})
	return server
}

func unaryInterceptor(ctx context.Context, request any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	incoming, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "request metadata is required")
	}
	apiVersions := incoming.Get(APIVersionMetadataKey)
	if len(apiVersions) != 1 || apiVersions[0] != APIVersion {
		return nil, status.Error(codes.FailedPrecondition, "unsupported API version")
	}
	requestIDs := incoming.Get(RequestIDMetadataKey)
	if len(requestIDs) != 1 || !requestIDPattern.MatchString(requestIDs[0]) {
		return nil, status.Error(codes.InvalidArgument, "request identifier is missing or invalid")
	}
	if _, ok := ctx.Deadline(); !ok {
		return nil, status.Error(codes.InvalidArgument, "request deadline is required")
	}
	ctx, cancel := context.WithTimeout(ctx, MaxOperationDuration)
	defer cancel()
	if err := grpc.SetHeader(ctx, metadata.Pairs(RequestIDMetadataKey, requestIDs[0])); err != nil {
		return nil, status.Error(codes.Internal, "cannot set response request identifier")
	}
	return handler(ctx, request)
}

type service struct {
	controlpb.UnimplementedControlServiceServer
	version string
	runtime RuntimeStatus
}

func (s *service) Ping(context.Context, *controlpb.PingRequest) (*controlpb.PingResponse, error) {
	return &controlpb.PingResponse{Status: "ok"}, nil
}

func (s *service) HostStatus(context.Context, *controlpb.HostStatusRequest) (*controlpb.HostStatusResponse, error) {
	return &controlpb.HostStatusResponse{DaemonVersion: s.version, RuntimeReady: s.runtime != nil && s.runtime.Ready()}, nil
}

// ListenAndServe runs the daemon on a private Unix socket until its context ends.
func ListenAndServe(ctx context.Context, socketPath string, server *grpc.Server) error {
	if socketPath == "" {
		return errors.New("socket path is required")
	}

	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		return fmt.Errorf("create socket directory: %w", err)
	}

	if _, err := os.Lstat(socketPath); err == nil {
		return fmt.Errorf("socket path already exists: %s", socketPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect socket path: %w", err)
	}

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("listen on Unix socket: %w", err)
	}

	defer func() {
		_ = listener.Close()
		_ = os.Remove(socketPath)
	}()

	if err := os.Chmod(socketPath, 0o600); err != nil {
		return fmt.Errorf("set socket permissions: %w", err)
	}

	return Serve(ctx, listener, server)
}

// Serve runs the gRPC server on a listener until its context ends.
func Serve(ctx context.Context, listener net.Listener, server *grpc.Server) error {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			server.GracefulStop()
		case <-done:
		}
	}()

	serveErr := server.Serve(listener)
	close(done)
	if ctx.Err() != nil {
		return nil
	}
	if serveErr != nil {
		return fmt.Errorf("serve gRPC control API: %w", serveErr)
	}

	return nil
}

// NewRequestID creates a random 128-bit request identifier.
func NewRequestID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("create request identifier: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}
