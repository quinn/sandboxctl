package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// Paths contains the XDG paths used to store sandboxctl data.
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
	Sockets    string
}

// Resolve returns sandboxctl paths from the current user's XDG environment.
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
		Sockets:    filepath.Join(runtimeDir, "sockets"),
	}, nil
}

// Ensure creates the private directories used by p.
func (p Paths) Ensure() error {
	paths := []string{
		p.ConfigDir,
		p.DataDir,
		p.StateDir,
		p.RuntimeDir,
		p.Instances,
		p.Images,
		p.Keys,
	}
	if p.Sockets != "" {
		paths = append(paths, p.Sockets)
	}
	for _, path := range paths {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return fmt.Errorf("create directory %s: %w", path, err)
		}
	}
	return nil
}

// PrivateKey returns the path to the managed SSH private key.
func (p Paths) PrivateKey() string {
	return filepath.Join(p.Keys, "client_ed25519")
}

// PublicKey returns the path to the managed SSH public key.
func (p Paths) PublicKey() string {
	return p.PrivateKey() + ".pub"
}

// InstanceDir returns the directory for name's instance data.
func (p Paths) InstanceDir(name string) string {
	return filepath.Join(p.Instances, name)
}

// InstanceFile returns the path to name's instance record.
func (p Paths) InstanceFile(name string) string {
	return filepath.Join(p.InstanceDir(name), "instance.json")
}

// PIDFile returns the path to name's process record.
func (p Paths) PIDFile(name string) string {
	return filepath.Join(p.RuntimeDir, name+".pid.json")
}

// SocketFile returns the path to name's runtime socket.
func (p Paths) SocketFile(name string) string { return filepath.Join(p.Sockets, name+".sock") }

// LogFile returns the path to name's sandbox log.
func (p Paths) LogFile(name string) string {
	return filepath.Join(p.StateDir, name+".log")
}

// FirecrackerLogFile returns the path to name's Firecracker log.
func (p Paths) FirecrackerLogFile(name string) string {
	return filepath.Join(p.StateDir, name+"-firecracker.log")
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
