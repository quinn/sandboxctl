# Design decisions

## Platform

The host and all guests use Linux. The first supported host is Arch Linux on x86-64.

The tool uses Kernel-based Virtual Machine (KVM) through Firecracker. It does not support CPU emulation.

## Command input boundary

Each CLI command owns argument parsing and user-input validation under
`cmd/sandboxctl/<command>`. Shared command parsing and validation belongs in
`internal/cmdutil`. Internal operation packages receive normalized, typed
inputs instead of raw command-line arguments. They can still validate stored
state and host invariants before they act.

The `sandboxctl` front end uses generic external-subcommand dispatch. It finds
`sandboxctl-<command>` on `PATH` and replaces itself with that executable.
Subcommand executables own their command behavior. The `sandboxd` executable
does not use this dispatcher.

## Guest port forwarding

`sandboxd` manages TCP forwarding listeners on host `127.0.0.1`.
The control API continues to use only the Unix socket.
Use `golang.org/x/crypto/ssh` for setup and forwarding.
Share one daemon SSH connection for each sandbox.
Use OpenSSH's host-key preference order among algorithms supported by Go.
Promote algorithms for trusted host keys.
Keep exact key verification after negotiation.
Save mappings and the trusted guest public host key in the instance record.
Use a separate daemon-owned forwarding key for each stable instance identifier.
The daemon must not open operator private keys.
The CLI installs the restricted forwarding public key through operator SSH access.
A separate guest key file preserves forwarding access across bootstrap runs in existing images.
The guest policy permits local TCP forwarding only to loopback ports.
The forwarding key cannot run commands or request a terminal.
Use the last assigned host port on restart when it is free.
Bind port zero when a preferred or saved host port is occupied.
Keep the bound listener to avoid a port-selection race.
Clear the forwarding host-key pin after reset.
Require explicit operator host-key verification before forwarding resumes.

## Runtime

The runtime uses the Firecracker Go Software Development Kit (SDK) v1.0.0.
Each sandbox has a detached `sandboxctl` runner process. The runner owns the SDK machine until it exits.
The start command waits for Firecracker to start and then returns.
The runner handles stop signals. It requests guest shutdown before it stops the VMM.

The tool starts sandboxes only when the user requests it. The privileged `sandboxd` service uses systemd socket activation. Routine sandbox commands run without `sudo`.
The Arch package owns the CLI, its command executables, the daemon, and systemd unit files. The explicit host-install command owns per-host configuration, operator group membership, network policy, and socket activation.

The first working iteration uses a prepared kernel, root drive, and TAP device.
The tool copies the root drive into the private instance directory.
Host networking and guest image construction remain manual for this iteration.

## Guest images

Dockerfile input is the next planned image build mode. BuildKit will execute
the Dockerfile inside the existing temporary builder VM. Required sandboxctl
provisioning follows the user build. The VM boots its guest init system as
PID 1. Dockerfile `CMD` and `ENTRYPOINT` do not start guest applications.
See [DOCKERFILE-IMAGE-BUILDS.md](DOCKERFILE-IMAGE-BUILDS.md) for scope and
implementation order. This input mode is not implemented yet.

The Arch package includes the pinned builder image bundle under
`/usr/share/sandboxctl/builder`. `sandboxctl init` verifies the bundle and
streams it to `sandboxd` for installation in the root-owned image store. The
image build command reads its default builder name and version from the
packaged manifest. User-owned image bundles use
`$XDG_DATA_HOME/sandboxctl/images/NAME/DIGEST/`. Podman is a package build
dependency only.

The first image profile supports Debian and Ubuntu systemd images. The image
creates a `dev` account and grants it passwordless sudo inside the guest. This
policy applies only to the isolated guest. The image keeps `authorized_keys`
empty until guest bootstrap installs the per-sandbox key. It disables SSH
password login and generates each VM's SSH host keys during bootstrap.

Each sandbox must have its own writable root drive. The tool must never start two sandboxes with the same writable root drive.
The create command makes a sparse copy of the supplied source drive.

The planned image process uses a read-only base image. The tool makes a copy-on-write copy when the file system supports it. It uses a sparse copy as the fallback.

The base image must contain these items:

- A Linux init system.
- An OpenSSH server.
- A `dev` user.
- A first-boot service for `sandboxctl` metadata.

The first-boot service must install the SSH public key. It must also set the guest host name.

## Network

The first network uses bridge `fcbr0` and subnet `172.30.0.0/24`.

The host address is `172.30.0.1`. Each guest gets a stable address from the instance registry.

Each guest uses one TAP device. Firecracker connects its network device to this TAP device.
The first manual test assigns the host address directly to its TAP device.

The host uses network address translation (NAT) for guest Internet access. The tool must use nftables on Arch Linux.

The local host can connect directly to each guest address. Therefore, Herdr does not need an SSH port-forward rule for the first release.

Remote access to the Firecracker host is a later feature. That feature can use one host port for each sandbox or routed Tailscale addresses.

## SSH

The tool owns one Ed25519 client key for the first release. All managed sandboxes trust this key.

The tool stores the key under `$XDG_DATA_HOME/sandboxctl/keys`. It does not write to `~/.ssh`.

The tool stores host keys in `$XDG_STATE_HOME/sandboxctl/known_hosts`.

The daemon-backed SSH configuration uses `HostKeyAlias sandbox-instance-ID`.
The daemon's stable instance identifier defines host-key trust.
Reused sandbox names and guest addresses do not reuse trust.
Existing address entries remain in the managed file but do not transfer trust to an instance.
Stop and start retain the trusted identity.
Reset retains the identity and requires explicit verified replacement of its host key.

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
