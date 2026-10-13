package daemon_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"sandboxctl/cmd/sandboxd/daemon"
	"sandboxctl/internal/controlpb"
	"sandboxctl/internal/controlsettings"
	"sandboxctl/internal/forward"
	"sandboxctl/internal/hoststate"

	"golang.org/x/crypto/ssh"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestForwardRPCsAndLifecycle(t *testing.T) {
	paths := hoststate.PathsAt(filepath.Join(t.TempDir(), "state"))
	store := hoststate.Store{Paths: paths}
	instance := daemonTestInstance("0123456789abcdef0123456789abcdef", "dev1")
	if err := store.Save(instance); err != nil {
		t.Fatal(err)
	}
	runtime := &resetTestRuntimeController{testRuntimeController: testRuntimeController{running: map[string]bool{"dev1": true}}}
	manager := &resetTestInstanceManager{Manager: hoststate.Manager{Store: store}}
	forwards := forward.New(store, runtime.Running)
	ctx, cancel := context.WithCancel(context.Background())
	go forwards.Run(ctx)
	t.Cleanup(func() { cancel(); forwards.Close() })
	connection := startServerWithGRPCServer(t, daemon.NewServerWithForwarding("test", runtime, manager, paths, forwards))
	service := controlpb.NewControlServiceClient(connection)
	call, cancelCall := callContext(controlsettings.APIVersion, "0123456789abcdef0123456789abcdef")
	defer cancelCall()
	prepared, err := service.PrepareForward(call, &controlpb.PrepareForwardRequest{Name: "dev1"})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.GetInstance().GetId() != instance.ID || !strings.HasPrefix(prepared.GetPublicKey(), "ssh-ed25519 ") || strings.Contains(prepared.GetPublicKey(), "PRIVATE") {
		t.Fatalf("bad preparation: %+v", prepared)
	}
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	key := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	occupied, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	preferred := uint32(occupied.Addr().(*net.TCPAddr).Port)
	added, err := service.AddForward(call, &controlpb.AddForwardRequest{Name: "dev1", InstanceId: instance.ID, GuestPort: 3000, PreferredHostPort: &preferred, HostKey: key})
	if err != nil {
		t.Fatal(err)
	}
	if added.GetForward().GetHostPort() == preferred || added.GetForward().GetPreferredHostPort() != preferred {
		t.Fatalf("no fallback: %+v", added)
	}
	for _, request := range []*controlpb.AddForwardRequest{
		{Name: "dev1", InstanceId: "other", GuestPort: 3000, HostKey: key},
		{Name: "dev1", InstanceId: instance.ID, GuestPort: 0, HostKey: key},
		{Name: "dev1", InstanceId: instance.ID, GuestPort: 65536, HostKey: key},
		{Name: "dev1", InstanceId: instance.ID, GuestPort: 3000, HostKey: "invalid"},
	} {
		if _, err := service.AddForward(call, request); err == nil {
			t.Fatalf("accepted invalid request: %+v", request)
		}
	}
	if _, err = service.StopInstance(call, &controlpb.StopInstanceRequest{Name: "dev1"}); err != nil {
		t.Fatal(err)
	}
	listed, err := service.ListForwards(call, &controlpb.ListForwardsRequest{Name: "dev1"})
	if err != nil || len(listed.GetForwards()) != 1 || listed.GetForwards()[0].GetState() != "stopped" {
		t.Fatalf("bad stopped mappings: %v %v", listed, err)
	}
	if _, err = service.PrepareForward(call, &controlpb.PrepareForwardRequest{Name: "dev1"}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("prepared stopped sandbox: %v", err)
	}
	if _, err = service.StartInstance(call, &controlpb.StartInstanceRequest{Name: "dev1"}); err != nil {
		t.Fatal(err)
	}
	listed, err = service.ListForwards(call, &controlpb.ListForwardsRequest{Name: "dev1"})
	if err != nil || listed.GetForwards()[0].GetState() == "stopped" {
		t.Fatalf("not restored: %v %v", listed, err)
	}
	if _, err = service.StopInstance(call, &controlpb.StopInstanceRequest{Name: "dev1"}); err != nil {
		t.Fatal(err)
	}
	if _, err = service.ResetInstance(call, &controlpb.ResetInstanceRequest{Name: "dev1"}); err != nil {
		t.Fatal(err)
	}
	saved, err := store.Load("dev1")
	if err != nil || len(saved.Forwards) != 1 || saved.ForwardHostKey != "" {
		t.Fatalf("reset did not preserve mappings and clear trust: %+v %v", saved, err)
	}
	if _, err = service.RemoveForward(call, &controlpb.RemoveForwardRequest{Name: "dev1", GuestPort: 3000}); err != nil {
		t.Fatal(err)
	}
	if _, err = service.RemoveForward(call, &controlpb.RemoveForwardRequest{Name: "dev1", GuestPort: 3000}); status.Code(err) != codes.NotFound {
		t.Fatalf("missing mapping: %v", err)
	}
}
