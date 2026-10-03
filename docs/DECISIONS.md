# Design decisions

## Platform

The host and all guests use Linux. The first supported host is Arch Linux on x86-64.

The tool uses Kernel-based Virtual Machine (KVM) through Firecracker. It does not support CPU emulation.

## Runtime

The first runtime starts `firectl` as an external process. The runtime package isolates this choice.

A later version can use the Firecracker Go Software Development Kit (SDK). This change must not change the command interface.

The tool starts sandboxes only when the user requests it. The tool does not require systemd services.

## Guest images

Each sandbox must have its own writable root drive. The tool must never start two sandboxes with the same writable root drive.

The planned image process uses a read-only base image. The tool makes a copy-on-write copy when the file system supports it. It uses a sparse copy as the fallback.

The base image must contain these items:

- A Linux init system.
- An OpenSSH server.
- A `dev` user.
- A first-boot service for `sandboxctl` metadata.
- The Herdr runtime, or a supported path for Herdr installation.

The first-boot service must install the SSH public key. It must also set the guest host name.

## Network

The first network uses bridge `fcbr0` and subnet `172.30.0.0/24`.

The host address is `172.30.0.1`. Each guest gets a stable address from the instance registry.

Each guest uses one TAP device. Firecracker connects its network device to this TAP device.

The host uses network address translation (NAT) for guest Internet access. The tool must use nftables on Arch Linux.

The local host can connect directly to each guest address. Therefore, Herdr does not need an SSH port-forward rule for the first release.

Remote access to the Firecracker host is a later feature. That feature can use one host port for each sandbox or routed Tailscale addresses.

## SSH

The tool owns one Ed25519 client key for the first release. All managed sandboxes trust this key.

The tool stores the key under `$XDG_DATA_HOME/sandboxctl/keys`. It does not write to `~/.ssh`.

The tool stores host keys in `$XDG_STATE_HOME/sandboxctl/known_hosts`.

Strict host-key checking remains active. The first connection can ask the user to accept the host key.

The first release uses an unencrypted client key. The file mode and directory mode protect the key. A later release can use a private `ssh-agent`.

## SSH configuration ownership

The tool owns `$XDG_CONFIG_HOME/sandboxctl/ssh_config`.
It generates exact host entries from the instance records.
Each entry matches `sandboxctl-NAME` and the recorded guest address.
It does not generate a `Host *` entry with authentication settings.

The user can include this file in the main SSH configuration.
The user installs the include block manually.
`sandboxctl ssh-config` refreshes the managed file and prints the block.
The tool never edits the user's main SSH configuration.
Earlier matching settings can take precedence over the included settings.

## Scope

The first phase provides sandbox lifecycle commands and a managed SSH configuration.
Applications can use the sandbox aliases through the user's manual include.
Application-specific connection management is outside this phase.
Existing instance records retain `herdr_label` for schema compatibility.

## State

The tool uses these XDG directories:

```text
$XDG_CONFIG_HOME/sandboxctl
$XDG_DATA_HOME/sandboxctl
$XDG_STATE_HOME/sandboxctl
$XDG_RUNTIME_DIR/sandboxctl
```

An instance record is the source for desired state. Process inspection is the source for current running state.

The tool writes instance records atomically. The tool does not infer a running state only from a PID file.

## Privileges

Normal list, SSH, Herdr, and state operations do not require root privileges.

Bridge, TAP, and nftables operations require additional privileges. A small privilege helper will own those operations.

The main process must not run as root. The privilege helper must accept only validated, structured operations.

