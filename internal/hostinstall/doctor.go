package hostinstall

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"sandboxctl/internal/client"
	"sandboxctl/internal/cmdutil"
	"sandboxctl/internal/config"
	"sandboxctl/internal/controlpb"
	microvmruntime "sandboxctl/internal/runtime"
)

const (
	controlSocket        = "/run/sandboxctl/control.sock"
	installedFirecracker = microvmruntime.DefaultFirecrackerBinaryPath
	installedJailer      = microvmruntime.DefaultJailerBinaryPath
)

type result struct {
	name string
	err  error
}

// Doctor reports host readiness without changing host state.
func Doctor(ctx context.Context, output io.Writer, paths config.Paths) error {
	results := []result{
		checkArchitecture(),
		checkVirtualizationCPU(),
		checkKVM(),
		checkDaemon(ctx),
		checkRuntimeVersions(ctx),
		checkCgroupV2(),
		checkProgram("nftables", "nft"),
		checkTUN(),
		checkAddressPool(ctx),
		checkSSHInclude(ctx, paths),
		checkInstalledImages(ctx),
	}
	failed := 0
	for _, item := range results {
		if item.err != nil {
			failed++
			fmt.Fprintf(output, "FAIL %s: %v\n", item.name, item.err)
		} else {
			fmt.Fprintf(output, "PASS %s\n", item.name)
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d host checks failed", failed)
	}
	return nil
}

func checkArchitecture() result {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return result{"x86-64 Linux host", fmt.Errorf("found %s/%s", runtime.GOOS, runtime.GOARCH)}
	}
	return result{name: "x86-64 Linux host"}
}

func checkVirtualizationCPU() result {
	file, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return result{"CPU virtualization support", err}
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "flags") || strings.HasPrefix(line, "Features") {
			for _, feature := range strings.Fields(line[strings.IndexByte(line, ':')+1:]) {
				if feature == "vmx" || feature == "svm" {
					return result{name: "CPU virtualization support"}
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return result{"CPU virtualization support", err}
	}
	return result{"CPU virtualization support", fmt.Errorf("no vmx or svm CPU flag was found")}
}

func checkKVM() result {
	file, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
	if err != nil {
		return result{"/dev/kvm access", err}
	}
	_ = file.Close()
	return result{name: "/dev/kvm access"}
}

func checkDaemon(ctx context.Context) result {
	connection, service, err := client.Dial(ctx, controlSocket)
	if err != nil {
		return result{"sandboxd socket access", err}
	}
	defer connection.Close()
	callContext, cancel := context.WithTimeout(ctx, client.DialTimeout)
	defer cancel()
	if _, err := service.Ping(callContext, &controlpb.PingRequest{}); err != nil {
		return result{"sandboxd socket access", err}
	}
	return result{name: "sandboxd socket access"}
}

func checkRuntimeVersions(ctx context.Context) result {
	if err := requireStaticallyLinked(installedFirecracker); err != nil {
		return result{"Firecracker and jailer versions", err}
	}
	firecracker, err := commandVersion(ctx, installedFirecracker, "Firecracker")
	if err != nil {
		return result{"Firecracker and jailer versions", err}
	}
	jailer, err := commandVersion(ctx, installedJailer, "Jailer")
	if err != nil {
		return result{"Firecracker and jailer versions", err}
	}
	if firecracker != jailer {
		return result{"Firecracker and jailer versions", fmt.Errorf("versions differ: %s and %s", firecracker, jailer)}
	}
	return result{name: "Firecracker and jailer versions"}
}

func checkCgroupV2() result {
	if _, err := os.Stat("/sys/fs/cgroup/cgroup.controllers"); err != nil {
		return result{"cgroup version 2", err}
	}
	return result{name: "cgroup version 2"}
}

func checkProgram(name, executable string) result {
	if _, err := exec.LookPath(executable); err != nil {
		return result{name, fmt.Errorf("%s was not found in PATH", executable)}
	}
	return result{name: name}
}

func checkTUN() result {
	file, err := os.OpenFile("/dev/net/tun", os.O_RDWR, 0)
	if err != nil {
		return result{"TUN device access", err}
	}
	_ = file.Close()
	return result{name: "TUN device access"}
}

func checkAddressPool(ctx context.Context) result {
	pool := "172.30.0.0/16"
	data, err := os.ReadFile(filepath.Join(configRoot, "host.json"))
	if err == nil {
		var configuration HostConfig
		if err := json.Unmarshal(data, &configuration); err != nil {
			return result{"address pool", fmt.Errorf("read host configuration: %w", err)}
		}
		if configuration.SchemaVersion != 1 {
			return result{"address pool", fmt.Errorf("unsupported host configuration schema %d", configuration.SchemaVersion)}
		}
		if configuration.AddressPool != "" {
			pool = configuration.AddressPool
		}
	} else if !os.IsNotExist(err) {
		return result{"address pool", fmt.Errorf("read host configuration: %w", err)}
	}
	parsedPool, err := cmdutil.ParseAddressPool(pool)
	if err != nil {
		return result{"address pool", err}
	}
	if err := checkRouteConflict(ctx, parsedPool); err != nil {
		return result{"address pool", err}
	}
	return result{name: "address pool"}
}

func checkSSHInclude(ctx context.Context, paths config.Paths) result {
	data, err := os.ReadFile(paths.SSHConfig)
	if err != nil {
		if os.IsNotExist(err) {
			return result{"SSH Include resolution", fmt.Errorf("run sandboxctl init first")}
		}
		return result{"SSH Include resolution", err}
	}
	aliases := make([]string, 0)
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) > 1 && strings.EqualFold(fields[0], "Host") {
			for _, alias := range fields[1:] {
				if strings.HasPrefix(alias, "sandboxctl-") && !strings.ContainsAny(alias, "*?!") {
					aliases = append(aliases, alias)
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return result{"SSH Include resolution", err}
	}
	if len(aliases) == 0 {
		return result{name: "SSH Include resolution (no sandbox aliases to check)"}
	}
	for _, alias := range aliases {
		commandContext, cancel := context.WithTimeout(ctx, 5*time.Second)
		command := exec.CommandContext(commandContext, "ssh", "-G", alias)
		output, commandErr := command.Output()
		cancel()
		if commandErr != nil {
			return result{"SSH Include resolution", fmt.Errorf("resolve %s: %w", alias, commandErr)}
		}
		hostname := ""
		for _, line := range strings.Split(string(output), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[0] == "hostname" {
				hostname = fields[1]
				break
			}
		}
		if hostname == "" || net.ParseIP(hostname) == nil {
			return result{"SSH Include resolution", fmt.Errorf("%s does not resolve to a sandbox address; include the managed file in ~/.ssh/config", alias)}
		}
	}
	return result{name: "SSH Include resolution"}
}

func checkInstalledImages(ctx context.Context) result {
	connection, service, err := client.Dial(ctx, controlSocket)
	if err != nil {
		return result{"installed image integrity", err}
	}
	defer connection.Close()
	callContext, cancel := context.WithTimeout(ctx, client.DialTimeout)
	defer cancel()
	response, err := service.ListImages(callContext, &controlpb.ListImagesRequest{})
	if err != nil {
		return result{"installed image integrity", err}
	}
	if len(response.GetImages()) == 0 {
		return result{name: "installed image integrity (no images installed)"}
	}
	return result{name: "installed image integrity"}
}
