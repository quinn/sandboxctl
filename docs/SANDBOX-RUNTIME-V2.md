# Secure sandbox runtime specification

Status: Authoritative design update

Date: 2026-10-03

## 1. Authority

This document defines the required runtime architecture for sandboxctl.

It replaces conflicting runtime, privilege, image, networking, SSH, and Herdr requirements in other project documents.

The Firecracker Go Software Development Kit (SDK) is already an accepted
project decision. This document does not compare the SDK with firectl. Continue
to use the SDK.

The current implementation is incomplete. Replace any behavior that conflicts with this design. Do not preserve a behavior only because code exists for it.

## 2. Required result

sandboxctl must run local Linux development sandboxes as Firecracker microVMs.

Normal sandbox operations must not prompt for sudo.

One privileged host-install command must install the required host service and policy:

    sudo sandboxctl host install --operator "$USER"

After host installation, these commands must run as the operator:

    sandboxctl image install default
    sandboxctl create dev1
    sandboxctl start dev1
    sandboxctl stop dev1
    sandboxctl delete dev1

Each running Firecracker process must use a unique unprivileged host user
identifier (UID) and group identifier (GID).

The Firecracker jailer must create the execution jail. The jailer starts with root privileges and drops privileges before it starts Firecracker.

The guest root user must not map to the host root user.

## 3. Scope

Version 1 supports:

- An Arch Linux host.
- x86-64 hosts first.
- Linux guests only.
- Firecracker with Kernel-based Virtual Machine support.
- The Firecracker Go SDK.
- The Firecracker jailer.
- systemd socket activation for the privileged service.
- A shared immutable SquashFS base image.
- One writable ext4 overlay disk for each sandbox.
- Routed TAP networking.
- OpenSSH configuration through one generated Include file.

Version 1 does not support:

- Non-Linux guests.
- CPU emulation.
- Remote Firecracker hosts.
- Live migration.
- High availability.
- Automatic VM startup during host boot.
- Direct Herdr commands or Herdr state management.
- SSH wrapper executables.
- A writable copy of the complete base root filesystem.
- A general root command runner.

## 4. Component model

| Component | Host identity | Responsibility |
| --- | --- | --- |
| sandboxctl | Operator | User commands, local SSH keys, generated SSH configuration |
| sandboxd | Root | Validated privileged host operations and VM lifecycle control |
| jailer | Root during setup | Jail construction, namespace setup, cgroup setup, and privilege drop |
| firecracker | Unique VM UID and GID | One microVM process |
| Guest initramfs | Guest root | Read-only base and writable overlay assembly |
| Guest bootstrap service | Guest root | Hostname, network, SSH key, and first-boot setup |

sandboxctl and sandboxd communicate through a Unix domain socket.

The operator must not start jailer or Firecracker directly during normal use.

## 5. Privilege model

### 5.1 One-time interactive escalation

The host-install command is the only interactive privilege escalation required for routine installation.

Host upgrade and host removal can require a later explicit sudo command. Routine VM operations must not require sudo.

The Arch package must install `sandboxctl`, its `sandboxctl-<command>` helper
executables, `sandboxd`, `sandboxd.socket`, and `sandboxd.service`. The
`sandboxctl` front end must find command helpers through `PATH` and replace
itself with the selected helper. The `sandboxd` executable must remain separate.
The Arch package must include the official x86-64 Firecracker and jailer
binaries from the same pinned release. The jailer requires a statically linked
Firecracker binary. The package must not depend on a dynamically linked host
Firecracker package. The package must not enable the socket.

Use one checkout-based package recipe at `packaging/local/PKGBUILD` for local installation and GitHub releases.
Do not maintain a separate Arch User Repository (AUR) recipe.
Release builds must use an existing `vMAJOR.MINOR.PATCH` tag at the checked-out commit.
Require a clean checkout and the required checks before release packaging.
Build GitHub releases on a GitHub-hosted runner in an Arch Linux container.
Run package creation as an unprivileged user.
Include the completed builder bundle in both package profiles.
Use the existing local builder cache in `$XDG_DATA_HOME/sandboxctl/images/builder/`.
Use `~/.local/share` when `XDG_DATA_HOME` is not set.
Reuse a cached bundle when its manifest, kernel, initramfs, and SquashFS files are present, nonempty, and not symbolic links.
Keep manual cache invalidation through `make clean-builder`.
Source changes must not invalidate the cache automatically.
Build a new builder bundle on a cache miss.
Copy the selected bundle into package staging and install it at `/usr/share/sandboxctl/builder`.
`sandboxctl init` must verify the packaged manifest and artifacts before importing them through sandboxd.
GitHub Actions must cache only completed builder bundles with a manually selected cache revision.
Release files must include the Arch package, `SHA256SUMS`, and `release.json`.
Record the source commit, package checksum, and builder identity in `release.json`.
Verify the package and builder before draft upload.
Require the remote tag to match the recorded source commit.
Create or update draft releases only.
Refuse to replace files in a published release.

The host-install command must:

1. Verify that it runs as root.
2. Resolve the selected operator before it changes any files.
3. Verify that the package-installed `sandboxd` executable and unit files exist.
4. Verify that Firecracker and jailer have the same release version.
5. Verify that Firecracker is statically linked for the jailer chroot.
6. Create the sandboxctl system group.
7. Add the selected operator to the sandboxctl group.
8. Create root-owned configuration and state directories.
9. Configure the dedicated sandboxctl nftables table.
10. Configure IPv4 forwarding through a dedicated sysctl file.
11. Enable sandboxd.socket.
12. Tell the operator when a new login session is necessary.

The installer must be idempotent.

The installer must not create a passwordless sudo rule.

The installer must not add capabilities to sandboxctl, Firecracker, or jailer.

### 5.2 Privileged service

sandboxd must run as root.

systemd must start sandboxd when a client connects to:

    /run/sandboxctl/control.sock

The socket must have these access properties:

| Property | Value |
| --- | --- |
| Owner | root |
| Group | sandboxctl |
| Mode | 0660 |

The socket owner, group, and mode control which local users can connect.
Use the socket permissions as the only client authorization check.
Do not add a second user or group authorization check in sandboxd.
Membership in the `sandboxctl` group grants access to the full sandboxd API.
Treat this group as a trusted host-operator group.

sandboxd must use a restrictive umask.

The control API must use only the Unix socket.
sandboxd may bind Transmission Control Protocol (TCP) listeners on `127.0.0.1` for registered guest port forwards.

sandboxd must not expose the Firecracker application programming interface
(API) socket outside the jail and managed runtime directories.

### 5.3 Privileged API

Use gRPC over the Unix domain socket for the control protocol.
Define messages and services with Protocol Buffers.
Use Buf to lint the schema and generate Go code.
Pin Buf remote plugin versions in the generation configuration.

The control protocol must be versioned.

Use a separate gRPC method for each operation. The method name identifies the
operation. Do not add a generic `Call` method or operation-name field.

Keep protocol version and request identifier metadata out of the Protocol
Buffers messages. Use the `sandboxctl-api-version` metadata key for the API
version. Use the `x-request-id` metadata key for the request identifier.

The client interceptor must set both metadata values. It must create a new
request identifier for each call. The server interceptor must validate both
values and add the request identifier to the response metadata. Do not add a
version-negotiation RPC.

Each request must contain operation-specific data only.

Use gRPC status codes for stable structured errors.

The protocol must enforce request-size limits and operation deadlines.

Allowed operation groups are:

- Host status.
- Image list, verified image import, and guarded image deletion.
- Instance create, inspect, list, start, stop, reset, and delete.
- Runtime status.
- Bounded log retrieval.
- Guest TCP forward preparation, registration, listing, and removal.

The API must not provide:

- A shell command field.
- An arbitrary executable field.
- An arbitrary host path field.
- A caller-selected UID or GID.
- A caller-selected TAP name.
- A caller-selected nftables command.
- Raw jailer arguments.
- Raw Firecracker process arguments.

Image import must not make sandboxd open an arbitrary user path by name.

Use one of these safe import methods:

1. Pass an already-open regular-file descriptor with the Unix SCM_RIGHTS mechanism.
2. Stream verified bytes through the authenticated Unix socket.

sandboxd must calculate and verify the expected digest again before it commits an imported artifact.

### 5.4 Prohibited privilege patterns

Do not:

- Run sandboxctl as root for normal commands.
- Run Firecracker as root.
- Run Firecracker as the operator.
- Give CAP_NET_ADMIN to Firecracker.
- Give CAP_SYS_ADMIN to Firecracker.
- Give a general-purpose CLI passwordless sudo.
- install a set-user-ID sandboxctl binary.
- Accept trusted jailer paths from the operator.

## 6. Host identities

sandboxd must allocate one numeric host UID and GID for each sandbox.

The allocation must remain stable while the sandbox exists.

The allocation registry must be root-owned and atomically updated.

The host installer must select a configurable UID and GID range. It must verify that the range does not conflict with:

- Existing local accounts.
- Existing local groups.
- Configured subordinate UID ranges.
- Configured subordinate GID ranges.

The allocated identities do not need login shells or home directories.

Do not reuse an identity until:

1. The VM has stopped.
2. The jail has been removed.
3. All processes with that identity have stopped.
4. All owned runtime files have been removed or reassigned.
5. The instance has been deleted.

## 7. Firecracker and jailer

### 7.1 SDK boundary

Continue to use the Firecracker Go SDK.

Use the SDK for Firecracker API configuration and lifecycle requests.

sandboxd must remain responsible for:

- Managed path construction.
- Image placement.
- Jailer arguments.
- UID and GID allocation.
- TAP creation.
- nftables rules.
- Process identity.
- Cleanup.

Do not use SDK Container Network Interface (CNI) automation.

Do not use firectl in the production runtime.

Pin the SDK version. Pin the Firecracker release. Test the selected pair together.

If an SDK feature does not support the selected Firecracker release, use the SDK generated API client or a small typed API implementation. Do not use shell curl commands.

### 7.2 Jailer invocation

sandboxd must start jailer with:

- A validated sandbox identifier.
- The root-owned Firecracker executable.
- The allocated UID.
- The allocated GID.
- A root-owned chroot base directory.
- cgroup version 2.
- Explicit resource limits.

Keep the default Firecracker seccomp filters active.

Do not pass no-seccomp.

Do not let the operator supply an executable path, chroot path, network namespace path, UID, GID, or jailer option.

All jailer input paths and parent directories must be root-owned and not writable by the operator.

The jailer must drop privileges before it executes Firecracker.

### 7.3 Jail resources

Each jail must contain only the resources required by one VM:

- Firecracker executable.
- Kernel image.
- Initramfs image.
- Read-only SquashFS base.
- Writable ext4 state disk.
- API socket.
- Bounded logging endpoints.

The kernel, initramfs, and SquashFS files must be immutable to the VM UID.

The writable state disk must be accessible only to the VM UID and sandboxd.

Do not place the operator SSH private key in the jail.

Do not place the sandboxd control socket in the jail.

### 7.4 Process supervision

sandboxd must record:

- Host process identifier (PID).
- Process start time.
- Host boot identifier.
- Sandbox UID and GID.
- Jailer identifier.
- Firecracker API socket path.
- Start timestamp.

A PID file alone is not proof that a VM runs.

sandboxd must detect PID reuse.

sandboxd must remove orphan jailer, cgroup, TAP, and socket resources after it proves that no matching Firecracker process runs.

A sandboxd restart must not cause an automatic VM start.

After a sandboxd restart, adopt an existing VM only when all these values
match the saved record:

- Managed cgroup.
- Process UID and GID.
- Executable identity.
- PID start time.
- Host boot identifier.
- Jailer identifier.
- Firecracker API socket.
- Firecracker VM identifier.

If sandboxd cannot prove all values, it must stop processes in the managed
cgroup, remove incomplete runtime resources, and report the sandbox as failed.

Do not silently assume that an unverified process belongs to a sandbox.

## 8. Image model

### 8.1 Image set

One installed image version consists of:

- A Linux kernel.
- A boot initramfs.
- A read-only SquashFS root filesystem.
- An image manifest.

The project image manifest must contain:

- Schema version.
- Image name.
- Image version.
- CPU architecture.
- Firecracker compatibility range.
- Kernel version.
- Guest distribution and version.
- Artifact sizes.
- SHA-256 digest for each artifact.
- Required kernel arguments.
- Build provenance reference.

Store the boot artifacts as `vmlinux`, `initramfs.cpio.gz`, and `root.squashfs` beside `manifest.json`.

Store each installed image under `/var/lib/sandboxctl/images/NAME/`.
Store its manifest as `manifest.json`.
The manifest must use `schema_version`, `name`, and `version` fields.
The `version` field identifies the pinned installed image version.
Use `sha256:` followed by the digest of the manifest fields and fixed artifact digests.
Hash the schema, name, architecture, compatibility, kernel, guest, provenance, kernel arguments, and artifact fields in that order.
Encode each UTF-8 field as its 64-bit big-endian byte length followed by its bytes.

Do not select an artifact by sorting an untrusted remote directory listing and taking the last value.

Use an explicit image version and pinned digests.

Download as the operator. Verify before import. Verify again in sandboxd.

The installed image store must be root-owned.

Installed kernel, initramfs, and SquashFS artifacts must not be writable by the operator or VM UIDs.

### 8.2 No host extraction

The normal host installation must not run unsquashfs.

The normal host installation must not mount the guest root filesystem.

The normal host installation must not convert the SquashFS image to a writable ext4 root image.

The project image build pipeline can build the SquashFS artifact separately. The runtime host consumes the completed artifact.

### 8.3 Writable state disk

Each sandbox gets one private sparse ext4 disk.

The default virtual size is 20 GiB.

The disk stores the OverlayFS upper and work directories.

The disk file must use mode 0600.

The disk file must be owned by the sandbox UID and GID.

Two sandboxes must never share one writable state disk.

The base SquashFS can be shared by all sandboxes because it is read-only.

### 8.4 Guest overlay boot

Firecracker presents:

| Guest device | Use | Access |
| --- | --- | --- |
| /dev/vda | SquashFS lower filesystem | Read-only |
| /dev/vdb | ext4 overlay state | Read-write |

The initramfs must:

1. Mount proc, sysfs, and devtmpfs.
2. Wait for the required block devices.
3. Mount /dev/vda read-only at /lower.
4. Mount /dev/vdb at /state.
5. Create /state/upper and /state/work when necessary.
6. Mount OverlayFS at /newroot.
7. Move required pseudo-filesystem mounts into /newroot.
8. Run switch_root into the guest init system.

The lower filesystem must remain read-only.

All guest changes outside separate mounts must go to the private state disk.

A reset operation must replace the state disk after the VM stops. It must not replace the base image.

A separate persistent home disk is outside version 1. The design must permit it later.

### 8.5 Guest bootstrap

The project-provided SquashFS image must contain:

- An init system.
- OpenSSH server.
- A non-root dev user.
- sudo with a documented guest policy.
- CA certificates.
- Git.
- A sandboxctl guest bootstrap service.

The bootstrap service must read versioned data from the Firecracker MicroVM
Metadata Service (MMDS).

Required metadata is:

    {
      "sandboxctl": {
        "schema_version": 1,
        "hostname": "dev1",
        "ssh_user": "dev",
        "ssh_authorized_key": "ssh-ed25519 AAAA...",
        "network": {
          "address": "172.30.0.2/30",
          "gateway": "172.30.0.1",
          "interface": "eth0"
        }
      }
    }

The service must:

1. Validate all values.
2. Set the hostname.
3. Confirm the guest address and route.
4. Install the authorized SSH key.
5. Use mode 0700 for the .ssh directory.
6. Use mode 0600 for authorized_keys.
7. Ensure that SSH host keys exist in the writable overlay.
8. Disable SSH password authentication.
9. Write an atomic completion marker.

The service must be safe to run again.

The guest must have basic network configuration before it requests MMDS data.

sandboxd must add an early network configuration to the kernel arguments. The
kernel and initramfs must support Linux IP autoconfiguration. The early
configuration must contain the guest address, host gateway, /30 netmask,
hostname, and interface name.

The initramfs must add the link-local route that is required to reach MMDS.

MMDS must contain the same network values. The bootstrap service must reject a
material difference between the early network configuration and MMDS.

## 9. Networking

### 9.1 Routed TAP design

Version 1 must use one routed TAP network for each VM.

Do not use a shared Layer 2 bridge in version 1.

The default managed address pool is:

    172.30.0.0/16

The installer must reject this default when it conflicts with an existing host route. The host configuration must permit a different private pool.

Allocate one /30 subnet for each sandbox.

Within each /30:

- The first usable address is the host TAP address.
- The second usable address is the guest address.

The allocation must be stable while the sandbox exists.

The allocation registry must detect conflicts.

TAP names must fit the Linux interface-name limit.

sandboxd must select TAP names. A client must not supply them.

sandboxd must:

1. Create the TAP.
2. Assign ownership to the sandbox UID and GID.
3. Assign the host address.
4. Set the TAP up.
5. Install the required routes and firewall rules.
6. Remove the TAP after the VM stops.

Firecracker must only open the prepared TAP. Firecracker must not create or configure host networking.

### 9.2 nftables

Use a dedicated nftables table named sandboxctl.

Do not change an unrelated table or chain.

Some hosts have a separate IPv4 FORWARD chain managed by iptables-nft with a
drop policy. Rules in the inet sandboxctl table cannot override a drop in that
chain. On these hosts, add tagged sandboxctl rules to the existing IPv4
FORWARD chain. Match only the sandboxctl TAP prefix and address pool. Keep the
existing chain policy and unrelated rules unchanged. The rules must permit
guest outbound traffic and established return traffic, and deny guest-to-guest
and unsolicited inbound traffic.

Rules must:

- Permit host-to-guest SSH.
- Permit established and related return traffic.
- Permit guest outbound traffic through network address translation.
- Deny guest-to-guest traffic by default.
- Deny unsolicited inbound traffic from external interfaces by default.

Rule creation and removal must be idempotent.

Do not change the host-wide forward policy.

The first guest network profile uses Cloudflare DNS at `1.1.1.1` and
`1.0.0.1`. Guest bootstrap must write these addresses to `/etc/resolv.conf`.
The host firewall must allow the guest to reach these resolvers through normal
outbound rules.

### 9.3 Network state

The instance record must contain:

- Allocated subnet.
- Host address.
- Guest address.
- TAP name.
- Guest MAC address.

Do not derive current ownership only from an interface name.

## 10. SSH configuration

### 10.1 Integration method

sandboxctl must generate one OpenSSH configuration fragment:

    $XDG_CONFIG_HOME/sandboxctl/ssh_config

The user includes that file from the normal OpenSSH configuration.

For the default XDG path, the user adds this line near the start of ~/.ssh/config:

    Include ~/.config/sandboxctl/ssh_config

When XDG_CONFIG_HOME has a non-default value, sandboxctl must print the correct absolute Include path.

sandboxctl must not edit ~/.ssh/config automatically.

sandboxctl init must:

1. Create an empty generated fragment when necessary.
2. Print the required Include line.
3. Check whether normal ssh resolution sees a generated test alias.
4. Report a clear action when the Include directive is missing.

### 10.2 Generated hosts

Use one stable alias for each sandbox:

    sandbox-NAME

For a sandbox named dev1, generate:

    Host sandbox-dev1
        HostName 172.30.0.2
        HostKeyAlias sandbox-instance-0123456789abcdef0123456789abcdef
        User dev
        Port 22
        IdentityFile "/home/USER/.local/share/sandboxctl/keys/dev1_ed25519"
        IdentitiesOnly yes
        UserKnownHostsFile "/home/USER/.local/state/sandboxctl/known_hosts"
        StrictHostKeyChecking ask
        CheckHostIP no

Use resolved absolute paths in generated entries.

Do not generate a Host * block.

Write the complete fragment to a temporary file. Set mode 0600. Rename it atomically.

Regenerate the complete fragment after create, delete, address change, or key change.

Add this recovery command:

    sandboxctl ssh-config sync

The command must rebuild the fragment from authoritative instance state.

### 10.3 SSH keys

Create one Ed25519 client key for each sandbox.

Store operator private keys only under:

    $XDG_DATA_HOME/sandboxctl/keys

Use mode 0700 for the key directory.

Use mode 0600 for each private key.

The operator private key remains owned by the operator.

Send only the public key to sandboxd and the guest.

Do not store operator private-key data in:

- Instance records.
- MMDS.
- sandboxd state.
- The jail.
- Logs.

### 10.4 Host-key checking

Keep strict host-key checking active.

Use the managed known-hosts file:

    $XDG_STATE_HOME/sandboxctl/known_hosts

Set `HostKeyAlias` to `sandbox-instance-` followed by the daemon's stable instance identifier.
Use the same host-key alias for the sandbox name and its numeric guest address.
Do not use the sandbox name or address as the trusted identity.
A recreated sandbox has a new identifier and requires new trust.
Keep the same host-key alias across stop and start operations.
Do not copy trust from old address entries when upgrading the SSH configuration.
The first connection after that upgrade requires host-key confirmation.
Reset retains the instance identifier but replaces the guest host keys.
After reset, require verified explicit removal of the old entry before accepting the new key.

The first interactive connection can ask the user to trust the guest host key.

Do not replace a changed host key automatically.

Provide an explicit command for a verified host-key replacement.

### 10.5 SSH command

This command:

    sandboxctl ssh dev1 -- uname -a

must execute the equivalent of:

    /usr/bin/ssh sandbox-dev1 -- uname -a

It must use the normal OpenSSH configuration resolution.

Do not pass -F.

Do not use an SSH wrapper.

## 11. Herdr boundary

sandboxctl has no direct Herdr integration.

Remove these features from the target design:

- sandboxctl herdr register
- sandboxctl herdr run
- Herdr command construction
- Herdr state inspection
- PATH replacement
- Private ssh and scp wrappers
- Herdr-specific compatibility tests

Herdr and other SSH clients use the generated alias through the normal OpenSSH Include mechanism.

For example, an external client can use:

    sandbox-dev1

sandboxctl must not write Herdr files or invoke Herdr commands.

## 12. Storage

### 12.1 Root-owned host state

Use:

    /etc/sandboxctl/
      host.json

    /var/lib/sandboxctl/
      images/
      instances/
      allocations/
      jails/

Store each instance under `instances/NAME/`.
Keep its `instance.json`, `authorized_key`, and `state.ext4` files in that directory.
Use mode `0600` for these files.

    /run/sandboxctl/
      control.sock
      api/
      runtime/

Root-owned configuration and parent directories must not be writable by the operator.

### 12.2 Operator-owned state

Use:

    $XDG_CONFIG_HOME/sandboxctl/
      ssh_config
      client.json

    $XDG_DATA_HOME/sandboxctl/
      keys/
      images/NAME/DIGEST/

    $XDG_STATE_HOME/sandboxctl/
      known_hosts

    $XDG_CACHE_HOME/sandboxctl/
      downloads/

Do not place privileged instance state in an operator-writable directory.
Keep user-owned image bundles in `images/NAME/DIGEST/`. Stream bundles to
sandboxd for installation in the root-owned image store.

### 12.3 Instance record

The privileged instance record must contain:

    {
      "schema_version": 3,
      "id": "stable-random-id",
      "name": "dev1",
      "created_at": "2026-10-03T14:00:00Z",
      "vcpus": 4,
      "memory_mib": 8192,
      "state_disk_bytes": 21474836480,
      "image": "default",
      "image_version": "pinned-version",
      "host_uid": 300001,
      "host_gid": 300001,
      "subnet": "172.30.0.0/30",
      "host_ip": "172.30.0.1",
      "guest_ip": "172.30.0.2",
      "tap_device": "sc000001",
      "guest_mac": "06:00:ac:10:00:02",
      "ssh_user": "dev",
      "ssh_alias": "sandbox-dev1",
      "forwards": [],
      "forward_host_key": ""
    }

Do not store an operator private-key path in privileged state unless it is display-only and never opened by sandboxd.

Unknown fields must survive a read and write cycle.

Add an explicit migration for each schema change.

## 13. Command contract

### 13.1 User initialization

    sandboxctl init

This command must:

- Create operator XDG directories.
- Create the generated SSH fragment.
- Create the managed known-hosts file.
- Print the OpenSSH Include instruction.
- Report host-service availability.

### 13.2 Host installation

    sudo sandboxctl host install --operator USER

Optional host-install settings are:

- `--uid-range START:COUNT`, with default `200000:65536`.
- `--gid-range START:COUNT`, with default `200000:65536`.
- `--address-pool CIDR`, with default `172.30.0.0/16`.

This command installs the privileged host components.

Do not combine user initialization with root-owned SSH key creation.

### 13.3 Host diagnosis

    sandboxctl doctor

This command must check:

- CPU virtualization support.
- /dev/kvm presence.
- sandboxd socket access.
- Firecracker and jailer versions.
- cgroup version 2.
- nftables availability.
- TUN support.
- Address-pool conflicts.
- SSH Include resolution.
- Installed image integrity.

The command must not change host state without an explicit repair option.

### 13.4 Image installation

    sandboxctl image install NAME --version VERSION

The command reads a local bundle from the XDG data directory.
The bundle contains `manifest.json`, `vmlinux`, `initramfs.cpio.gz`, and `root.squashfs`.
The command must use a pinned manifest and digest.
The operator command must verify every artifact before it sends the bytes.
The daemon must verify every artifact again before it commits the image.
The daemon must not open a path supplied by the operator.
The daemon must publish the image only after all artifact sizes and digests match.

Do not use a moving latest prefix.

### 13.4.1 Image deletion

    sandboxctl image delete NAME [--yes]

The command must ask for confirmation unless `--yes` is present. It must get
the current pinned version from the daemon, then request deletion of that exact
version. The daemon must refuse to delete an image that any saved sandbox uses.
It must validate the image directory before it removes the fixed image files.
The command must leave staged local bundles in place.

### 13.5 Create

    sandboxctl create NAME [--cpus N] [--memory MiB] [--disk-size GiB] [--image NAME]

Create the operator SSH key before the privileged create request.

Commit privileged instance state before the command reports success.

Regenerate the SSH fragment after the privileged create operation succeeds.

If SSH fragment generation fails, keep the instance and report the recovery command.

### 13.6 Start

    sandboxctl start NAME

The command must:

1. Ask sandboxd to start the VM.
2. Wait for a bounded readiness result.
3. Report the SSH alias.

A second start must be idempotent when the correct VM already runs.

### 13.7 Stop

    sandboxctl stop NAME [--force]

The normal stop sequence is:

1. Request a guest shutdown.
2. Wait for a bounded interval.
3. Send SIGTERM when the normal interval expires.
4. Preserve the state disk.

Send SIGKILL only when --force is present or a documented daemon-safety timeout requires it.

### 13.8 Reset

    sandboxctl reset NAME

The command must require a stopped VM.

It must replace only the writable state disk.

It must preserve:

- Instance identity.
- Network allocation.
- Operator SSH key.
- SSH alias.
- Selected base image.

### 13.9 Delete

    sandboxctl delete NAME [--yes]

The command must refuse a running VM.

It must list the managed resources that it will delete.

It must not delete the shared base image.

It must not delete unrelated known-host entries.

### 13.10 List

    sandboxctl list [--json]

Text output must contain:

- Name.
- Runtime state.
- Guest address.
- SSH alias.
- Image name.
- CPU count.
- Memory size.
- State-disk virtual size.

JSON output must have a schema version.

### 13.11 Port forwarding

    sandboxctl forward NAME [HOST_PORT:]GUEST_PORT
    sandboxctl forward NAME --list
    sandboxctl forward NAME --remove GUEST_PORT

The sandbox must be running when a mapping is added.
Host ports range from 1024 to 65535.
Guest ports range from 1 to 65535.
The host port is an optional preference.
Bind the listener directly and keep it open.
If the preferred or saved port is in use, bind port zero to select a free port.
Expose listeners only on host `127.0.0.1`.
Forward TCP connections only to guest `127.0.0.1`.
Use `golang.org/x/crypto/ssh` in the daemon.
Share one SSH connection for each sandbox.
Bound the number of mappings and active connections.
The CLI must return after registration and print the assigned endpoint.

Save each mapping's guest port, preferred host port, and last assigned host port in `instance.json`.
Migrate version 2 records explicitly to version 3 without discarding unknown fields.
Serialize record changes with start, stop, reset, and delete.
Stop must close listeners and active connections.
Start and daemon reconciliation must restore saved mappings for verified running instances.
Delete must remove saved mappings and the forwarding key.
Reset must retain mappings and clear the trusted forwarding host key.
After reset, require explicit host-key trust before restoring the mappings.

Create one daemon-owned Ed25519 forwarding key for each instance.
Store its private part in `forward_ed25519` with mode `0600` in the private instance directory.
Never open an operator private key in the daemon.
The preparation RPC may return only the forwarding public key and instance identity.
The CLI uses the operator key and managed known-hosts file to install the public key through native Go SSH.
Use a separate guest key file that bootstrap does not replace.
Restrict this key to local TCP forwarding to guest loopback ports.
Reject shell, command, terminal, agent, and X11 access for this key.
Verify the guest host key before the daemon connects.
Use OpenSSH's host-key algorithm order for algorithms that the Go library supports.
Promote algorithms for trusted keys before the remaining algorithms.
Use the saved host-key pin for daemon preferences.
Use the managed known-hosts entries for CLI setup preferences.
Require exact key verification after algorithm selection.
The registration RPC accepts only typed ports, an expected instance identifier, and a trusted public host key.
Require `--replace-host-key` to change an existing forwarding host-key pin.
Do not accept generic guest commands or filesystem paths through this API.

## 14. Lifecycle states

Use:

| State | Meaning |
| --- | --- |
| stopped | No matching Firecracker process runs |
| preparing | Privileged resources are being prepared |
| starting | Firecracker runs, but the guest is not ready |
| running | The guest readiness check succeeded |
| stopping | A stop operation is active |
| failed | The last lifecycle operation failed |
| unknown | Current process identity cannot be proven |

State transitions must be serialized for each instance.

Two start, stop, reset, or delete operations must not modify the same instance concurrently.

Use a root-owned per-instance lock.

## 15. Atomicity and recovery

Write registries and instance records with:

1. A temporary file in the destination directory.
2. fsync on the file.
3. Atomic rename.
4. fsync on the parent directory when required.

On create failure:

- Remove only resources created by that operation.
- Do not remove an existing key or image.

On start failure:

- Preserve the state disk.
- Collect bounded diagnostic data.
- Remove incomplete TAP, jail, cgroup, and socket resources.

On stop failure:

- Report the proven process state.
- Do not mark the VM stopped while the matching process runs.

On daemon or host restart:

- Do not start a VM automatically.
- Reconcile allocations and runtime resources.
- Preserve all state disks.

## 16. Security requirements

The implementation must:

- Validate a sandbox name before path construction.
- Use stable internal identifiers for privileged paths.
- Reject symlinks in privileged managed paths.
- Use openat2 or equivalent safe relative-path resolution where applicable.
- Avoid time-of-check and time-of-use path races.
- Use absolute executable paths.
- Use exec without a shell.
- Clear or define the environment for privileged child processes.
- Set close-on-exec on unrelated descriptors.
- Bound log and serial-console storage.
- Disable core dumps for privileged processes and Firecracker.
- Keep project-owned nftables rules separate.
- Keep the base image immutable.
- Keep VM writable disks separate.
- Keep VM UIDs separate.
- Keep operator private keys outside privileged storage.
- Keep Firecracker API sockets local and private.

The implementation must treat these as untrusted:

- Guest disk contents.
- Guest network traffic.
- Guest serial output.
- Guest log output.
- MMDS values received by guest software.
- Operator request fields.
- Imported artifact bytes until digest verification succeeds.

## 17. Tests

### 17.1 Unit tests

Add tests for:

- Protocol decoding and size limits.
- Peer authorization decisions.
- Name validation.
- UID and GID allocation.
- /30 address allocation.
- TAP-name allocation.
- Managed path resolution.
- Symlink rejection.
- Image manifest validation.
- Digest verification.
- Jailer argument construction.
- SDK configuration.
- Lifecycle state transitions.
- PID reuse detection.
- SSH fragment generation.
- Atomic SSH fragment replacement.
- Instance schema migration.
- Deletion boundaries.

### 17.2 Privileged integration tests

Run these tests on a dedicated Linux KVM host:

- Host installation is idempotent.
- Normal commands do not invoke sudo.
- Firecracker runs under the allocated UID and GID.
- Two running VMs use different UIDs and GIDs.
- Two running VMs use different writable disks.
- A VM cannot modify the shared SquashFS.
- A VM cannot open another VM state disk.
- A VM can install a package into its overlay.
- A stop and start preserves overlay changes.
- A reset removes overlay changes.
- Guest outbound networking works.
- Host-to-guest SSH works.
- Guest-to-guest traffic is denied by default.
- Unrelated nftables state remains unchanged.
- A sandboxd restart follows the documented reconciliation behavior.
- A malformed socket request cannot select a host path or executable.

### 17.3 SSH tests

Use a temporary HOME and XDG directory set.

Verify:

- sandboxctl does not edit ~/.ssh/config.
- The generated Include instruction is correct.
- ssh -G sandbox-dev1 resolves the generated values after the test adds the Include line.
- The generated file contains no Host * section.
- Each sandbox uses its own key.
- A changed host key is not accepted automatically.
- No wrapper directory or PATH change is necessary.

Do not add Herdr-specific tests.

## 18. Implementation sequence

Before the implementation sequence, complete the risk-first Phase 1 storage proof.
Use the Phase 1 plan described above.
Keep this proof isolated from sandboxd, the installer, and networking.
Do not continue to the control protocol until the proof meets its exit conditions.

Complete the implementation in this order:

1. Keep the existing project build green.
2. Add the sandboxd command and versioned Unix socket protocol.
3. Add systemd socket and service assets.
4. Add host install and doctor commands.
5. Move privileged state from XDG directories to /var/lib/sandboxctl.
6. Add UID, GID, address, and TAP allocation registries.
7. Add jailer launch through the established SDK-based runtime.
8. Add immutable image import.
9. Add initramfs OverlayFS boot.
10. Add routed TAP and nftables support.
11. Generate one OpenSSH Include fragment.
12. Remove direct Herdr commands and the internal Herdr package.
13. Remove ssh and scp wrapper generation.
14. Remove firectl runtime code after the SDK path passes integration tests.

Do not retain two active runtime architectures.

## 19. Acceptance sequence

Run on a clean supported Arch Linux host:

    sudo sandboxctl host install --operator "$USER"

Start a new login session when group membership changes.

Then run:

    sandboxctl init
    sandboxctl doctor
    sandboxctl image install default --version PINNED_VERSION
    sandboxctl create dev1 --cpus 4 --memory 8192
    sandboxctl create dev2 --cpus 2 --memory 4096
    sandboxctl start dev1
    sandboxctl start dev2
    ssh sandbox-dev1 hostname
    ssh sandbox-dev2 hostname

Confirm:

- No routine command prompts for sudo.
- Firecracker does not run as root.
- Firecracker does not run as the operator.
- The two Firecracker processes have different UIDs.
- The two VMs use the same read-only SquashFS.
- The two VMs use different writable state disks.
- Both guests have outbound network access.
- Guest-to-guest traffic is denied.
- Normal OpenSSH resolves both aliases through Include.

Test persistence:

    ssh sandbox-dev1 -- sh -c 'echo retained > ~/retained.txt'
    sandboxctl stop dev1
    sandboxctl start dev1
    ssh sandbox-dev1 -- cat ~/retained.txt

Test reset:

    sandboxctl stop dev1
    sandboxctl reset dev1
    sandboxctl start dev1

The retained file must no longer exist after reset.

## 20. Primary references

- Firecracker jailer: https://github.com/firecracker-microvm/firecracker/blob/main/docs/jailer.md
- Firecracker production host setup: https://github.com/firecracker-microvm/firecracker/blob/main/docs/prod-host-setup.md
- Firecracker block devices: https://github.com/firecracker-microvm/firecracker/blob/main/docs/block.md
- Firecracker Go SDK: https://github.com/firecracker-microvm/firecracker-go-sdk
- OpenSSH client configuration: https://man.openbsd.org/ssh_config
- ASD-STE100: https://www.asd-ste100.org/
