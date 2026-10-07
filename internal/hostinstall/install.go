package hostinstall

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"sandboxctl/internal/cmdutil"
	"sandboxctl/internal/hoststate"
)

const (
	configRoot        = "/etc/sandboxctl"
	stateRoot         = hoststate.DefaultRoot
	groupName         = "sandboxctl"
	firecrackerBinary = "/usr/bin/firecracker"
	jailerBinary      = "/usr/bin/jailer"
)

// HostConfig contains the privileged host settings used by sandboxd.
type HostConfig struct {
	SchemaVersion int             `json:"schema_version"`
	Operator      string          `json:"operator"`
	UIDRange      cmdutil.IDRange `json:"uid_range"`
	GIDRange      cmdutil.IDRange `json:"gid_range"`
	AddressPool   string          `json:"address_pool"`
}

// InstallOptions contains the values needed for one-time host setup.
type InstallOptions struct {
	Operator    user.User
	UIDRange    cmdutil.IDRange
	GIDRange    cmdutil.IDRange
	AddressPool netip.Prefix
}

// Install performs the one-time privileged host installation.
func Install(ctx context.Context, options InstallOptions) error {
	if os.Geteuid() != 0 {
		return errors.New("host install must run as root; use sudo sandboxctl host install")
	}

	if err := requireArchLinux(); err != nil {
		return err
	}

	if err := validateRanges(options.UIDRange, options.GIDRange, "/etc/passwd", "/etc/group", "/etc/subuid", "/etc/subgid"); err != nil {
		return err
	}

	if err := checkRouteConflict(ctx, options.AddressPool); err != nil {
		return err
	}

	paths := binaries{firecracker: firecrackerBinary, jailer: jailerBinary}
	fcVersion, err := commandVersion(ctx, paths.firecracker, "Firecracker")
	if err != nil {
		return err
	}

	jailerVersion, err := commandVersion(ctx, paths.jailer, "Jailer")
	if err != nil {
		return err
	}
	if fcVersion != jailerVersion {
		return fmt.Errorf("Firecracker and jailer versions do not match: %s and %s", fcVersion, jailerVersion)
	}

	if err := requireRegularExecutable("/usr/bin/sandboxd"); err != nil {
		return fmt.Errorf("sandboxd was not found; install the sandboxctl package first: %w", err)
	}

	for _, unit := range []string{"sandboxd.socket", "sandboxd.service"} {
		if _, err := os.Stat(filepath.Join("/usr/lib/systemd/system", unit)); err != nil {
			return fmt.Errorf("systemd unit %s was not found; install the sandboxctl package first", unit)
		}
	}

	for _, command := range []string{"/usr/bin/groupadd", "/usr/bin/usermod", "/usr/bin/nft", "/usr/bin/sysctl", "/usr/bin/systemctl", "/usr/bin/ip"} {
		if err := requireRegularExecutable(command); err != nil {
			return fmt.Errorf("required host command %q was not found", command)
		}
	}

	for _, path := range []string{paths.firecracker, paths.jailer} {
		if err := requireRegularExecutable(path); err != nil {
			return err
		}
	}

	config := HostConfig{
		SchemaVersion: 1,
		Operator:      options.Operator.Username,
		UIDRange:      options.UIDRange,
		GIDRange:      options.GIDRange,
		AddressPool:   options.AddressPool.String(),
	}
	if err := apply(ctx, config, options.Operator.Username); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "Installed sandboxctl host service.")
	fmt.Fprintln(os.Stdout, "Start a new login session before you run sandboxctl commands.")
	return nil
}

type binaries struct{ firecracker, jailer string }

func requireRegularExecutable(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect executable %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("executable %s must be a regular executable file", path)
	}
	return nil
}

func commandVersion(ctx context.Context, path, prefix string) (string, error) {
	commandContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	command := exec.CommandContext(commandContext, path, "--version")
	command.Env = hostEnvironment()
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("read %s version: %w", prefix, err)
	}
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == prefix {
			return strings.TrimPrefix(fields[1], "v"), nil
		}
	}
	return "", fmt.Errorf("could not read %s version", prefix)
}

func requireArchLinux() error {
	file, err := os.Open("/etc/os-release")
	if err != nil {
		return fmt.Errorf("read /etc/os-release: %w", err)
	}
	defer file.Close()
	values := make(map[string]string)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "=")
		if ok {
			values[key] = strings.Trim(value, `"`)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read /etc/os-release: %w", err)
	}
	if values["ID"] != "arch" && !strings.Contains(" "+values["ID_LIKE"]+" ", " arch ") {
		return errors.New("host install supports Arch Linux hosts only")
	}
	return nil
}

func apply(ctx context.Context, config HostConfig, operator string) error {
	if err := run(ctx, "/usr/bin/groupadd", "--system", "--force", groupName); err != nil {
		return fmt.Errorf("create sandboxctl group: %w", err)
	}
	if err := run(ctx, "/usr/bin/usermod", "--append", "--groups", groupName, "--", operator); err != nil {
		return fmt.Errorf("add operator to sandboxctl group: %w", err)
	}
	state := hoststate.DefaultPaths()
	for _, directory := range []struct {
		path string
		mode os.FileMode
		gid  int
	}{
		{configRoot, 0o755, 0},
		{state.Root, 0o700, 0}, {state.Images, 0o700, 0},
		{state.Instances, 0o700, 0}, {state.Allocations, 0o700, 0},
		{state.Jails, 0o700, 0},
	} {
		if err := ensureRootDirectory(directory.path, directory.mode, directory.gid); err != nil {
			return err
		}
	}
	encodedConfig, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(configRoot, "host.json"), append(encodedConfig, '\n'), 0o644); err != nil {
		return fmt.Errorf("write host configuration: %w", err)
	}
	if err := atomicWrite("/etc/sysctl.d/99-sandboxctl.conf", []byte("net.ipv4.ip_forward = 1\n"), 0o644); err != nil {
		return fmt.Errorf("write IPv4 forwarding configuration: %w", err)
	}
	if err := ensureNFTTable(ctx); err != nil {
		return err
	}
	if err := run(ctx, "/usr/bin/sysctl", "-w", "net.ipv4.ip_forward=1"); err != nil {
		return fmt.Errorf("enable IPv4 forwarding: %w", err)
	}
	if err := run(ctx, "/usr/bin/systemctl", "daemon-reload"); err != nil {
		return fmt.Errorf("reload systemd unit files: %w", err)
	}
	if err := run(ctx, "/usr/bin/systemctl", "enable", "--now", "sandboxd.socket"); err != nil {
		return fmt.Errorf("enable sandboxd socket: %w", err)
	}
	return nil
}

func ensureRootDirectory(path string, mode os.FileMode, gid int) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(path, mode); err != nil {
			return fmt.Errorf("create directory %s: %w", path, err)
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return fmt.Errorf("inspect directory %s: %w", path, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Sys().(*syscall.Stat_t).Uid != 0 {
		return fmt.Errorf("directory %s must be a root-owned directory", path)
	}
	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("set directory mode %s: %w", path, err)
	}
	if err := os.Chown(path, 0, gid); err != nil {
		return fmt.Errorf("set directory ownership %s: %w", path, err)
	}
	return nil
}

func atomicWrite(destination string, data []byte, mode os.FileMode) error {
	return copyFileAtomic(destination, bytes.NewReader(data), mode)
}

func copyFileAtomic(destination string, source io.Reader, mode os.FileMode) error {
	directory := filepath.Dir(destination)
	temporary, err := os.CreateTemp(directory, ".sandboxctl-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := io.Copy(temporary, source); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return err
	}
	return nil
}

func ensureNFTTable(ctx context.Context) error {
	output, err := hostCommand(ctx, "/usr/bin/nft", "list", "tables").CombinedOutput()
	if err != nil {
		return fmt.Errorf("list nftables tables: %w: %s", err, strings.TrimSpace(string(output)))
	}
	for _, line := range strings.Split(string(output), "\n") {
		if strings.TrimSpace(line) == "table inet sandboxctl" {
			return nil
		}
	}
	if err := run(ctx, "/usr/bin/nft", "add", "table", "inet", "sandboxctl"); err != nil {
		return fmt.Errorf("create sandboxctl nftables table: %w", err)
	}
	return nil
}

func run(ctx context.Context, name string, arguments ...string) error {
	command := hostCommand(ctx, name, arguments...)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func checkRouteConflict(ctx context.Context, pool netip.Prefix) error {
	command := hostCommand(ctx, "/usr/bin/ip", "-4", "route", "show", "table", "all")
	output, err := command.Output()
	if err != nil {
		return fmt.Errorf("read host IPv4 routes: %w", err)
	}
	return findRouteConflict(pool, string(output))
}

func hostCommand(ctx context.Context, name string, arguments ...string) *exec.Cmd {
	command := exec.CommandContext(ctx, name, arguments...)
	command.Env = hostEnvironment()
	return command
}

func hostEnvironment() []string {
	return []string{"PATH=/usr/bin:/usr/sbin:/bin:/sbin", "LANG=C", "LC_ALL=C"}
}

func findRouteConflict(pool netip.Prefix, output string) error {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] == "default" {
			continue
		}
		route, err := parseIPv4Route(fields[0])
		if err != nil || route.IP.To4() == nil {
			continue
		}
		routeAddress := netip.AddrFrom4([4]byte(route.IP.To4()))
		if pool.Contains(routeAddress) || route.Contains(net.IP(pool.Addr().AsSlice())) {
			return fmt.Errorf("address pool %s conflicts with host route %s", pool, fields[0])
		}
	}
	return nil
}

func parseIPv4Route(value string) (*net.IPNet, error) {
	_, network, err := net.ParseCIDR(value)
	if err == nil {
		return network, nil
	}
	address := net.ParseIP(value).To4()
	if address == nil {
		return nil, err
	}
	return &net.IPNet{IP: address, Mask: net.CIDRMask(32, 32)}, nil
}

func validateRanges(uidRange, gidRange cmdutil.IDRange, passwdPath, groupPath, subuidPath, subgidPath string) error {
	if err := checkNumericFileRange(passwdPath, 2, uidRange); err != nil {
		return fmt.Errorf("UID range conflicts with a local account: %w", err)
	}
	if err := checkNumericFileRange(groupPath, 2, gidRange); err != nil {
		return fmt.Errorf("GID range conflicts with a local group: %w", err)
	}
	if err := checkSubIDRange(subuidPath, uidRange); err != nil {
		return fmt.Errorf("UID range conflicts with subordinate IDs: %w", err)
	}
	if err := checkSubIDRange(subgidPath, gidRange); err != nil {
		return fmt.Errorf("GID range conflicts with subordinate IDs: %w", err)
	}
	return nil
}

func checkNumericFileRange(path string, field int, selected cmdutil.IDRange) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, ":")
		if len(fields) <= field {
			return fmt.Errorf("malformed entry in %s", path)
		}
		id, err := strconv.ParseUint(fields[field], 10, 32)
		if err != nil {
			return fmt.Errorf("invalid identifier in %s", path)
		}
		if within(selected, uint32(id)) {
			return fmt.Errorf("identifier %d is already assigned", id)
		}
	}
	return scanner.Err()
}

func checkSubIDRange(path string, selected cmdutil.IDRange) error {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, ":")
		if len(fields) != 3 {
			return fmt.Errorf("malformed entry in %s", path)
		}
		start, startErr := strconv.ParseUint(fields[1], 10, 32)
		count, countErr := strconv.ParseUint(fields[2], 10, 32)
		if startErr != nil || countErr != nil || count == 0 || start+count-1 > 1<<32-1 {
			return fmt.Errorf("invalid range in %s", path)
		}
		if overlaps(selected, cmdutil.IDRange{Start: uint32(start), Count: uint32(count)}) {
			return fmt.Errorf("range %s:%s overlaps %s:%s", fields[1], fields[2], strconv.FormatUint(uint64(selected.Start), 10), strconv.FormatUint(uint64(selected.Count), 10))
		}
	}
	return scanner.Err()
}

func within(selected cmdutil.IDRange, value uint32) bool {
	return value >= selected.Start && uint64(value) < uint64(selected.Start)+uint64(selected.Count)
}

func overlaps(left, right cmdutil.IDRange) bool {
	return uint64(left.Start) < uint64(right.Start)+uint64(right.Count) && uint64(right.Start) < uint64(left.Start)+uint64(left.Count)
}
