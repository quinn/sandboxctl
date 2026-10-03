# sandboxctl implementation specification

## 1. Purpose

`sandboxctl` manages Linux development sandboxes on one Linux host. Each sandbox is a Firecracker microVM.

The user can manually include the tool-owned SSH configuration in the main SSH configuration.
The tool does not edit `~/.ssh`.

## 2. Goals

The tool must:

- Create named development sandboxes.
- Start a sandbox only when the user requests it.
- Stop a sandbox without a system service.
- Keep each writable root drive after a stop.
- Give each running sandbox a stable network identity.
- Manage the SSH client key.
- Manage known host keys.
- Keep all SSH files outside `~/.ssh`.
- Show clear current state.
- Protect data during failed operations.

## 3. Non-goals for version 1

Version 1 does not support:

- Non-Linux guests.
- CPU emulation.
- Live migration.
- High availability.
- Automatic host startup.
- Kubernetes.
- A web interface.
- Remote Firecracker hosts.
- GPU access.
- VM snapshots.

## 4. User commands

### 4.1 Initialize

```sh
sandboxctl init
```

This command must:

1. Create the XDG directories.
2. Create the client key if it does not exist.
3. Create the SSH configuration.
4. Create the known-hosts file.
5. Check required host programs.
6. Check access to `/dev/kvm`.
7. Report incomplete host-network setup.

The command must not replace an existing private key.

### 4.2 Create

```sh
sandboxctl create NAME [--cpus N] [--memory MiB] [--image NAME]
```

The final command must copy the selected base root drive. It must make an instance record before it reports success.

Default values:

| Field | Value |
| --- | ---: |
| CPUs | 2 |
| Memory | 4096 MiB |
| Image | `default` |
| SSH user | `dev` |
| SSH port | 22 |

The name must match this expression:

```text
^[a-z][a-z0-9-]{0,31}$
```

### 4.3 Start

```sh
sandboxctl start NAME
```

This command must:

1. Load the instance record.
2. Refuse a second start when the sandbox runs.
3. Prepare the TAP device.
4. Start a runner that owns the Firecracker SDK machine.
5. Save the process identity.
6. Wait for SSH readiness.
7. Report the address and log path.

The process record must contain more than a process identifier (PID). It must also contain the Linux process start time.

### 4.4 Stop

```sh
sandboxctl stop NAME
```

The command must request a normal stop first. It must wait for a bounded time before it reports success.

The command can send `SIGTERM` after the normal stop timeout. It must not send `SIGKILL` without an explicit option.

### 4.5 List

```sh
sandboxctl list [--json]
```

The text output must contain:

- Name.
- State.
- Guest address.
- CPU count.
- Memory size.
- Herdr registration state.

The JSON output must have a documented schema version.

### 4.6 SSH

```sh
sandboxctl ssh NAME [-- COMMAND ...]
```

This command must start `/usr/bin/ssh` with the tool-owned configuration. It must not use files in `~/.ssh`.

### 4.6.1 SSH configuration include

```sh
sandboxctl ssh-config
```

This command initializes the SSH files and refreshes the managed configuration.
It prints a `Host *` line followed by an `Include` directive with the absolute managed path.
The user adds this block to the main SSH configuration manually.
The tool must not edit that configuration.
Earlier matching settings can take precedence over included settings.

The tool must refresh the managed file during `init` and `create`.
It must use atomic replacement and mode `0600`.
Invalid instance data must not replace the previous file.

### 4.7 Delete

```sh
sandboxctl delete NAME [--yes]
```

This command must refuse a running sandbox. It must show each managed file that it will delete.

## 5. Storage model

Use this layout:

```text
$XDG_CONFIG_HOME/sandboxctl/
  config.json
  ssh_config

$XDG_DATA_HOME/sandboxctl/
  images/
    default/
      image.json
      vmlinux
      root.ext4
  instances/
    dev1/
      instance.json
      root.ext4
  keys/
    client_ed25519
    client_ed25519.pub

$XDG_STATE_HOME/sandboxctl/
  known_hosts
  dev1.log

$XDG_RUNTIME_DIR/sandboxctl/
  dev1.pid.json
  sockets/
```

Do not store a private key in an instance record.

## 6. Instance schema

An instance record must contain:

```json
{
  "schema_version": 1,
  "name": "dev1",
  "created_at": "2026-09-29T14:00:00Z",
  "vcpus": 4,
  "memory_mib": 8192,
  "image": "default",
  "kernel_image": "/absolute/path/vmlinux",
  "root_drive": "/absolute/path/root.ext4",
  "tap_device": "fc-dev1",
  "guest_mac": "06:00:ac:10:00:10",
  "guest_ip": "172.30.0.10",
  "gateway_ip": "172.30.0.1",
  "ssh_user": "dev",
  "ssh_port": 22,
  "herdr_label": "dev1"
}
```

Unknown fields must not cause data loss during a read and write cycle. Add a migration before a schema change.

## 7. Lifecycle states

Use these states:

| State | Meaning |
| --- | --- |
| `stopped` | No valid runtime process exists. |
| `starting` | The process exists, but SSH is not ready. |
| `running` | The process exists and SSH responds. |
| `stopping` | A stop request is active. |
| `failed` | Start or stop did not complete. |
| `unknown` | The saved process identity cannot prove current state. |

Do not use the presence of a PID file as proof of a running sandbox.

## 8. Firecracker contract

The runtime uses the Firecracker Go SDK. A per-sandbox runner process owns the SDK machine until the guest exits.
The `start` command must wait for the runner to report successful machine startup.
The runner must keep the SDK start context active while the guest runs.
The runner must handle termination signals, request guest shutdown, and stop the VMM if shutdown times out.
It must save the runner PID and Linux process start time after startup.
The Firecracker API socket must stay under the private runtime directory.

The SDK configuration must map these instance fields:

```text
KernelImagePath: kernel_image
KernelArgs: kernel_options
Drives: root_drive as a writable root device
NetworkInterfaces: tap_device and guest_mac
MachineCfg: vcpus and memory_mib
SocketPath: private runtime socket path
```

The runtime must not log private-key data.

## 9. Guest metadata contract

Use MMDS for per-sandbox bootstrap data:

```json
{
  "sandboxctl": {
    "schema_version": 1,
    "hostname": "dev1",
    "ssh_user": "dev",
    "ssh_authorized_key": "ssh-ed25519 AAAA... sandboxctl"
  }
}
```

The guest service must validate all values. It must write the authorized-key file with mode `0600`.

The service must write a completion marker. It must not repeat an incomplete write without recovery logic.

## 10. Host network contract

The privilege helper can perform only these operations:

- Create or inspect bridge `fcbr0`.
- Set address `172.30.0.1/24` on the bridge.
- Create or inspect a validated TAP device.
- Attach the TAP device to `fcbr0`.
- Set the TAP device up or down.
- Add or remove sandboxctl-owned nftables rules.

The helper must reject all other interface names, addresses, and commands.

Use a dedicated nftables table. Do not change rules in an unrelated table.

## 11. SSH configuration contract

The tool-owned SSH configuration must contain one entry for each saved sandbox.
Each entry must match the sandbox alias and its recorded guest address.
For example:

```sshconfig
Host sandboxctl-dev1 172.30.0.10
    HostName 172.30.0.10
    User dev
    Port 22
    IdentityFile "/absolute/path/client_ed25519"
    IdentitiesOnly yes
    UserKnownHostsFile "/absolute/path/known_hosts"
    GlobalKnownHostsFile /dev/null
    StrictHostKeyChecking ask
    CheckHostIP no
```

Do not set `StrictHostKeyChecking no`.

## 12. Scope

Application-specific SSH management is outside the first phase.
The tool owns its SSH configuration file.
The user owns the main SSH configuration and its include directive.

## 13. Failure rules

All create operations must use temporary files and atomic replacement.

If `create` fails, remove only new files from that operation.

If `start` fails, keep the root drive. Remove the failed runtime record after log collection.

If host-key verification fails, do not remove or replace the recorded key automatically.

## 14. Security rules

- Never run the main program as root.
- Keep root operations in the small privilege helper.
- Validate sandbox names before path construction.
- Resolve managed paths before deletion.
- Refuse a path outside the instance directory.
- Keep strict host-key checking active.
- Do not print private-key contents.
- Do not put secrets in process arguments.
- Do not trust MMDS values inside the guest without validation.
- Do not expose the Firecracker API socket outside the runtime directory.

## 15. Test requirements

Unit tests must cover:

- Name validation.
- Address allocation.
- TAP-name allocation.
- JSON schema handling.
- Atomic record writes.
- PID reuse detection.
- Firecracker SDK configuration construction.
- Managed SSH configuration generation.
- Manual SSH include behavior.
- Managed-path deletion checks.

Integration tests must cover:

- One complete boot and SSH connection.
- Two concurrent sandboxes.
- Stop and restart with persistent files.
- A stale PID record.
- A changed host key.
- No read or write in a temporary fake `~/.ssh` directory.

## 16. Release acceptance test

Run this sequence on a clean Arch Linux host:

```sh
sandboxctl init
sandboxctl create dev1 --cpus 4 --memory 8192
sandboxctl create dev2 --cpus 2 --memory 4096
sandboxctl start dev1
sandboxctl start dev2
sandboxctl ssh dev1 -- hostname
sandboxctl ssh dev2 -- hostname
ssh sandboxctl-dev1 hostname
ssh sandboxctl-dev2 hostname
```

Install the manual SSH include before testing the aliases.
Stop and start `dev1`. The SSH alias must still work.

## 17. Open design items

Resolve these items before version 1:

- Select the base Linux distribution.
- Select the kernel source and update policy.
- Select the Firecracker version policy.
- Select the privilege-helper installation method.
- Select the normal-stop mechanism inside the guest.
- Select the project license.
- Decide whether version 1 must use one key or one key for each sandbox.
