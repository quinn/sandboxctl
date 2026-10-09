package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
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
	MaxCreateDuration     = 2 * time.Minute
	MaxStartDuration      = 2 * time.Minute
	MaxStopDuration       = 30 * time.Second
	MaxResetDuration      = 3 * time.Minute
	MaxDeleteDuration     = 30 * time.Second
	MaxInstallDuration    = 30 * time.Minute
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
	return newServer(version, runtime, instances, nil)
}

// NewServerWithInstanceManager creates a gRPC server with create and inspect operations.
func NewServerWithInstanceManager(version string, runtime RuntimeStatus, instances InstanceManager) *grpc.Server {
	return newServer(version, runtime, instances, instances)
}

// NewServerWithImageStore creates a control server that can import images into paths.
func NewServerWithImageStore(version string, runtime RuntimeStatus, instances InstanceManager, paths hoststate.Paths) *grpc.Server {
	return newServerWithImageStore(version, runtime, instances, instances, paths)
}

func newServer(version string, runtime RuntimeStatus, instances InstanceStore, creator InstanceCreator) *grpc.Server {
	return newServerConfigured(version, runtime, instances, creator, nil)
}

func newServerWithImageStore(version string, runtime RuntimeStatus, instances InstanceStore, creator InstanceCreator, paths hoststate.Paths) *grpc.Server {
	return newServerConfigured(version, runtime, instances, creator, &paths)
}

func newServerConfigured(version string, runtime RuntimeStatus, instances InstanceStore, creator InstanceCreator, imagePaths *hoststate.Paths) *grpc.Server {
	server := grpc.NewServer(
		grpc.MaxRecvMsgSize(MaxMessageSize),
		grpc.MaxSendMsgSize(MaxMessageSize),
		grpc.UnaryInterceptor(unaryInterceptor),
		grpc.StreamInterceptor(streamInterceptor),
	)
	controller, _ := runtime.(RuntimeController)
	deleter, _ := creator.(InstanceDeleter)
	resetter, _ := creator.(InstanceResetter)
	controlpb.RegisterControlServiceServer(server, &service{version: version, runtime: runtime, controller: controller, instances: instances, creator: creator, deleter: deleter, resetter: resetter, imagePaths: imagePaths, imageSlot: make(chan struct{}, 1)})
	return server
}

func streamInterceptor(server any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	incoming, ok := metadata.FromIncomingContext(stream.Context())
	if !ok {
		return status.Error(codes.InvalidArgument, "request metadata is required")
	}
	apiVersions := incoming.Get(APIVersionMetadataKey)
	if len(apiVersions) != 1 || apiVersions[0] != APIVersion {
		return status.Error(codes.FailedPrecondition, "unsupported API version")
	}
	requestIDs := incoming.Get(RequestIDMetadataKey)
	if len(requestIDs) != 1 || !requestIDPattern.MatchString(requestIDs[0]) {
		return status.Error(codes.InvalidArgument, "request identifier is missing or invalid")
	}
	ctx := stream.Context()
	if _, ok := ctx.Deadline(); !ok {
		return status.Error(codes.InvalidArgument, "request deadline is required")
	}
	duration := MaxOperationDuration
	if info.FullMethod == controlpb.ControlService_InstallImage_FullMethodName {
		duration = MaxInstallDuration
	}
	ctx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	if err := grpc.SetHeader(ctx, metadata.Pairs(RequestIDMetadataKey, requestIDs[0])); err != nil {
		return status.Error(codes.Internal, "cannot set response request identifier")
	}
	started := time.Now()
	slog.Info("RPC started", "method", info.FullMethod, "request_id", requestIDs[0])
	err := handler(server, &contextServerStream{ServerStream: stream, ctx: ctx})
	logRPCResult(info.FullMethod, requestIDs[0], started, err)
	return err
}

type contextServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (stream *contextServerStream) Context() context.Context { return stream.ctx }

func unaryInterceptor(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
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
	duration := MaxOperationDuration
	if info.FullMethod == controlpb.ControlService_CreateInstance_FullMethodName {
		duration = MaxCreateDuration
	} else if info.FullMethod == controlpb.ControlService_StartInstance_FullMethodName {
		duration = MaxStartDuration
	} else if info.FullMethod == controlpb.ControlService_StopInstance_FullMethodName {
		duration = MaxStopDuration
	} else if info.FullMethod == controlpb.ControlService_ResetInstance_FullMethodName {
		duration = MaxResetDuration
	} else if info.FullMethod == controlpb.ControlService_DeleteInstance_FullMethodName {
		duration = MaxDeleteDuration
	}
	ctx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	if err := grpc.SetHeader(ctx, metadata.Pairs(RequestIDMetadataKey, requestIDs[0])); err != nil {
		return nil, status.Error(codes.Internal, "cannot set response request identifier")
	}
	started := time.Now()
	slog.Info("RPC started", "method", info.FullMethod, "request_id", requestIDs[0])
	response, err := handler(ctx, request)
	logRPCResult(info.FullMethod, requestIDs[0], started, err)
	return response, err
}

func logRPCResult(method, requestID string, started time.Time, err error) {
	attributes := []any{"method", method, "request_id", requestID, "duration", time.Since(started), "status", status.Code(err).String()}
	if err != nil {
		slog.Error("RPC failed", attributes...)
		return
	}
	slog.Info("RPC completed", attributes...)
}

type service struct {
	controlpb.UnimplementedControlServiceServer
	version    string
	runtime    RuntimeStatus
	controller RuntimeController
	instances  InstanceStore
	creator    InstanceCreator
	deleter    InstanceDeleter
	resetter   InstanceResetter
	imagePaths *hoststate.Paths
	imageSlot  chan struct{}
}

// InstanceStore provides saved instance records to the control service.
type InstanceStore interface {
	List() ([]hoststate.Instance, error)
	Load(string) (hoststate.Instance, error)
}

// InstanceCreator creates a stopped instance and reserves its host resources.
type InstanceCreator interface {
	Create(context.Context, hoststate.CreateOptions) (hoststate.Instance, error)
}

// InstanceManager provides saved instance state and creates stopped instances.
type InstanceManager interface {
	InstanceStore
	InstanceCreator
}

// InstanceDeleter removes a stopped instance and releases its allocation.
type InstanceDeleter interface {
	Delete(hoststate.Instance) error
}

// InstanceResetter replaces the writable disk for an existing stopped instance.
type InstanceResetter interface {
	Reset(context.Context, hoststate.Instance) error
}

// RuntimeController starts and stops jailed Firecracker machines.
type RuntimeController interface {
	Start(context.Context, hoststate.Instance) error
	Stop(context.Context, string, bool) error
	Running(string) bool
}

// RuntimeDeleter proves runtime resources are gone before removing instance state.
type RuntimeDeleter interface {
	DeleteStopped(context.Context, hoststate.Instance, func() error) error
}

// RuntimeResetter proves the VM is stopped before it calls reset.
type RuntimeResetter interface {
	ResetStopped(context.Context, hoststate.Instance, func() error) error
}

// RuntimeStateReporter reports persisted runtime state after daemon restart.
type RuntimeStateReporter interface {
	RuntimeState(string) string
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
		response.Instances = append(response.Instances, s.protocolInstance(instance))
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
	return &controlpb.InspectInstanceResponse{Instance: s.protocolInstance(instance)}, nil
}

func (s *service) CreateInstance(ctx context.Context, request *controlpb.CreateInstanceRequest) (*controlpb.CreateInstanceResponse, error) {
	if s.creator == nil {
		return nil, status.Error(codes.FailedPrecondition, "instance creation is unavailable")
	}
	if request.GetVcpus() < 1 || request.GetMemoryMib() < 128 || request.GetStateDiskBytes() < 1 || request.GetImage() == "" {
		return nil, status.Error(codes.InvalidArgument, "CPU, memory, disk size, and image are required")
	}
	instance, err := s.creator.Create(ctx, hoststate.CreateOptions{
		Name: request.GetName(), VCPUs: int(request.GetVcpus()), MemoryMiB: int(request.GetMemoryMib()),
		StateDiskBytes: request.GetStateDiskBytes(), Image: request.GetImage(), SSHPublicKey: request.GetSshPublicKey(),
	})
	if errors.Is(err, hoststate.ErrInvalidName) || errors.Is(err, hoststate.ErrInvalidCreateOptions) || errors.Is(err, hoststate.ErrInvalidSSHPublicKey) {
		return nil, status.Error(codes.InvalidArgument, "create request is invalid")
	}
	if errors.Is(err, hoststate.ErrAlreadyExists) {
		return nil, status.Error(codes.AlreadyExists, "sandbox already exists")
	}
	if errors.Is(err, hoststate.ErrImageNotInstalled) || errors.Is(err, hoststate.ErrImageLookupUnavailable) {
		return nil, status.Error(codes.FailedPrecondition, "requested image is not installed")
	}
	if errors.Is(err, context.Canceled) {
		return nil, status.Error(codes.Canceled, "create request was canceled")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return nil, status.Error(codes.DeadlineExceeded, "create request exceeded its time limit")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot create sandbox instance")
	}
	return &controlpb.CreateInstanceResponse{Instance: s.protocolInstance(instance)}, nil
}

func (s *service) StartInstance(ctx context.Context, request *controlpb.StartInstanceRequest) (*controlpb.StartInstanceResponse, error) {
	if s.controller == nil || s.instances == nil {
		return nil, status.Error(codes.FailedPrecondition, "sandbox runtime is unavailable")
	}
	instance, err := s.instances.Load(request.GetName())
	if errors.Is(err, hoststate.ErrInvalidName) {
		return nil, status.Error(codes.InvalidArgument, "invalid sandbox name")
	}
	if errors.Is(err, fs.ErrNotExist) {
		return nil, status.Error(codes.NotFound, "sandbox does not exist")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot read sandbox state")
	}
	if err := s.controller.Start(ctx, instance); err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, status.Error(codes.Canceled, "start request was canceled")
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, status.Error(codes.DeadlineExceeded, "start request exceeded its time limit")
		}
		if errors.Is(err, hoststate.ErrAlreadyStarting) {
			return nil, status.Error(codes.Aborted, "sandbox is already starting")
		}
		if errors.Is(err, hoststate.ErrAlreadyRunning) {
			return nil, status.Error(codes.FailedPrecondition, "sandbox is already running")
		}
		slog.Error("start sandbox failed", "name", instance.Name, "error", err)
		return nil, status.Error(codes.FailedPrecondition, "cannot start sandbox")
	}
	return &controlpb.StartInstanceResponse{Instance: s.protocolInstance(instance)}, nil
}

func (s *service) StopInstance(ctx context.Context, request *controlpb.StopInstanceRequest) (*controlpb.StopInstanceResponse, error) {
	if s.controller == nil || s.instances == nil {
		return nil, status.Error(codes.FailedPrecondition, "sandbox runtime is unavailable")
	}
	instance, err := s.instances.Load(request.GetName())
	if errors.Is(err, hoststate.ErrInvalidName) {
		return nil, status.Error(codes.InvalidArgument, "invalid sandbox name")
	}
	if errors.Is(err, fs.ErrNotExist) {
		return nil, status.Error(codes.NotFound, "sandbox does not exist")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot read sandbox state")
	}
	if err := s.controller.Stop(ctx, instance.Name, request.GetForce()); err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, status.Error(codes.Canceled, "stop request was canceled")
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, status.Error(codes.DeadlineExceeded, "stop request exceeded its time limit")
		}
		return nil, status.Error(codes.FailedPrecondition, "cannot stop sandbox")
	}
	return &controlpb.StopInstanceResponse{Instance: s.protocolInstance(instance)}, nil
}

func (s *service) ResetInstance(ctx context.Context, request *controlpb.ResetInstanceRequest) (*controlpb.ResetInstanceResponse, error) {
	if s.instances == nil || s.resetter == nil {
		return nil, status.Error(codes.FailedPrecondition, "state disk reset is unavailable")
	}
	runtimeResetter, ok := s.controller.(RuntimeResetter)
	if !ok {
		return nil, status.Error(codes.FailedPrecondition, "runtime reset checks are unavailable")
	}
	instance, err := s.instances.Load(request.GetName())
	if errors.Is(err, hoststate.ErrInvalidName) {
		return nil, status.Error(codes.InvalidArgument, "invalid sandbox name")
	}
	if errors.Is(err, fs.ErrNotExist) {
		return nil, status.Error(codes.NotFound, "sandbox does not exist")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot read sandbox state")
	}
	slog.Info("resetting sandbox state disk", "name", instance.Name, "instance_id", instance.ID, "size_bytes", instance.StateDiskBytes)
	if err := runtimeResetter.ResetStopped(ctx, instance, func() error {
		return s.resetter.Reset(ctx, instance)
	}); err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, status.Error(codes.Canceled, "reset request was canceled")
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, status.Error(codes.DeadlineExceeded, "reset request exceeded its time limit")
		}
		slog.Error("reset sandbox failed", "name", instance.Name, "error", err)
		return nil, status.Error(codes.FailedPrecondition, "cannot reset sandbox")
	}
	return &controlpb.ResetInstanceResponse{Instance: s.protocolInstance(instance)}, nil
}

func (s *service) DeleteInstance(ctx context.Context, request *controlpb.DeleteInstanceRequest) (*controlpb.DeleteInstanceResponse, error) {
	if s.instances == nil || s.deleter == nil {
		return nil, status.Error(codes.FailedPrecondition, "instance deletion is unavailable")
	}
	runtimeDeleter, ok := s.controller.(RuntimeDeleter)
	if !ok {
		return nil, status.Error(codes.FailedPrecondition, "runtime cleanup is unavailable")
	}
	instance, err := s.instances.Load(request.GetName())
	if errors.Is(err, hoststate.ErrInvalidName) {
		return nil, status.Error(codes.InvalidArgument, "invalid sandbox name")
	}
	if errors.Is(err, fs.ErrNotExist) {
		return nil, status.Error(codes.NotFound, "sandbox does not exist")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot read sandbox state")
	}
	if err := runtimeDeleter.DeleteStopped(ctx, instance, func() error { return s.deleter.Delete(instance) }); err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, status.Error(codes.Canceled, "delete request was canceled")
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, status.Error(codes.DeadlineExceeded, "delete request exceeded its time limit")
		}
		slog.Error("delete sandbox failed", "name", instance.Name, "error", err)
		return nil, status.Error(codes.FailedPrecondition, "cannot delete sandbox")
	}
	return &controlpb.DeleteInstanceResponse{Name: instance.Name}, nil
}

func (s *service) InstallImage(stream controlpb.ControlService_InstallImageServer) error {
	if s.imagePaths == nil {
		return status.Error(codes.FailedPrecondition, "image installation is unavailable")
	}
	select {
	case s.imageSlot <- struct{}{}:
		defer func() { <-s.imageSlot }()
	case <-stream.Context().Done():
		if errors.Is(stream.Context().Err(), context.DeadlineExceeded) {
			return status.Error(codes.DeadlineExceeded, "image import exceeded its time limit")
		}
		return status.Error(codes.Canceled, "image import was canceled")
	}
	first, err := stream.Recv()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return status.Error(codes.InvalidArgument, "image manifest is required")
		}
		return err
	}
	if first.GetArtifactName() != "manifest.json" || first.GetOffset() != 0 || first.GetName() == "" || first.GetVersion() == "" {
		return status.Error(codes.InvalidArgument, "first image chunk must contain the manifest")
	}
	image, err := hoststate.BeginImageImport(*s.imagePaths, first.GetName(), first.GetVersion(), first.GetData())
	if err != nil {
		if errors.Is(err, hoststate.ErrImageAlreadyInstalled) {
			return status.Error(codes.AlreadyExists, "image is already installed")
		}
		if errors.Is(err, hoststate.ErrInvalidName) || errors.Is(err, hoststate.ErrInvalidImageManifest) {
			return status.Error(codes.InvalidArgument, "image manifest or identity is invalid")
		}
		return status.Error(codes.Internal, "cannot prepare image import")
	}
	defer image.Abort()
	for {
		request, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if request.GetName() != first.GetName() || request.GetVersion() != first.GetVersion() {
			return status.Error(codes.InvalidArgument, "image identity changed during import")
		}
		if err := image.WriteChunk(request.GetArtifactName(), request.GetOffset(), request.GetData()); err != nil {
			if errors.Is(err, hoststate.ErrInvalidImageChunk) {
				return status.Error(codes.InvalidArgument, "image artifact chunk is invalid")
			}
			return status.Error(codes.Internal, "cannot write image artifact")
		}
	}
	if err := image.Commit(); err != nil {
		if errors.Is(err, hoststate.ErrImageAlreadyInstalled) {
			return status.Error(codes.AlreadyExists, "image is already installed")
		}
		if errors.Is(err, hoststate.ErrInvalidImageChunk) {
			return status.Error(codes.InvalidArgument, "image import is incomplete")
		}
		return status.Error(codes.Internal, "cannot publish installed image")
	}
	if err := stream.SendAndClose(&controlpb.InstallImageResponse{Name: first.GetName(), Version: first.GetVersion()}); err != nil {
		return status.Error(codes.Internal, "cannot return image installation result")
	}
	return nil
}

func (s *service) protocolInstance(instance hoststate.Instance) *controlpb.Instance {
	state := "stopped"
	if reporter, ok := s.controller.(RuntimeStateReporter); ok {
		state = reporter.RuntimeState(instance.Name)
	} else if s.controller != nil && s.controller.Running(instance.Name) {
		state = "running"
	}
	return toProtocolInstance(instance, state)
}

func toProtocolInstance(instance hoststate.Instance, runtimeState string) *controlpb.Instance {
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
		RuntimeState:   runtimeState,
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
