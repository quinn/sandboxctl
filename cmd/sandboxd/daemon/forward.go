package daemon

import (
	"context"
	"errors"
	"io/fs"

	"sandboxctl/internal/controlpb"
	"sandboxctl/internal/forward"
	"sandboxctl/internal/hoststate"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// NewServerWithForwarding adds daemon-managed guest TCP forwarding.
func NewServerWithForwarding(version string, runtime RuntimeStatus, instances InstanceManager, paths hoststate.Paths, manager *forward.Manager) *grpc.Server {
	return newServerConfigured(version, runtime, instances, instances, &paths, manager)
}

func (s *service) lockForwarding(ctx context.Context) (func(), error) {
	if s.forwards == nil {
		return func() {}, nil
	}
	unlock, err := s.forwards.Lock(ctx)
	if err != nil {
		return nil, status.FromContextError(err).Err()
	}
	return unlock, nil
}
func (s *service) forwardInstance(name string, running bool) (hoststate.Instance, error) {
	if s.forwards == nil || s.instances == nil {
		return hoststate.Instance{}, status.Error(codes.FailedPrecondition, "port forwarding is unavailable")
	}
	instance, err := s.instances.Load(name)
	if errors.Is(err, hoststate.ErrInvalidName) {
		return instance, status.Error(codes.InvalidArgument, "invalid sandbox name")
	}
	if errors.Is(err, fs.ErrNotExist) {
		return instance, status.Error(codes.NotFound, "sandbox does not exist")
	}
	if err != nil {
		return instance, status.Error(codes.Internal, "cannot read sandbox state")
	}
	if running && !s.forwards.Running(name) {
		return instance, status.Error(codes.FailedPrecondition, "start the sandbox before forwarding a port")
	}
	return instance, nil
}
func (s *service) PrepareForward(ctx context.Context, request *controlpb.PrepareForwardRequest) (*controlpb.PrepareForwardResponse, error) {
	unlock, err := s.lockForwarding(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	instance, err := s.forwardInstance(request.GetName(), true)
	if err != nil {
		return nil, err
	}
	key, err := s.forwards.Prepare(instance)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot prepare forwarding key")
	}
	return &controlpb.PrepareForwardResponse{Instance: s.protocolInstance(instance), PublicKey: key, HostKey: instance.ForwardHostKey}, nil
}
func (s *service) AddForward(ctx context.Context, request *controlpb.AddForwardRequest) (*controlpb.AddForwardResponse, error) {
	if request.GetGuestPort() < 1 || request.GetGuestPort() > 65535 || (request.PreferredHostPort != nil && (request.GetPreferredHostPort() < 1024 || request.GetPreferredHostPort() > 65535)) {
		return nil, status.Error(codes.InvalidArgument, "guest ports range from 1 to 65535; host ports range from 1024 to 65535")
	}
	if _, err := forward.ParseHostKey(request.GetHostKey()); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	unlock, err := s.lockForwarding(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	instance, err := s.forwardInstance(request.GetName(), true)
	if err != nil {
		return nil, err
	}
	if instance.ID != request.GetInstanceId() {
		return nil, status.Error(codes.FailedPrecondition, "sandbox identity changed during forwarding setup")
	}
	var preferred *uint16
	if request.PreferredHostPort != nil {
		port := uint16(request.GetPreferredHostPort())
		preferred = &port
	}
	mapping, err := s.forwards.Add(instance, uint16(request.GetGuestPort()), preferred, request.GetHostKey(), request.GetReplaceHostKey())
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	return &controlpb.AddForwardResponse{Forward: protocolForward(mapping)}, nil
}
func (s *service) ListForwards(ctx context.Context, request *controlpb.ListForwardsRequest) (*controlpb.ListForwardsResponse, error) {
	unlock, err := s.lockForwarding(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	instance, err := s.forwardInstance(request.GetName(), false)
	if err != nil {
		return nil, err
	}
	response := &controlpb.ListForwardsResponse{}
	for _, item := range s.forwards.List(instance) {
		response.Forwards = append(response.Forwards, protocolForward(item))
	}
	return response, nil
}
func (s *service) RemoveForward(ctx context.Context, request *controlpb.RemoveForwardRequest) (*controlpb.RemoveForwardResponse, error) {
	if request.GetGuestPort() < 1 || request.GetGuestPort() > 65535 {
		return nil, status.Error(codes.InvalidArgument, "guest port must range from 1 to 65535")
	}
	unlock, err := s.lockForwarding(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	instance, err := s.forwardInstance(request.GetName(), false)
	if err != nil {
		return nil, err
	}
	if err = s.forwards.Remove(instance, uint16(request.GetGuestPort())); errors.Is(err, fs.ErrNotExist) {
		return nil, status.Error(codes.NotFound, "port mapping does not exist")
	} else if err != nil {
		return nil, status.Error(codes.Internal, "cannot remove port mapping")
	}
	return &controlpb.RemoveForwardResponse{}, nil
}
func protocolForward(item forward.Status) *controlpb.Forward {
	return &controlpb.Forward{GuestPort: uint32(item.Mapping.GuestPort), PreferredHostPort: uint32(item.Mapping.PreferredHostPort), HostPort: uint32(item.Mapping.LastHostPort), State: item.State, Error: item.Error}
}
