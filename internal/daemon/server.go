package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"time"

	controlpb "sandboxctl/internal/controlpb"
	"sandboxctl/internal/hoststate"

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
	return NewServerWithInstanceStore(version, runtime, nil)
}

// NewServerWithInstanceStore creates a gRPC server with read-only instance access.
func NewServerWithInstanceStore(version string, runtime RuntimeStatus, instances InstanceStore) *grpc.Server {
	server := grpc.NewServer(
		grpc.MaxRecvMsgSize(MaxMessageSize),
		grpc.MaxSendMsgSize(MaxMessageSize),
		grpc.UnaryInterceptor(unaryInterceptor),
	)
	controlpb.RegisterControlServiceServer(server, &service{version: version, runtime: runtime, instances: instances})
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
	version   string
	runtime   RuntimeStatus
	instances InstanceStore
}

// InstanceStore provides saved instance records to the control service.
type InstanceStore interface {
	List() ([]hoststate.Instance, error)
	Load(string) (hoststate.Instance, error)
}

func (s *service) Ping(context.Context, *controlpb.PingRequest) (*controlpb.PingResponse, error) {
	return &controlpb.PingResponse{Status: "ok"}, nil
}

func (s *service) HostStatus(context.Context, *controlpb.HostStatusRequest) (*controlpb.HostStatusResponse, error) {
	return &controlpb.HostStatusResponse{DaemonVersion: s.version, RuntimeReady: s.runtime != nil && s.runtime.Ready()}, nil
}

func (s *service) ListInstances(context.Context, *controlpb.ListInstancesRequest) (*controlpb.ListInstancesResponse, error) {
	if s.instances == nil {
		return nil, status.Error(codes.FailedPrecondition, "instance state is unavailable")
	}
	instances, err := s.instances.List()
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot read instance state")
	}
	response := &controlpb.ListInstancesResponse{Instances: make([]*controlpb.Instance, 0, len(instances))}
	for _, instance := range instances {
		response.Instances = append(response.Instances, toProtocolInstance(instance))
	}
	return response, nil
}

func (s *service) InspectInstance(_ context.Context, request *controlpb.InspectInstanceRequest) (*controlpb.InspectInstanceResponse, error) {
	if s.instances == nil {
		return nil, status.Error(codes.FailedPrecondition, "instance state is unavailable")
	}
	instance, err := s.instances.Load(request.GetName())
	if errors.Is(err, hoststate.ErrInvalidName) {
		return nil, status.Error(codes.InvalidArgument, "invalid sandbox name")
	}
	if errors.Is(err, fs.ErrNotExist) {
		return nil, status.Error(codes.NotFound, "sandbox does not exist")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot read instance state")
	}
	return &controlpb.InspectInstanceResponse{Instance: toProtocolInstance(instance)}, nil
}

func toProtocolInstance(instance hoststate.Instance) *controlpb.Instance {
	createdAt := ""
	if !instance.CreatedAt.IsZero() {
		createdAt = instance.CreatedAt.Format(time.RFC3339Nano)
	}
	return &controlpb.Instance{
		SchemaVersion:  int32(instance.SchemaVersion),
		Id:             instance.ID,
		Name:           instance.Name,
		CreatedAt:      createdAt,
		Vcpus:          int32(instance.VCPUs),
		MemoryMib:      int32(instance.MemoryMiB),
		StateDiskBytes: instance.StateDiskBytes,
		Image:          instance.Image,
		ImageVersion:   instance.ImageVersion,
		HostUid:        uint32(instance.HostUID),
		HostGid:        uint32(instance.HostGID),
		Subnet:         instance.Subnet,
		HostIp:         instance.HostIP,
		GuestIp:        instance.GuestIP,
		TapDevice:      instance.TapDevice,
		GuestMac:       instance.GuestMAC,
		SshUser:        instance.SSHUser,
		SshAlias:       instance.SSHAlias,
	}
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
