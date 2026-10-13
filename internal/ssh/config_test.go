package ssh

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"sandboxctl/internal/config"
	"sandboxctl/internal/controlpb"
	"sandboxctl/internal/model"
	"sandboxctl/internal/store"

	"google.golang.org/grpc"
)

func testManager(t *testing.T) Manager {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	for _, key := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR"} {
		t.Setenv(key, filepath.Join(root, key+" with spaces"))
	}
	paths, err := config.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	return Manager{Paths: paths}
}

func TestIncludedConfig(t *testing.T) {
	if _, err := os.Stat("/usr/bin/ssh"); err != nil {
		t.Skip("OpenSSH is not installed")
	}
	m := testManager(t)
	instance := model.Instance{SchemaVersion: model.SchemaVersion, Name: "dev1", GuestIP: "172.30.0.10", SSHUser: "dev", SSHPort: 2222}
	s := store.Store{Paths: m.Paths}
	if err := s.Save(instance); err != nil {
		t.Fatal(err)
	}
	if err := m.WriteConfig(); err != nil {
		t.Fatal(err)
	}
	block, err := m.IncludeBlock()
	if err != nil {
		t.Fatal(err)
	}
	// The include must work after an unrelated host section.
	mainConfig := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(mainConfig, []byte("Host unrelated\n    User other\n"+block), 0600); err != nil {
		t.Fatal(err)
	}
	resolve := func(host string) string {
		t.Helper()
		out, err := exec.Command("/usr/bin/ssh", "-G", "-F", mainConfig, host).Output()
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	for _, host := range []string{"sandboxctl-dev1", instance.GuestIP} {
		out := resolve(host)
		for _, want := range []string{"hostname 172.30.0.10\n", "user dev\n", "port 2222\n", "identitiesonly yes\n", "stricthostkeychecking ask\n", "identityfile " + m.Paths.PrivateKey() + "\n", "userknownhostsfile " + m.Paths.KnownHosts + "\n"} {
			if !strings.Contains(out, want) {
				t.Fatalf("%s: missing %q in %s", host, want, out)
			}
		}
	}
	out := resolve("unrelated")
	if !strings.Contains(out, "user other\n") || strings.Contains(out, m.Paths.PrivateKey()) || strings.Contains(out, m.Paths.KnownHosts) {
		t.Fatalf("managed settings affect an unrelated host: %s", out)
	}
	info, err := os.Stat(m.Paths.SSHConfig)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	// Regeneration must replace the previous address and restore private permissions.
	instance.GuestIP = "172.30.0.11"
	if err := s.Save(instance); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(m.Paths.SSHConfig, 0644); err != nil {
		t.Fatal(err)
	}
	if err := m.WriteConfig(); err != nil {
		t.Fatal(err)
	}
	out = resolve("sandboxctl-dev1")
	if !strings.Contains(out, "hostname 172.30.0.11\n") {
		t.Fatal(out)
	}
	info, err = os.Stat(m.Paths.SSHConfig)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("private mode was not restored: %v", err)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".ssh")); !os.IsNotExist(err) {
		t.Fatalf("the tool created .ssh: %v", err)
	}
}

func TestWriteConfigEntriesUsesDaemonValues(t *testing.T) {
	m := testManager(t)
	entries := []HostEntry{{InstanceID: strings.Repeat("a", 32), Name: "dev1", Address: "172.30.0.2", User: "dev"}}
	if err := m.WriteConfigEntries(entries); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(m.Paths.SSHConfig)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Host sandbox-dev1 172.30.0.2", "HostName 172.30.0.2", "HostKeyAlias sandbox-instance-" + entries[0].InstanceID, "User \"dev\"", "Port 22", "StrictHostKeyChecking ask"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("SSH fragment does not contain %q: %s", want, data)
		}
	}
	before := string(data)
	if err := m.WriteConfigEntries([]HostEntry{{InstanceID: entries[0].InstanceID, Name: "dev1", Address: "172.30.0.2\nHost *", User: "dev"}}); err == nil {
		t.Fatal("accepted a line break in a daemon address")
	}
	data, err = os.ReadFile(m.Paths.SSHConfig)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != before {
		t.Fatal("invalid daemon data changed the SSH fragment")
	}
}

func TestWriteConfigEntriesRejectsInvalidInstanceID(t *testing.T) {
	m := testManager(t)
	entry := HostEntry{InstanceID: strings.Repeat("a", 32), Name: "dev1", Address: "172.30.0.2", User: "dev"}
	if err := m.WriteConfigEntries([]HostEntry{entry}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(m.Paths.SSHConfig)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", "short", strings.Repeat("a", 31), strings.Repeat("a", 33), strings.Repeat("g", 32), strings.Repeat("A", 32), strings.Repeat("a", 32) + "\nHost *"} {
		t.Run(id, func(t *testing.T) {
			entry.InstanceID = id
			if err := m.WriteConfigEntries([]HostEntry{entry}); err == nil {
				t.Fatal("accepted an invalid instance identifier")
			}
			after, err := os.ReadFile(m.Paths.SSHConfig)
			if err != nil || string(after) != string(before) {
				t.Fatalf("invalid instance identifier changed the SSH fragment: %v", err)
			}
		})
	}
}

type configService struct {
	controlpb.ControlServiceClient
	response *controlpb.ListInstancesResponse
	err      error
}

func (s configService) ListInstances(context.Context, *controlpb.ListInstancesRequest, ...grpc.CallOption) (*controlpb.ListInstancesResponse, error) {
	return s.response, s.err
}

func TestRefreshConfigUsesInstanceIdentity(t *testing.T) {
	m := testManager(t)
	instance := &controlpb.Instance{Id: strings.Repeat("a", 32), Name: "dev1", GuestIp: "172.30.0.2", SshUser: "dev"}
	service := configService{response: &controlpb.ListInstancesResponse{Instances: []*controlpb.Instance{instance}}}
	if err := m.RefreshConfig(context.Background(), service); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(m.Paths.SSHConfig)
	if err != nil || !strings.Contains(string(data), "HostKeyAlias sandbox-instance-"+instance.Id) {
		t.Fatalf("missing instance identity: %s, %v", data, err)
	}
	before := string(data)
	service.err = errors.New("list failed")
	if err := m.RefreshConfig(context.Background(), service); err == nil {
		t.Fatal("ignored a daemon error")
	}
	data, err = os.ReadFile(m.Paths.SSHConfig)
	if err != nil || string(data) != before {
		t.Fatalf("failed refresh changed the SSH fragment: %v", err)
	}
}

func TestIncludedDaemonConfigSeparatesReusedAddresses(t *testing.T) {
	for _, program := range []string{"/usr/bin/ssh", "/usr/bin/ssh-keygen"} {
		if _, err := os.Stat(program); err != nil {
			t.Skip("OpenSSH is not installed")
		}
	}
	m := testManager(t)
	entry := HostEntry{InstanceID: strings.Repeat("a", 32), Name: "dev1", Address: "172.30.0.2", User: "dev"}
	keyPath := filepath.Join(t.TempDir(), "host_key")
	if output, err := exec.Command("/usr/bin/ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", keyPath).CombinedOutput(); err != nil {
		t.Fatalf("generate test host key: %v: %s", err, output)
	}
	publicKey, err := os.ReadFile(keyPath + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	// Keep a legacy address entry and a trusted key for the original instance.
	trustedAlias := "sandbox-instance-" + entry.InstanceID
	knownHosts := entry.Address + " " + string(publicKey) + trustedAlias + " " + string(publicKey)
	if err := os.WriteFile(m.Paths.KnownHosts, []byte(knownHosts), 0o600); err != nil {
		t.Fatal(err)
	}
	block, err := m.IncludeBlock()
	if err != nil {
		t.Fatal(err)
	}
	mainConfig := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(mainConfig, []byte(block), 0o600); err != nil {
		t.Fatal(err)
	}
	resolve := func(host string) string {
		t.Helper()
		output, err := exec.Command("/usr/bin/ssh", "-G", "-F", mainConfig, host).Output()
		if err != nil {
			t.Fatal(err)
		}
		return string(output)
	}
	for _, test := range []struct {
		name       string
		entry      HostEntry
		wantPinned bool
	}{
		{name: "original", entry: entry, wantPinned: true},
		{name: "restart", entry: entry, wantPinned: true},
		{name: "same instance at a new address", entry: HostEntry{InstanceID: entry.InstanceID, Name: entry.Name, Address: "172.30.0.6", User: entry.User}, wantPinned: true},
		{name: "recreated sandbox at the same address", entry: HostEntry{InstanceID: strings.Repeat("b", 32), Name: entry.Name, Address: entry.Address, User: entry.User}},
		{name: "another sandbox at the same address", entry: HostEntry{InstanceID: strings.Repeat("c", 32), Name: "dev2", Address: entry.Address, User: entry.User}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := m.WriteConfigEntries([]HostEntry{test.entry}); err != nil {
				t.Fatal(err)
			}
			alias := "sandbox-instance-" + test.entry.InstanceID
			for _, host := range []string{"sandbox-" + test.entry.Name, test.entry.Address} {
				output := resolve(host)
				for _, want := range []string{"hostkeyalias " + alias + "\n", "hostname " + test.entry.Address + "\n", "stricthostkeychecking ask\n", "checkhostip no\n", "userknownhostsfile " + m.Paths.KnownHosts + "\n"} {
					if !strings.Contains(output, want) {
						t.Fatalf("%s: missing %q in %s", host, want, output)
					}
				}
			}
			output, err := exec.Command("/usr/bin/ssh-keygen", "-F", alias, "-f", m.Paths.KnownHosts).CombinedOutput()
			if test.wantPinned {
				if err != nil || !strings.Contains(string(output), string(publicKey)) {
					t.Fatalf("existing instance lost its trusted key: %v: %s", err, output)
				}
			} else {
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 || len(output) != 0 {
					t.Fatalf("new instance inherited a trusted key: %v: %s", err, output)
				}
			}
			data, err := os.ReadFile(m.Paths.KnownHosts)
			if err != nil || string(data) != knownHosts {
				t.Fatalf("configuration refresh changed trusted keys: %v", err)
			}
		})
	}
	if err := m.WriteConfigEntries(nil); err != nil {
		t.Fatal(err)
	}
	if output := resolve("sandbox-dev1"); strings.Contains(output, "hostkeyalias sandbox-instance-") || strings.Contains(output, m.Paths.KnownHosts) {
		t.Fatalf("deleted sandbox retained managed SSH settings: %s", output)
	}
}

func TestConfigFailurePreservesFile(t *testing.T) {
	for _, invalid := range []model.Instance{
		{GuestIP: "172.30.0.10\nHost *", SSHUser: "dev", SSHPort: 22},
		{GuestIP: "172.30.0.10", SSHUser: "dev\nHost *", SSHPort: 22},
		{GuestIP: "172.30.0.10", SSHUser: "", SSHPort: 22},
		{GuestIP: "172.30.0.10", SSHUser: "dev", SSHPort: 0},
	} {
		m := testManager(t)
		if err := m.WriteConfig(); err != nil {
			t.Fatal(err)
		}
		before, err := os.ReadFile(m.Paths.SSHConfig)
		if err != nil {
			t.Fatal(err)
		}
		invalid.Name = "dev1"
		invalid.SchemaVersion = model.SchemaVersion
		if err := (store.Store{Paths: m.Paths}).Save(invalid); err != nil {
			t.Fatal(err)
		}
		if err := m.WriteConfig(); err == nil {
			t.Fatal("invalid record was accepted")
		}
		after, err := os.ReadFile(m.Paths.SSHConfig)
		if err != nil {
			t.Fatal(err)
		}
		if string(before) != string(after) {
			t.Fatal("failed generation changed the configuration")
		}
	}
}

func TestIncludeBlockRejectsLineBreak(t *testing.T) {
	m := Manager{Paths: config.Paths{SSHConfig: "/tmp/config\nHost *"}}
	if _, err := m.IncludeBlock(); err == nil {
		t.Fatal("accepted a line break")
	}
}
