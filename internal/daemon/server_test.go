package daemon_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"sandboxctl/internal/controlpb"
	"sandboxctl/internal/daemon"
	"sandboxctl/internal/hoststate"
	"sandboxctl/internal/imagebuild/manifest"

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
	if listed.GetInstances()[0].GetHostUid() != 300001 || listed.GetInstances()[0].GetStateDiskBytes() == 0 || listed.GetInstances()[0].GetRuntimeState() != "stopped" {
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

func TestServerCreatesStoppedInstance(t *testing.T) {
	paths := hoststate.PathsAt(filepath.Join(t.TempDir(), "state"))
	manager := hoststate.Manager{
		Store: hoststate.Store{Paths: paths},
		Allocator: hoststate.Allocator{
			Paths: paths,
			Config: hoststate.AllocationConfig{
				UIDRange:    hoststate.IDRange{Start: 200000, Count: 8},
				GIDRange:    hoststate.IDRange{Start: 300000, Count: 8},
				AddressPool: netip.MustParsePrefix("172.30.0.0/16"),
			},
			TapInUse: func(string) bool { return false },
		},
		ImageVersions: hoststate.StaticImageVersions{"default": "sha256:0123456789abcdef"},
		StateDisks:    daemonTestDiskBuilder{},
	}
	connection := startServerWithInstances(t, manager)
	service := controlpb.NewControlServiceClient(connection)
	ctx, cancel := callContext(daemon.APIVersion, "44444444444444444444444444444444")
	response, err := service.CreateInstance(ctx, &controlpb.CreateInstanceRequest{
		Name: "dev1", Vcpus: 2, MemoryMib: 2048, StateDiskBytes: 1 << 30,
		Image: "default", SshPublicKey: daemonTestPublicKey(),
	})
	cancel()
	if err != nil {
		t.Fatalf("CreateInstance(): %v", err)
	}
	instance := response.GetInstance()
	if instance.GetName() != "dev1" || instance.GetGuestIp() != "172.30.0.2" || instance.GetRuntimeState() != "stopped" || instance.GetSshAlias() != "sandbox-dev1" {
		t.Fatalf("CreateInstance() = %v", instance)
	}
	if key, err := manager.Store.LoadPublicKey("dev1"); err != nil || key != daemonTestPublicKey() {
		t.Fatalf("stored public key = %q, error = %v", key, err)
	}
	duplicateContext, duplicateCancel := callContext(daemon.APIVersion, "66666666666666666666666666666666")
	_, err = service.CreateInstance(duplicateContext, &controlpb.CreateInstanceRequest{
		Name: "dev1", Vcpus: 2, MemoryMib: 2048, StateDiskBytes: 1 << 30,
		Image: "default", SshPublicKey: daemonTestPublicKey(),
	})
	duplicateCancel()
	if status.Code(err) != codes.AlreadyExists {
		t.Fatalf("duplicate CreateInstance() code = %s, want AlreadyExists (%v)", status.Code(err), err)
	}
	invalidContext, invalidCancel := callContext(daemon.APIVersion, "77777777777777777777777777777777")
	_, err = service.CreateInstance(invalidContext, &controlpb.CreateInstanceRequest{
		Name: "../bad", Vcpus: 1, MemoryMib: 128, StateDiskBytes: 1 << 30,
		Image: "default", SshPublicKey: daemonTestPublicKey(),
	})
	invalidCancel()
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid CreateInstance() code = %s, want InvalidArgument (%v)", status.Code(err), err)
	}
	missingImageContext, missingImageCancel := callContext(daemon.APIVersion, "88888888888888888888888888888888")
	_, err = service.CreateInstance(missingImageContext, &controlpb.CreateInstanceRequest{
		Name: "dev2", Vcpus: 1, MemoryMib: 128, StateDiskBytes: 1 << 30,
		Image: "missing", SshPublicKey: daemonTestPublicKey(),
	})
	missingImageCancel()
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("missing image CreateInstance() code = %s, want FailedPrecondition (%v)", status.Code(err), err)
	}
}

func TestServerDeletesOnlyStoppedInstances(t *testing.T) {
	paths := hoststate.PathsAt(filepath.Join(t.TempDir(), "state"))
	store := hoststate.Store{Paths: paths}
	id, err := hoststate.NewID()
	if err != nil {
		t.Fatal(err)
	}
	instance := daemonTestInstance(id, "dev1")
	if err := store.Save(instance); err != nil {
		t.Fatal(err)
	}
	manager := &deleteTestInstanceManager{Manager: hoststate.Manager{Store: store}}
	runtime := &deleteTestRuntimeController{testRuntimeController: testRuntimeController{running: map[string]bool{"dev1": true}}}
	server := daemon.NewServerWithInstanceManager("test-version", runtime, manager)
	connection := startServerWithGRPCServer(t, server)
	service := controlpb.NewControlServiceClient(connection)
	ctx, cancel := callContext(daemon.APIVersion, "adadadadadadadadadadadadadadadad")
	_, err = service.DeleteInstance(ctx, &controlpb.DeleteInstanceRequest{Name: "dev1"})
	cancel()
	if status.Code(err) != codes.FailedPrecondition || manager.deleted {
		t.Fatalf("DeleteInstance() for running sandbox = %v, deleted=%t", err, manager.deleted)
	}
	runtime.running["dev1"] = false
	ctx, cancel = callContext(daemon.APIVersion, "aeaeaeaeaeaeaeaeaeaeaeaeaeaeaeae")
	response, err := service.DeleteInstance(ctx, &controlpb.DeleteInstanceRequest{Name: "dev1"})
	cancel()
	if err != nil || response.GetName() != "dev1" || !manager.deleted {
		t.Fatalf("DeleteInstance() for stopped sandbox = %v, response=%v, deleted=%t", err, response, manager.deleted)
	}
}

func TestServerResetsOnlyStoppedInstance(t *testing.T) {
	paths := hoststate.PathsAt(filepath.Join(t.TempDir(), "state"))
	store := hoststate.Store{Paths: paths}
	id, err := hoststate.NewID()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(daemonTestInstance(id, "dev1")); err != nil {
		t.Fatal(err)
	}
	manager := &resetTestInstanceManager{Manager: hoststate.Manager{Store: store}}
	runtime := &resetTestRuntimeController{testRuntimeController: testRuntimeController{running: map[string]bool{"dev1": true}}}
	connection := startServerWithGRPCServer(t, daemon.NewServerWithInstanceManager("test-version", runtime, manager))
	service := controlpb.NewControlServiceClient(connection)
	ctx, cancel := callContext(daemon.APIVersion, "afafafafafafafafafafafafafafafaf")
	_, err = service.ResetInstance(ctx, &controlpb.ResetInstanceRequest{Name: "dev1"})
	cancel()
	if status.Code(err) != codes.FailedPrecondition || manager.reset {
		t.Fatalf("ResetInstance() for running sandbox = %v, reset=%t", err, manager.reset)
	}
	runtime.running["dev1"] = false
	ctx, cancel = callContext(daemon.APIVersion, "bfbfbfbfbfbfbfbfbfbfbfbfbfbfbfbf")
	response, err := service.ResetInstance(ctx, &controlpb.ResetInstanceRequest{Name: "dev1"})
	cancel()
	if err != nil || response.GetInstance().GetName() != "dev1" || !manager.reset {
		t.Fatalf("ResetInstance() for stopped sandbox = %v, response=%v, reset=%t", err, response, manager.reset)
	}
}

func TestServerImportsVerifiedImageStream(t *testing.T) {
	paths := hoststate.PathsAt(filepath.Join(t.TempDir(), "state"))
	manager := hoststate.Manager{
		Store: hoststate.Store{Paths: paths},
		Allocator: hoststate.Allocator{Paths: paths, Config: hoststate.AllocationConfig{
			UIDRange: hoststate.IDRange{Start: 200000, Count: 4}, GIDRange: hoststate.IDRange{Start: 300000, Count: 4},
			AddressPool: netip.MustParsePrefix("172.30.0.0/16"),
		}},
		ImageVersions: hoststate.StaticImageVersions{}, StateDisks: daemonTestDiskBuilder{},
	}
	connection := startServerWithImageStore(t, manager, paths)
	service := controlpb.NewControlServiceClient(connection)
	artifacts := map[string][]byte{"vmlinux": []byte("kernel"), "initramfs.cpio.gz": []byte("initramfs"), "root.squashfs": []byte("squashfs")}
	artifactInfo := make(map[string]manifest.Artifact, len(artifacts))
	for name, data := range artifacts {
		digest := sha256.Sum256(data)
		artifactInfo[name] = manifest.Artifact{Size: int64(len(data)), SHA256: hex.EncodeToString(digest[:])}
	}
	imageManifest := manifest.Manifest{
		SchemaVersion: 1, Name: "default", Architecture: "x86_64", Firecracker: "1.17.x",
		KernelVersion: "6.1.155", Guest: manifest.Guest{Distribution: "Ubuntu", Version: "24.04"},
		Artifacts: artifactInfo, KernelArgs: []string{"console=ttyS0", "rdinit=/init"}, Provenance: "test",
	}
	version := manifest.VersionForManifest(imageManifest)
	imageManifest.Version = version
	manifestData, err := json.Marshal(imageManifest)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := callContext(daemon.APIVersion, "77777777777777777777777777777777")
	defer cancel()
	stream, err := service.InstallImage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&controlpb.InstallImageRequest{Name: "default", Version: version, ArtifactName: "manifest.json", Data: manifestData}); err != nil {
		t.Fatal(err)
	}
	for _, name := range manifest.ArtifactNames() {
		if err := stream.Send(&controlpb.InstallImageRequest{Name: "default", Version: version, ArtifactName: name, Data: artifacts[name]}); err != nil {
			t.Fatal(err)
		}
	}
	response, err := stream.CloseAndRecv()
	if err != nil {
		t.Fatalf("InstallImage(): %v", err)
	}
	if response.GetName() != "default" || response.GetVersion() != version {
		t.Fatalf("InstallImage() = %v", response)
	}
	if _, err := (hoststate.InstalledImages{Paths: paths}).ResolveBootImage("default", version); err != nil {
		t.Fatalf("resolve imported image: %v", err)
	}
}

func TestServerStartsAndStopsInstanceThroughRuntimeController(t *testing.T) {
	paths := hoststate.PathsAt(filepath.Join(t.TempDir(), "state"))
	store := hoststate.Store{Paths: paths}
	id, err := hoststate.NewID()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(daemonTestInstance(id, "dev1")); err != nil {
		t.Fatal(err)
	}
	runtime := &testRuntimeController{running: make(map[string]bool)}
	connection := startServerWithRuntime(t, store, runtime)
	service := controlpb.NewControlServiceClient(connection)

	startCtx, startCancel := callContext(daemon.APIVersion, "77777777777777777777777777777777")
	started, err := service.StartInstance(startCtx, &controlpb.StartInstanceRequest{Name: "dev1"})
	startCancel()
	if err != nil {
		t.Fatalf("StartInstance(): %v", err)
	}
	if started.GetInstance().GetRuntimeState() != "running" || !runtime.Running("dev1") {
		t.Fatalf("StartInstance() response = %v", started.GetInstance())
	}

	listCtx, listCancel := callContext(daemon.APIVersion, "88888888888888888888888888888888")
	listed, err := service.ListInstances(listCtx, &controlpb.ListInstancesRequest{})
	listCancel()
	if err != nil || len(listed.GetInstances()) != 1 || listed.GetInstances()[0].GetRuntimeState() != "running" {
		t.Fatalf("ListInstances() after start = %v, %v", listed, err)
	}

	stopCtx, stopCancel := callContext(daemon.APIVersion, "99999999999999999999999999999999")
	stopped, err := service.StopInstance(stopCtx, &controlpb.StopInstanceRequest{Name: "dev1", Force: true})
	stopCancel()
	if err != nil {
		t.Fatalf("StopInstance(): %v", err)
	}
	if stopped.GetInstance().GetRuntimeState() != "stopped" || runtime.Running("dev1") {
		t.Fatalf("StopInstance() response = %v", stopped.GetInstance())
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if !runtime.force {
		t.Fatal("StopInstance() did not pass the force option")
	}
}

func TestServerRejectsStartAndStopForMissingInstance(t *testing.T) {
	paths := hoststate.PathsAt(filepath.Join(t.TempDir(), "state"))
	store := hoststate.Store{Paths: paths}
	connection := startServerWithRuntime(t, store, &testRuntimeController{running: make(map[string]bool)})
	service := controlpb.NewControlServiceClient(connection)
	ctx, cancel := callContext(daemon.APIVersion, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	_, err := service.StartInstance(ctx, &controlpb.StartInstanceRequest{Name: "missing"})
	cancel()
	if status.Code(err) != codes.NotFound {
		t.Fatalf("StartInstance() code = %s, want NotFound (%v)", status.Code(err), err)
	}
	ctx, cancel = callContext(daemon.APIVersion, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	_, err = service.StopInstance(ctx, &controlpb.StopInstanceRequest{Name: "missing"})
	cancel()
	if status.Code(err) != codes.NotFound {
		t.Fatalf("StopInstance() code = %s, want NotFound (%v)", status.Code(err), err)
	}
}

func TestServerLogsRuntimeStartFailure(t *testing.T) {
	paths := hoststate.PathsAt(filepath.Join(t.TempDir(), "state"))
	store := hoststate.Store{Paths: paths}
	id, err := hoststate.NewID()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(daemonTestInstance(id, "dev1")); err != nil {
		t.Fatal(err)
	}
	runtime := &testRuntimeController{
		running:  make(map[string]bool),
		startErr: fmt.Errorf("jailer failed: permission denied"),
	}
	connection := startServerWithRuntime(t, store, runtime)
	service := controlpb.NewControlServiceClient(connection)

	var output bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	ctx, cancel := callContext(daemon.APIVersion, "abababababababababababababababab")
	_, err = service.StartInstance(ctx, &controlpb.StartInstanceRequest{Name: "dev1"})
	cancel()
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("StartInstance() error = %v, want FailedPrecondition", err)
	}
	if got := output.String(); !strings.Contains(got, `msg="start sandbox failed" name=dev1 error="jailer failed: permission denied"`) {
		t.Fatalf("start failure log = %q", got)
	}
}

type testRuntimeController struct {
	mu       sync.Mutex
	running  map[string]bool
	force    bool
	startErr error
}

type deleteTestRuntimeController struct{ testRuntimeController }

func (r *deleteTestRuntimeController) DeleteStopped(_ context.Context, _ hoststate.Instance, remove func() error) error {
	if r.Running("dev1") {
		return fmt.Errorf("sandbox is running")
	}
	return remove()
}

type deleteTestInstanceManager struct {
	hoststate.Manager
	deleted bool
}

type resetTestInstanceManager struct {
	hoststate.Manager
	reset bool
}

func (manager *resetTestInstanceManager) Reset(context.Context, hoststate.Instance) error {
	manager.reset = true
	return nil
}

type resetTestRuntimeController struct{ testRuntimeController }

func (runtime *resetTestRuntimeController) ResetStopped(_ context.Context, instance hoststate.Instance, reset func() error) error {
	if runtime.Running(instance.Name) {
		return fmt.Errorf("sandbox is running")
	}
	return reset()
}

func (manager *deleteTestInstanceManager) Delete(hoststate.Instance) error {
	manager.deleted = true
	return nil
}

func (r *testRuntimeController) Ready() bool { return true }

func (r *testRuntimeController) Running(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running[name]
}

func (r *testRuntimeController) Start(_ context.Context, instance hoststate.Instance) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.startErr != nil {
		return r.startErr
	}
	r.running[instance.Name] = true
	return nil
}

func (r *testRuntimeController) Stop(_ context.Context, name string, force bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.force = force
	delete(r.running, name)
	return nil
}

type daemonTestDiskBuilder struct{}

func (daemonTestDiskBuilder) Build(_ context.Context, path string, size int64, uid, gid uint32) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte("state disk"), 0o600)
}

func daemonTestPublicKey() string {
	data := append([]byte{0, 0, 0, 11}, []byte("ssh-ed25519")...)
	data = append(data, 0, 0, 0, 32)
	data = append(data, make([]byte, 32)...)
	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString(data) + " test"
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
	return startServerWithRuntime(t, instances, nil)
}

func startServerWithRuntime(t *testing.T, instances daemon.InstanceStore, runtime daemon.RuntimeStatus) *grpc.ClientConn {
	server := daemon.NewServerWithInstanceStore("test-version", runtime, instances)
	if manager, ok := instances.(daemon.InstanceManager); ok {
		server = daemon.NewServerWithInstanceManager("test-version", runtime, manager)
	}
	return startServerWithGRPCServer(t, server)
}

func startServerWithImageStore(t *testing.T, instances daemon.InstanceManager, paths hoststate.Paths) *grpc.ClientConn {
	return startServerWithGRPCServer(t, daemon.NewServerWithImageStore("test-version", nil, instances, paths))
}

func startServerWithGRPCServer(t *testing.T, server *grpc.Server) *grpc.ClientConn {
	t.Helper()
	socketPath := filepath.Join(t.TempDir(), "control.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
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
		GuestMAC:       "06:00:ac:1e:00:02",
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
