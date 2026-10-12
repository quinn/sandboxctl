package taskgit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"sandboxctl/internal/cmdutil"
	"sandboxctl/internal/config"
	"sandboxctl/internal/controlpb"
	"sandboxctl/internal/controlsettings"
	sshconfig "sandboxctl/internal/ssh"
)

// TransferTimeout bounds Git transfers and report retrieval.
const TransferTimeout = 10 * time.Minute

// Manager keeps repository state and coordinates task transfers.
type Manager struct {
	Paths   config.Paths
	Service controlpb.ControlServiceClient
	Runner  Runner
	Output  io.Writer
}

// New creates an operator task manager for the daemon connection.
func New(paths config.Paths, service controlpb.ControlServiceClient, output io.Writer) Manager {
	return Manager{Paths: paths, Service: service, Runner: SystemRunner{SSHManager: sshconfig.Manager{Paths: paths}}, Output: output}
}

func (m Manager) print(format string, values ...any) {
	if m.Output != nil {
		fmt.Fprintf(m.Output, format, values...)
	}
}

func remoteURL(name string) string { return "sandbox-" + name + ":/home/dev/repos/" + name + ".git" }

// Discover checks the current directory for a repository and name conflicts.
// A directory outside a repository does not attach a task.
func (m Manager) Discover(ctx context.Context, name string) (*Record, error) {
	if err := cmdutil.ValidateSandboxName(name); err != nil {
		return nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	data, err := m.Runner.Git(ctx, cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// Git's exit status alone also covers corrupt repositories and missing Git.
		// Ignore only Git's specific diagnostic for a directory outside a repository.
		if strings.Contains(err.Error(), "not a git repository") {
			return nil, nil
		}
		return nil, fmt.Errorf("find the host Git repository: %w", err)
	}
	root, err := filepath.EvalSymlinks(strings.TrimSpace(string(data)))
	if err != nil {
		return nil, err
	}
	if strings.ContainsAny(root, "\x00\r\n") {
		return nil, errors.New("the host repository path contains a line break")
	}
	head, err := m.Runner.Git(ctx, root, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return nil, fmt.Errorf("the host repository requires a starting commit: %w", err)
	}
	commit := strings.TrimSpace(string(head))
	if !objectID.MatchString(commit) {
		return nil, errors.New("Git returned an invalid starting commit")
	}
	branches, err := m.Runner.Git(ctx, root, "for-each-ref", "--format=%(refname)", "refs/heads/"+name)
	if err != nil {
		return nil, err
	}
	if strings.Contains(string(branches), "refs/heads/"+name+"\n") {
		return nil, fmt.Errorf("Git branch %s already exists", name)
	}
	remotes, err := m.Runner.Git(ctx, root, "remote")
	if err != nil {
		return nil, err
	}
	for _, remote := range strings.Fields(string(remotes)) {
		if remote == name {
			return nil, fmt.Errorf("Git remote %s already exists", name)
		}
	}
	record := &Record{SchemaVersion: 1, Name: name, Repository: root, StartCommit: commit, AuthorName: "Sandbox agent", AuthorEmail: "dev@sandbox.local"}
	if value, err := m.Runner.Git(ctx, root, "config", "--get", "user.name"); err == nil && strings.TrimSpace(string(value)) != "" {
		record.AuthorName = strings.TrimSpace(string(value))
	}
	if value, err := m.Runner.Git(ctx, root, "config", "--get", "user.email"); err == nil && strings.TrimSpace(string(value)) != "" {
		record.AuthorEmail = strings.TrimSpace(string(value))
	}
	return record, nil
}

// Attach saves a discovered repository after the daemon creates the sandbox.
func (m Manager) Attach(ctx context.Context, instance *controlpb.Instance, candidate *Record) error {
	if candidate == nil {
		return nil
	}
	unlock, err := m.lock(instance.GetName())
	if err != nil {
		return err
	}
	defer unlock()
	return m.attach(ctx, instance, candidate)
}

func (m Manager) attach(ctx context.Context, instance *controlpb.Instance, candidate *Record) error {
	if !instanceID.MatchString(instance.GetId()) || candidate.Name != instance.GetName() {
		return errors.New("the sandbox identity does not match the Git task")
	}
	existing, err := m.load(candidate.Name)
	if err != nil {
		return err
	}
	if existing != nil {
		if existing.InstanceID != instance.GetId() || existing.Repository != candidate.Repository {
			return errors.New("the sandbox already has a different Git task association")
		}
		candidate = existing
	} else {
		candidate.InstanceID = instance.GetId()
		// Save ownership before branch and remote creation. Start can finish a partial setup.
		if err := m.save(candidate); err != nil {
			return err
		}
	}
	if err := m.ensureHost(ctx, candidate); err != nil {
		return err
	}
	m.print("Git task branch and remote: %s\n", candidate.Name)
	return nil
}

func (m Manager) ensureHost(ctx context.Context, record *Record) error {
	root, err := m.Runner.Git(ctx, record.Repository, "rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("open the saved host repository: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(strings.TrimSpace(string(root)))
	if err != nil || canonical != record.Repository {
		return errors.New("the saved host repository path no longer identifies the task repository")
	}
	if _, err := m.Runner.Git(ctx, record.Repository, "cat-file", "-e", record.StartCommit+"^{commit}"); err != nil {
		return fmt.Errorf("the starting commit is unavailable: %w", err)
	}
	branch := "refs/heads/" + record.Name
	refs, err := m.Runner.Git(ctx, record.Repository, "for-each-ref", "--format=%(refname)", branch)
	if err != nil {
		return err
	}
	if !strings.Contains(string(refs), branch+"\n") {
		if _, err := m.Runner.Git(ctx, record.Repository, "update-ref", branch, record.StartCommit, strings.Repeat("0", len(record.StartCommit))); err != nil {
			return err
		}
	}
	remotes, err := m.Runner.Git(ctx, record.Repository, "remote")
	if err != nil {
		return err
	}
	exists := false
	for _, name := range strings.Fields(string(remotes)) {
		exists = exists || name == record.Name
	}
	id, idErr := m.Runner.Git(ctx, record.Repository, "config", "--get", "remote."+record.Name+".sandboxctl-id")
	if idErr != nil && !exists {
		// Save the marker first so start can resume an interrupted setup.
		if _, err := m.Runner.Git(ctx, record.Repository, "config", "--local", "remote."+record.Name+".sandboxctl-id", record.InstanceID); err != nil {
			return err
		}
		id = []byte(record.InstanceID)
		idErr = nil
	}
	if idErr != nil || strings.TrimSpace(string(id)) != record.InstanceID {
		return errors.New("the Git remote belongs to a different task")
	}
	urls, err := m.Runner.Git(ctx, record.Repository, "config", "--get-all", "remote."+record.Name+".url")
	if err != nil && ctx.Err() == nil {
		if _, err := m.Runner.Git(ctx, record.Repository, "config", "--local", "remote."+record.Name+".url", remoteURL(record.Name)); err != nil {
			return err
		}
		urls = []byte(remoteURL(record.Name))
		err = nil
	}
	if err != nil || strings.TrimSpace(string(urls)) != remoteURL(record.Name) {
		return errors.New("the Git remote no longer points to this sandbox")
	}
	fetchRef := "+refs/heads/" + record.Name + ":refs/remotes/" + record.Name + "/" + record.Name
	fetch, err := m.Runner.Git(ctx, record.Repository, "config", "--get-all", "remote."+record.Name+".fetch")
	if err != nil && ctx.Err() == nil {
		if _, err := m.Runner.Git(ctx, record.Repository, "config", "--local", "remote."+record.Name+".fetch", fetchRef); err != nil {
			return err
		}
		fetch = []byte(fetchRef)
		err = nil
	}
	if err != nil || strings.TrimSpace(string(fetch)) != fetchRef {
		return errors.New("the Git remote has a different fetch mapping")
	}
	pushURLs, err := m.Runner.Git(ctx, record.Repository, "config", "--get-all", "remote."+record.Name+".pushurl")
	if err == nil && strings.TrimSpace(string(pushURLs)) != "" {
		return errors.New("the Git remote has a different push destination")
	}
	return nil
}

func (m Manager) validateInstance(record *Record, instance *controlpb.Instance) error {
	if instance.GetName() != record.Name || instance.GetId() != record.InstanceID {
		return errors.New("the saved Git task belongs to a different sandbox identity")
	}
	if instance.GetSshUser() != "dev" {
		return errors.New("Git task transfers require the dev SSH account")
	}
	return nil
}

func (m Manager) prepareSSH(ctx context.Context, instance *controlpb.Instance) error {
	ssh := sshconfig.Manager{Paths: m.Paths}
	if err := ssh.PrepareKey(); err != nil {
		return err
	}
	listCtx, cancel := context.WithTimeout(ctx, controlsettings.MaxOperationDuration)
	err := ssh.RefreshConfig(listCtx, m.Service)
	cancel()
	if err != nil {
		return err
	}
	readyCtx, cancel := context.WithTimeout(ctx, controlsettings.MaxStartDuration)
	defer cancel()
	return m.Runner.Ready(readyCtx, instance)
}

// Initialize attaches a repository when needed and prepares the running guest.
func (m Manager) Initialize(ctx context.Context, instance *controlpb.Instance) error {
	unlock, err := m.lock(instance.GetName())
	if err != nil {
		return err
	}
	defer unlock()
	return m.initialize(ctx, instance)
}

func (m Manager) initialize(ctx context.Context, instance *controlpb.Instance) error {
	record, err := m.load(instance.GetName())
	if err != nil {
		return err
	}
	if record == nil {
		record, err = m.Discover(ctx, instance.GetName())
		if err != nil || record == nil {
			return err
		}
		if err := m.attach(ctx, instance, record); err != nil {
			return err
		}
	}
	if err := m.validateInstance(record, instance); err != nil {
		return err
	}
	if err := m.ensureHost(ctx, record); err != nil {
		return err
	}
	if err := m.prepareSSH(ctx, instance); err != nil {
		return err
	}
	if err := m.initializeGuest(ctx, record); err != nil {
		return err
	}
	m.print("Guest working directory: /home/dev/work/%s\n", record.Name)
	return nil
}

// Start holds the task lock while it starts the VM and prepares its repository.
// A non-nil instance with an error means that the VM started but setup failed.
func (m Manager) Start(ctx context.Context, name string) (*controlpb.Instance, error) {
	unlock, err := m.lock(name)
	if err != nil {
		return nil, err
	}
	defer unlock()
	instance, err := m.inspect(ctx, name)
	if err != nil {
		return nil, err
	}
	record, err := m.load(name)
	if err != nil {
		return nil, err
	}
	if record == nil {
		candidate, err := m.Discover(ctx, name)
		if err != nil {
			return nil, err
		}
		if candidate != nil {
			if err := m.attach(ctx, instance, candidate); err != nil {
				return nil, err
			}
		}
	} else if err := m.validateInstance(record, instance); err != nil {
		return nil, err
	}
	startCtx, cancel := context.WithTimeout(ctx, controlsettings.MaxStartDuration)
	response, err := m.Service.StartInstance(startCtx, &controlpb.StartInstanceRequest{Name: name})
	cancel()
	if err != nil {
		return nil, err
	}
	instance = response.GetInstance()
	if instance == nil || instance.GetName() != name {
		return nil, errors.New("sandboxd returned an invalid start response")
	}
	if err := m.initialize(ctx, instance); err != nil {
		return instance, err
	}
	return instance, nil
}

func (m Manager) initializeGuest(ctx context.Context, record *Record) error {
	status, err := m.Runner.SSH(ctx, record.Name, prepareGuest(record))
	if err != nil {
		return fmt.Errorf("prepare the guest Git repository: %w", err)
	}
	switch strings.TrimSpace(string(status)) {
	case "seed":
		if _, err := m.Runner.Git(ctx, record.Repository, "push", "--no-verify", record.Name, record.StartCommit+":refs/heads/"+record.Name); err != nil {
			return fmt.Errorf("send the starting commit: %w", err)
		}
	case "existing", "clone":
	default:
		return errors.New("the guest returned an invalid Git setup status")
	}
	if _, err := m.Runner.SSH(ctx, record.Name, finishGuest(record)); err != nil {
		return fmt.Errorf("prepare the guest working directory: %w", err)
	}
	record.Initialized = true
	return m.save(record)
}

// HasTask reports whether this operator has a saved association for name.
func (m Manager) HasTask(name string) (bool, error) {
	record, err := m.load(name)
	return record != nil, err
}

// Pull saves committed task results and the guest report.
func (m Manager) Pull(ctx context.Context, name string) error {
	unlock, err := m.lock(name)
	if err != nil {
		return err
	}
	defer unlock()
	record, err := m.load(name)
	if err != nil {
		return err
	}
	if record == nil {
		return fmt.Errorf("sandbox %s has no attached Git repository", name)
	}
	return m.pull(ctx, record)
}

func (m Manager) inspect(ctx context.Context, name string) (*controlpb.Instance, error) {
	callCtx, cancel := context.WithTimeout(ctx, controlsettings.MaxOperationDuration)
	defer cancel()
	response, err := m.Service.InspectInstance(callCtx, &controlpb.InspectInstanceRequest{Name: name})
	if err != nil {
		return nil, err
	}
	if response.GetInstance() == nil || response.GetInstance().GetName() != name {
		return nil, errors.New("sandboxd returned an invalid instance")
	}
	return response.GetInstance(), nil
}

func (m Manager) pull(ctx context.Context, record *Record) (pullErr error) {
	instance, err := m.inspect(ctx, record.Name)
	if err != nil {
		return err
	}
	if err := m.validateInstance(record, instance); err != nil {
		return err
	}
	if err := m.ensureHost(ctx, record); err != nil {
		return err
	}
	switch instance.GetRuntimeState() {
	case "stopped":
		// Stop also follows an uncertain start result. Cleanup has its own deadline.
		defer func() {
			stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), controlsettings.MaxStopDuration)
			defer cancel()
			_, err := m.Service.StopInstance(stopCtx, &controlpb.StopInstanceRequest{Name: record.Name})
			if err != nil {
				pullErr = errors.Join(pullErr, fmt.Errorf("stop the temporarily started sandbox: %w", err))
			}
		}()
		startCtx, cancel := context.WithTimeout(ctx, controlsettings.MaxStartDuration)
		response, err := m.Service.StartInstance(startCtx, &controlpb.StartInstanceRequest{Name: record.Name})
		cancel()
		if err != nil {
			return fmt.Errorf("start the sandbox for pull: %w", err)
		}
		instance = response.GetInstance()
		if err := m.validateInstance(record, instance); err != nil {
			return err
		}
	case "running":
	default:
		return fmt.Errorf("cannot pull sandbox %s in state %s", record.Name, instance.GetRuntimeState())
	}
	if err := m.prepareSSH(ctx, instance); err != nil {
		return err
	}
	if !record.Initialized {
		if err := m.initializeGuest(ctx, record); err != nil {
			return err
		}
	}
	if _, err := m.Runner.SSH(ctx, record.Name, publishGuest(record)); err != nil {
		return fmt.Errorf("publish the guest task; commit guest changes before pulling: %w", err)
	}
	ref := "refs/remotes/" + record.Name + "/" + record.Name
	if _, err := m.Runner.Git(ctx, record.Repository, "fetch", "--no-tags", "--no-recurse-submodules", record.Name, "refs/heads/"+record.Name+":"+ref); err != nil {
		return fmt.Errorf("fetch the guest task: %w", err)
	}
	result, err := m.Runner.Git(ctx, record.Repository, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return err
	}
	if _, err := m.Runner.Git(ctx, record.Repository, "merge-base", "--is-ancestor", record.StartCommit, strings.TrimSpace(string(result))); err != nil {
		return fmt.Errorf("the guest task no longer contains its starting commit: %w", err)
	}
	report, err := m.Runner.SSH(ctx, record.Name, readReport)
	if err != nil {
		return fmt.Errorf("retrieve the guest report: %w", err)
	}
	if len(report) == 0 || (report[0] != '0' && report[0] != '1') || (report[0] == '0' && len(report) != 1) {
		return errors.New("the guest returned an invalid report status")
	}
	if report[0] == '1' {
		if err := privateDirectory(filepath.Join(m.Paths.StateDir, "reports")); err != nil {
			return err
		}
		directory := filepath.Join(m.Paths.StateDir, "reports", record.Name)
		path := filepath.Join(directory, "REPORT.md")
		if err := atomicFile(directory, path, report[1:]); err != nil {
			return fmt.Errorf("save the guest report: %w", err)
		}
		m.print("Saved guest report: %s\n", path)
	}
	summary, err := m.Runner.Git(ctx, record.Repository, "diff", "--stat", "--no-ext-diff", "--no-textconv", record.StartCommit, strings.TrimSpace(string(result)))
	if err != nil {
		return err
	}
	m.print("Pulled sandbox %s.\nStarting commit: %s\nResult commit: %s\n%s", record.Name, record.StartCommit, strings.TrimSpace(string(result)), summary)
	return nil
}

// Delete pulls all selected tasks before it deletes any stopped sandbox.
func (m Manager) Delete(ctx context.Context, instances []*controlpb.Instance) error {
	unlocks := make([]func(), 0, len(instances))
	defer func() {
		for i := len(unlocks) - 1; i >= 0; i-- {
			unlocks[i]()
		}
	}()
	for _, selected := range instances {
		unlock, err := m.lock(selected.GetName())
		if err != nil {
			return err
		}
		unlocks = append(unlocks, unlock)
		current, err := m.inspect(ctx, selected.GetName())
		if err != nil {
			return err
		}
		if current.GetId() != selected.GetId() || current.GetRuntimeState() != "stopped" {
			return fmt.Errorf("sandbox %s must remain stopped with the same identity before deletion", selected.GetName())
		}
	}
	for _, instance := range instances {
		record, err := m.load(instance.GetName())
		if err != nil {
			return err
		}
		if record != nil {
			pullCtx, cancel := context.WithTimeout(ctx, TransferTimeout)
			err := m.pull(pullCtx, record)
			cancel()
			if err != nil {
				return fmt.Errorf("keep sandbox %s because pull failed: %w", instance.GetName(), err)
			}
		}
	}
	for _, instance := range instances {
		current, err := m.inspect(ctx, instance.GetName())
		if err != nil {
			return err
		}
		if current.GetId() != instance.GetId() || current.GetRuntimeState() != "stopped" {
			return fmt.Errorf("sandbox %s changed before deletion", instance.GetName())
		}
		callCtx, cancel := context.WithTimeout(ctx, controlsettings.MaxDeleteDuration)
		_, err = m.Service.DeleteInstance(callCtx, &controlpb.DeleteInstanceRequest{Name: instance.GetName()})
		cancel()
		if err != nil {
			return fmt.Errorf("delete sandbox %s: %w", instance.GetName(), err)
		}
		if err := os.Remove(m.recordPath(instance.GetName())); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove the deleted sandbox task association: %w", err)
		}
		m.print("Deleted sandbox %s.\n", instance.GetName())
	}
	return nil
}
