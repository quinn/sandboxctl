package taskgit

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"sandboxctl/internal/config"
	"sandboxctl/internal/controlpb"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

type testService struct {
	controlpb.ControlServiceClient
	instances              map[string]*controlpb.Instance
	starts, stops, deletes int
	startErr, stopErr      error
}

func (s *testService) InspectInstance(_ context.Context, request *controlpb.InspectInstanceRequest, _ ...grpc.CallOption) (*controlpb.InspectInstanceResponse, error) {
	instance := s.instances[request.GetName()]
	if instance == nil {
		return nil, errors.New("sandbox is missing")
	}
	return &controlpb.InspectInstanceResponse{Instance: proto.Clone(instance).(*controlpb.Instance)}, nil
}
func (s *testService) ListInstances(context.Context, *controlpb.ListInstancesRequest, ...grpc.CallOption) (*controlpb.ListInstancesResponse, error) {
	response := &controlpb.ListInstancesResponse{}
	for _, instance := range s.instances {
		response.Instances = append(response.Instances, proto.Clone(instance).(*controlpb.Instance))
	}
	return response, nil
}
func (s *testService) StartInstance(ctx context.Context, request *controlpb.StartInstanceRequest, _ ...grpc.CallOption) (*controlpb.StartInstanceResponse, error) {
	if _, ok := ctx.Deadline(); !ok {
		return nil, errors.New("start has no deadline")
	}
	s.starts++
	instance := s.instances[request.GetName()]
	instance.RuntimeState = "running"
	if s.startErr != nil {
		return nil, s.startErr
	}
	return &controlpb.StartInstanceResponse{Instance: proto.Clone(instance).(*controlpb.Instance)}, nil
}
func (s *testService) StopInstance(ctx context.Context, request *controlpb.StopInstanceRequest, _ ...grpc.CallOption) (*controlpb.StopInstanceResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.stops++
	if s.stopErr != nil {
		return nil, s.stopErr
	}
	instance := s.instances[request.GetName()]
	instance.RuntimeState = "stopped"
	return &controlpb.StopInstanceResponse{Instance: proto.Clone(instance).(*controlpb.Instance)}, nil
}
func (s *testService) DeleteInstance(_ context.Context, request *controlpb.DeleteInstanceRequest, _ ...grpc.CallOption) (*controlpb.DeleteInstanceResponse, error) {
	if s.instances[request.GetName()].GetRuntimeState() != "stopped" {
		return nil, errors.New("running sandbox")
	}
	s.deletes++
	delete(s.instances, request.GetName())
	return &controlpb.DeleteInstanceResponse{Name: request.GetName()}, nil
}

type localRunner struct {
	SystemRunner
	guests                            map[string]string
	failFetch, failReport, failFinish bool
	cancelAfterPublish                context.CancelFunc
}

func (r *localRunner) Git(ctx context.Context, directory string, args ...string) ([]byte, error) {
	if len(args) > 0 && args[0] == "fetch" && r.failFetch {
		return nil, errors.New("test fetch failure")
	}
	if len(args) > 0 && (args[0] == "push" || args[0] == "fetch") {
		prefix := []string{}
		for name, root := range r.guests {
			bare := filepath.Join(root, "repos", name+".git")
			prefix = append(prefix, "-c", "url."+bare+".insteadOf="+remoteURL(name))
		}
		args = append(prefix, args...)
	}
	return r.SystemRunner.Git(ctx, directory, args...)
}
func (r *localRunner) Ready(context.Context, *controlpb.Instance) error { return nil }
func (r *localRunner) SSH(ctx context.Context, name, script string) ([]byte, error) {
	if script == readReport && r.failReport {
		return nil, errors.New("test report failure")
	}
	if strings.Contains(script, "author_name=") && r.failFinish {
		return nil, errors.New("test checkout failure")
	}
	root := r.guests[name]
	script = strings.ReplaceAll(script, "/home/dev", root)
	script = strings.ReplaceAll(script, "/var/lib/sandboxctl/bootstrap-complete", filepath.Join(root, "bootstrap-complete"))
	script = strings.ReplaceAll(script, "/REPORT.md", filepath.Join(root, "REPORT.md"))
	command := exec.CommandContext(ctx, "/bin/sh", "-c", script)
	data, err := capture(ctx, command)
	if strings.Contains(script, "status --porcelain") && r.cancelAfterPublish != nil {
		r.cancelAfterPublish()
	}
	return data, err
}

type fixture struct {
	t       *testing.T
	m       Manager
	service *testService
	runner  *localRunner
	host    string
	output  *bytes.Buffer
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	for _, variable := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR"} {
		t.Setenv(variable, filepath.Join(root, variable))
	}
	paths, err := config.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.PrivateKey(), []byte("test key"), 0o600); err != nil {
		t.Fatal(err)
	}
	host := filepath.Join(root, "host with spaces")
	if err := os.Mkdir(host, 0o700); err != nil {
		t.Fatal(err)
	}
	output := &bytes.Buffer{}
	service := &testService{instances: map[string]*controlpb.Instance{}}
	runner := &localRunner{guests: map[string]string{}}
	f := &fixture{t: t, service: service, runner: runner, host: host, output: output}
	f.m = New(paths, service, output)
	f.m.Runner = runner
	f.git(host, "init", "--quiet", "--template=", "--initial-branch=main")
	f.git(host, "config", "user.name", "Test operator")
	f.git(host, "config", "user.email", "operator@example.test")
	f.write(filepath.Join(host, "project.txt"), "starting code\n")
	f.git(host, "add", "project.txt")
	f.git(host, "commit", "--quiet", "-m", "Starting code")
	t.Chdir(host)
	return f
}

func (f *fixture) git(directory string, args ...string) string {
	f.t.Helper()
	data, err := f.runner.SystemRunner.Git(context.Background(), directory, args...)
	if err != nil {
		f.t.Fatalf("git %q: %v", args, err)
	}
	return strings.TrimSpace(string(data))
}
func (f *fixture) write(path, contents string) {
	f.t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		f.t.Fatal(err)
	}
}
func (f *fixture) attach(name string) *controlpb.Instance {
	f.t.Helper()
	root := filepath.Join(f.t.TempDir(), "guest")
	if err := os.Mkdir(root, 0o700); err != nil {
		f.t.Fatal(err)
	}
	f.write(filepath.Join(root, "bootstrap-complete"), "")
	f.runner.guests[name] = root
	id := strings.Repeat("a", 32)
	if name != "dev1" {
		id = strings.Repeat("b", 32)
	}
	instance := &controlpb.Instance{Name: name, Id: id, RuntimeState: "stopped", GuestIp: "172.30.0.2", SshUser: "dev", SshAlias: "sandbox-" + name}
	f.service.instances[name] = instance
	candidate, err := f.m.Discover(context.Background(), name)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.m.Attach(context.Background(), instance, candidate); err != nil {
		f.t.Fatal(err)
	}
	return instance
}
func (f *fixture) start(name string) {
	f.t.Helper()
	if _, err := f.m.Start(context.Background(), name); err != nil {
		f.t.Fatal(err)
	}
}
func (f *fixture) commitGuest(name string) string {
	f.t.Helper()
	work := filepath.Join(f.runner.guests[name], "work", name)
	f.write(filepath.Join(work, "project.txt"), "agent result\n")
	f.git(work, "add", "project.txt")
	f.git(work, "commit", "--quiet", "-m", "Agent result")
	return f.git(work, "rev-parse", "HEAD")
}

func TestTaskRoundTripAndDeletePreserveHostWork(t *testing.T) {
	f := newFixture(t)
	instance := f.attach("dev1")
	f.write(filepath.Join(f.host, "project.txt"), "uncommitted host work\n")
	f.start("dev1")
	work := filepath.Join(f.runner.guests["dev1"], "work", "dev1")
	data, _ := os.ReadFile(filepath.Join(work, "project.txt"))
	if string(data) != "starting code\n" {
		t.Fatal("sent uncommitted host changes")
	}
	want := f.commitGuest("dev1")
	f.write(filepath.Join(f.runner.guests["dev1"], "REPORT.md"), "Task complete.\n")
	instance.RuntimeState = "stopped"
	if err := f.m.Pull(context.Background(), "dev1"); err != nil {
		t.Fatal(err)
	}
	if instance.GetRuntimeState() != "stopped" || f.service.stops != 1 {
		t.Fatal("pull did not restore stopped state")
	}
	if f.git(f.host, "rev-parse", "refs/remotes/dev1/dev1") != want {
		t.Fatal("missing agent commits")
	}
	if f.git(f.host, "branch", "--show-current") != "main" {
		t.Fatal("pull changed the current branch")
	}
	data, _ = os.ReadFile(filepath.Join(f.host, "project.txt"))
	if string(data) != "uncommitted host work\n" {
		t.Fatal("pull changed host working files")
	}
	report := filepath.Join(f.m.Paths.StateDir, "reports", "dev1", "REPORT.md")
	data, err := os.ReadFile(report)
	if err != nil || string(data) != "Task complete.\n" {
		t.Fatalf("report = %q, %v", data, err)
	}
	for path, mode := range map[string]os.FileMode{f.m.stateDirectory(): 0o700, f.m.recordPath("dev1"): 0o600, filepath.Dir(report): 0o700, report: 0o600} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("permissions on %s", path)
		}
	}
	if err := f.m.Delete(context.Background(), []*controlpb.Instance{proto.Clone(instance).(*controlpb.Instance)}); err != nil {
		t.Fatal(err)
	}
	if f.service.deletes != 1 || f.git(f.host, "rev-parse", "refs/remotes/dev1/dev1") != want {
		t.Fatal("deletion lost fetched results")
	}
	if _, err := os.Stat(report); err != nil {
		t.Fatal("deletion removed the saved report")
	}
	if found, err := f.m.HasTask("dev1"); err != nil || found {
		t.Fatal("deletion retained the active association")
	}
	if f.git(f.host, "config", "--get", "remote.dev1.url") != remoteURL("dev1") {
		t.Fatal("deletion changed the host remote")
	}
}

func TestPullRunningSandboxAndRepeatedStartPreserveGuestChanges(t *testing.T) {
	f := newFixture(t)
	f.attach("dev1")
	f.start("dev1")
	want := f.commitGuest("dev1")
	work := filepath.Join(f.runner.guests["dev1"], "work", "dev1")
	f.write(filepath.Join(work, "pending.txt"), "unfinished\n")
	f.start("dev1")
	if f.git(work, "rev-parse", "HEAD") != want {
		t.Fatal("restart replaced guest commits")
	}
	if _, err := os.Stat(filepath.Join(work, "pending.txt")); err != nil {
		t.Fatal("restart removed pending work")
	}
	if err := os.Remove(filepath.Join(work, "pending.txt")); err != nil {
		t.Fatal(err)
	}
	starts := f.service.starts
	if err := f.m.Pull(context.Background(), "dev1"); err != nil {
		t.Fatal(err)
	}
	if f.service.starts != starts || f.service.stops != 0 || f.service.instances["dev1"].GetRuntimeState() != "running" {
		t.Fatal("pull changed a running sandbox's state")
	}
}

func TestDeleteKeepsAllSandboxesOnPullFailure(t *testing.T) {
	for _, failure := range []string{"dirty", "fetch", "report", "stop", "start"} {
		t.Run(failure, func(t *testing.T) {
			f := newFixture(t)
			one := f.attach("dev1")
			f.start("dev1")
			two := f.attach("dev2")
			f.start("dev2")
			f.commitGuest("dev1")
			f.commitGuest("dev2")
			one.RuntimeState = "stopped"
			two.RuntimeState = "stopped"
			switch failure {
			case "dirty":
				f.write(filepath.Join(f.runner.guests["dev2"], "work", "dev2", "project.txt"), "unfinished\n")
			case "fetch":
				f.runner.failFetch = true
			case "report":
				f.runner.failReport = true
			case "stop":
				f.service.stopErr = errors.New("test stop failure")
			case "start":
				f.service.startErr = errors.New("test uncertain start")
			}
			if err := f.m.Delete(context.Background(), []*controlpb.Instance{proto.Clone(one).(*controlpb.Instance), proto.Clone(two).(*controlpb.Instance)}); err == nil {
				t.Fatal("deleted after a pull failure")
			}
			if f.service.deletes != 0 || len(f.service.instances) != 2 {
				t.Fatal("deleted a sandbox before all pulls succeeded")
			}
			if failure != "stop" && one.GetRuntimeState() != "stopped" {
				t.Fatal("failed pull left a temporary VM running")
			}
		})
	}
}

func TestCancelledPullStillStopsTemporarySandbox(t *testing.T) {
	f := newFixture(t)
	one := f.attach("dev1")
	f.start("dev1")
	f.commitGuest("dev1")
	one.RuntimeState = "stopped"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.runner.cancelAfterPublish = cancel
	if err := f.m.Pull(ctx, "dev1"); err == nil {
		t.Fatal("pull ignored cancellation")
	}
	if one.GetRuntimeState() != "stopped" || f.service.stops != 1 {
		t.Fatal("cancellation prevented VM cleanup")
	}
}

func TestInitializeResumesInterruptedCheckout(t *testing.T) {
	f := newFixture(t)
	f.attach("dev1")
	f.runner.failFinish = true
	instance, err := f.m.Start(context.Background(), "dev1")
	if err == nil || instance == nil {
		t.Fatal("setup failure did not identify the running VM")
	}
	f.runner.failFinish = false
	f.start("dev1")
	if _, err := os.Stat(filepath.Join(f.runner.guests["dev1"], "work", "dev1", "project.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestStartAttachesRepositoryAndRecoversHostSetup(t *testing.T) {
	f := newFixture(t)
	instance := f.attach("dev1")
	if err := os.Remove(f.m.recordPath("dev1")); err != nil {
		t.Fatal(err)
	}
	f.git(f.host, "remote", "remove", "dev1")
	f.git(f.host, "branch", "-D", "dev1")
	f.start("dev1")
	if found, err := f.m.HasTask("dev1"); err != nil || !found {
		t.Fatal("start did not attach the host repository")
	}
	f.git(f.host, "config", "--unset", "remote.dev1.url")
	f.git(f.host, "config", "--unset", "remote.dev1.fetch")
	f.start("dev1")
	if f.git(f.host, "config", "--get", "remote.dev1.url") != remoteURL(instance.GetName()) {
		t.Fatal("start did not resume the host remote setup")
	}
}

func TestGuestUntrackedFilesAndOwnershipBlockPull(t *testing.T) {
	f := newFixture(t)
	instance := f.attach("dev1")
	f.start("dev1")
	work := filepath.Join(f.runner.guests["dev1"], "work", "dev1")
	f.write(filepath.Join(work, "new-code.txt"), "unfinished\n")
	if err := f.m.Pull(context.Background(), "dev1"); err == nil {
		t.Fatal("accepted untracked guest files")
	}
	if err := os.Remove(filepath.Join(work, "new-code.txt")); err != nil {
		t.Fatal(err)
	}
	f.git(work, "config", "sandboxctl.id", strings.Repeat("c", 32))
	instance.RuntimeState = "stopped"
	if err := f.m.Delete(context.Background(), []*controlpb.Instance{proto.Clone(instance).(*controlpb.Instance)}); err == nil || f.service.deletes != 0 {
		t.Fatal("deleted a guest with an invalid task ownership marker")
	}
}

func TestReportSymlinkBlocksDeletion(t *testing.T) {
	f := newFixture(t)
	instance := f.attach("dev1")
	f.start("dev1")
	root := f.runner.guests["dev1"]
	if err := os.Symlink(filepath.Join(root, "work", "dev1", "project.txt"), filepath.Join(root, "REPORT.md")); err != nil {
		t.Fatal(err)
	}
	instance.RuntimeState = "stopped"
	if err := f.m.Delete(context.Background(), []*controlpb.Instance{proto.Clone(instance).(*controlpb.Instance)}); err == nil || f.service.deletes != 0 {
		t.Fatal("deleted a guest with an invalid report path")
	}
}

func TestDiscoverAndAssociationGuards(t *testing.T) {
	f := newFixture(t)
	f.git(f.host, "branch", "occupied")
	if _, err := f.m.Discover(context.Background(), "occupied"); err == nil {
		t.Fatal("accepted an existing branch")
	}
	f.git(f.host, "remote", "add", "taken", "/tmp/other.git")
	if _, err := f.m.Discover(context.Background(), "taken"); err == nil {
		t.Fatal("accepted an existing remote")
	}
	instance := f.attach("dev1")
	f.start("dev1")
	f.git(f.host, "remote", "set-url", "dev1", "/tmp/other.git")
	if err := f.m.Pull(context.Background(), "dev1"); err == nil {
		t.Fatal("accepted a changed remote")
	}
	f.git(f.host, "remote", "set-url", "dev1", remoteURL("dev1"))
	instance.Id = strings.Repeat("c", 32)
	if err := f.m.Pull(context.Background(), "dev1"); err == nil {
		t.Fatal("used a task association for a recreated VM")
	}
	t.Chdir(t.TempDir())
	if record, err := f.m.Discover(context.Background(), "plain"); err != nil || record != nil {
		t.Fatalf("outside Git = %v, %v", record, err)
	}
}

func TestTaskLockAndRecordValidation(t *testing.T) {
	f := newFixture(t)
	f.attach("dev1")
	unlock, err := f.m.lock("dev1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.lock("dev1"); err == nil {
		t.Fatal("accepted concurrent task operations")
	}
	unlock()
	original, err := os.ReadFile(f.m.recordPath("dev1"))
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{`{}`, string(original) + `{}`, strings.Replace(string(original), `"schema_version": 1`, `"schema_version": 2`, 1), strings.Replace(string(original), `"name": "dev1"`, `"name": "other"`, 1)} {
		f.write(f.m.recordPath("dev1"), invalid)
		if _, err := f.m.load("dev1"); err == nil {
			t.Fatal("accepted an invalid task record")
		}
	}
}

func TestDeleteWithoutTaskAndRunningRefusal(t *testing.T) {
	f := newFixture(t)
	instance := &controlpb.Instance{Name: "plain", Id: strings.Repeat("a", 32), RuntimeState: "running"}
	f.service.instances["plain"] = instance
	if err := f.m.Delete(context.Background(), []*controlpb.Instance{proto.Clone(instance).(*controlpb.Instance)}); err == nil {
		t.Fatal("deleted a running sandbox")
	}
	instance.RuntimeState = "stopped"
	if err := f.m.Delete(context.Background(), []*controlpb.Instance{proto.Clone(instance).(*controlpb.Instance)}); err != nil {
		t.Fatal(err)
	}
	if f.service.starts != 0 || f.service.deletes != 1 {
		t.Fatal("started an unattached sandbox before deletion")
	}
}

func TestRecordRejectsNonRegularFiles(t *testing.T) {
	f := newFixture(t)
	if err := privateDirectory(f.m.stateDirectory()); err != nil {
		t.Fatal(err)
	}
	path := f.m.recordPath("bad")
	for _, kind := range []string{"directory", "fifo", "symlink", "oversized"} {
		var err error
		switch kind {
		case "directory":
			err = os.Mkdir(path, 0o700)
		case "fifo":
			err = syscall.Mkfifo(path, 0o600)
		case "symlink":
			err = os.Symlink(filepath.Join(f.host, "project.txt"), path)
		case "oversized":
			err = os.WriteFile(path, bytes.Repeat([]byte("x"), (64<<10)+1), 0o600)
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.m.load("bad"); err == nil {
			t.Fatalf("accepted %s record", kind)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDeleteBatchSavesEveryResult(t *testing.T) {
	f := newFixture(t)
	var selected []*controlpb.Instance
	results := map[string]string{}
	for _, name := range []string{"dev1", "dev2"} {
		instance := f.attach(name)
		f.start(name)
		results[name] = f.commitGuest(name)
		instance.RuntimeState = "stopped"
		selected = append(selected, proto.Clone(instance).(*controlpb.Instance))
	}
	if err := f.m.Delete(context.Background(), selected); err != nil {
		t.Fatal(err)
	}
	if f.service.deletes != 2 || len(f.service.instances) != 0 {
		t.Fatal("batch deletion did not complete")
	}
	for name, commit := range results {
		if f.git(f.host, "rev-parse", "refs/remotes/"+name+"/"+name) != commit {
			t.Fatal("batch deletion lost a task result")
		}
	}
}
