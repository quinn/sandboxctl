package config

import (
	"fmt"
	"os"
	"path/filepath"
)

type Paths struct {
	ConfigDir  string
	DataDir    string
	StateDir   string
	RuntimeDir string
	Instances  string
	Images     string
	Keys       string
	KnownHosts string
	SSHConfig  string
}

func Resolve() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("get the user home directory: %w", err)
	}

	configHome := envOr("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	dataHome := envOr("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	stateHome := envOr("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	runtimeHome := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeHome == "" {
		runtimeHome = filepath.Join(os.TempDir(), fmt.Sprintf("sandboxctl-%d", os.Getuid()))
	}

	configDir := filepath.Join(configHome, "sandboxctl")
	dataDir := filepath.Join(dataHome, "sandboxctl")
	stateDir := filepath.Join(stateHome, "sandboxctl")
	runtimeDir := filepath.Join(runtimeHome, "sandboxctl")

	return Paths{
		ConfigDir:  configDir,
		DataDir:    dataDir,
		StateDir:   stateDir,
		RuntimeDir: runtimeDir,
		Instances:  filepath.Join(dataDir, "instances"),
		Images:     filepath.Join(dataDir, "images"),
		Keys:       filepath.Join(dataDir, "keys"),
		KnownHosts: filepath.Join(stateDir, "known_hosts"),
		SSHConfig:  filepath.Join(configDir, "ssh_config"),
	}, nil
}

func (p Paths) Ensure() error {
	for _, path := range []string{
		p.ConfigDir,
		p.DataDir,
		p.StateDir,
		p.RuntimeDir,
		p.Instances,
		p.Images,
		p.Keys,
	} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return fmt.Errorf("create directory %s: %w", path, err)
		}
	}
	return nil
}

func (p Paths) PrivateKey() string {
	return filepath.Join(p.Keys, "client_ed25519")
}

func (p Paths) PublicKey() string {
	return p.PrivateKey() + ".pub"
}

func (p Paths) InstanceDir(name string) string {
	return filepath.Join(p.Instances, name)
}

func (p Paths) InstanceFile(name string) string {
	return filepath.Join(p.InstanceDir(name), "instance.json")
}

func (p Paths) PIDFile(name string) string {
	return filepath.Join(p.RuntimeDir, name+".pid")
}

func (p Paths) LogFile(name string) string {
	return filepath.Join(p.StateDir, name+".log")
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
