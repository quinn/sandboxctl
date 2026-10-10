# sandboxctl

`sandboxctl` creates and runs Linux development sandboxes as Firecracker virtual
machines. Each sandbox has a private writable disk and a read-only base image.
The tool manages the virtual machine, host network, and Secure Shell (SSH)
settings.

The host must run Arch Linux on x86-64. It must have a working Kernel-based
Virtual Machine (KVM) device at `/dev/kvm`. Guest images must use Debian or
Ubuntu.

## Install and prepare the host

Install the local Arch package from this checkout:

```sh
make install
```

The target uses `yay`. It downloads the pinned Firecracker release from GitHub.

Install the host service and network rules:

```sh
sudo sandboxctl host install --operator "$USER"
```

This command adds the operator to the `sandboxctl` group. Start a new login
session if the command changes your group membership. Routine sandbox commands
do not need `sudo`.

Host installation defaults to user and group ID ranges of `200000:65536` and
the address pool `172.30.0.0/16`. Pass `--uid-range`, `--gid-range`, or
`--address-pool` to select different values.

Initialize the user files and check the host:

```sh
sandboxctl init
sandboxctl doctor
```

`init` prints an OpenSSH `Include` line. Add that line to `~/.ssh/config`
yourself. `sandboxctl` does not edit that file. After you create a sandbox,
check that OpenSSH reads its managed configuration with:

```sh
ssh -G sandbox-NAME
```

## Build and install a guest image

First, create and install the builder image. It is a reusable build
environment. `make stage-builder-image` uses Podman and the pinned BuildKit
tools to create it:

```sh
make stage-builder-image
sandboxctl image install builder --version sha256:BUILDER_DIGEST
```

Use the digest printed by `make stage-builder-image` in the install command.
The installed builder image runs as a temporary virtual machine for each image
build. It is not the operating system image for the guest. The `--base-image`
value below selects that guest operating system:

```sh
sandboxctl image build dev-image \
  --base-image ubuntu:24.04@sha256:BASE_IMAGE_DIGEST \
  --builder-version sha256:BUILDER_DIGEST \
  --guest-version 24.04
```

The build supports Ubuntu and Debian. It uses the installed `builder` image by
default. Use `--distribution debian` for a Debian base image. The base image
must include a valid Secure Hash Algorithm 256-bit (SHA-256) digest. The
command prints the digest and install command for the finished image. Run the
printed install command. It reads the image bundle from your user data
directory.

```sh
sandboxctl image install dev-image --version sha256:IMAGE_DIGEST
```

The image build needs host network rules from host installation. The builder
VM also needs network access to the image registry and package servers.

## Create and use a sandbox

Create a sandbox from the installed image, then start it:

```sh
sandboxctl create dev1 --image dev-image --cpus 2 --memory 4096
sandboxctl start dev1
```

Connect through the generated OpenSSH alias:

```sh
ssh sandbox-dev1
```

You can also run a command in the guest:

```sh
sandboxctl ssh dev1 -- uname -a
```

List sandboxes and their state:

```sh
sandboxctl list
```

Stop a sandbox to keep its writable state disk. Reset the disk to remove guest
changes while keeping the sandbox and its image. Delete a stopped sandbox to
remove its state disk and release its host resources.

```sh
sandboxctl stop dev1
sandboxctl reset dev1
sandboxctl start dev1
sandboxctl stop dev1
sandboxctl delete dev1
```

Delete asks for confirmation. Add `--yes` to skip the prompt. The tool refuses
to delete a running sandbox. Delete does not remove the shared image.

The default state disk is 20 GiB. Set `--disk-size` when you create a sandbox
to choose another size. `--cpus`, `--memory`, and `--image` also have defaults.
Run `sandboxctl help` for command syntax.

Reset creates new guest SSH host keys. Before you connect again, remove the
old host key for the guest address. Get the address from `sandboxctl list` and
the known-hosts path from `sandboxctl paths`, then run:

```sh
ssh-keygen -R GUEST_ADDRESS -f KNOWN_HOSTS_PATH
```

## Commands

- `sandboxctl init` creates user configuration and SSH files.
- `sandboxctl doctor` checks host setup, KVM, networking, SSH, and installed
  images.
- `sandboxctl host install` installs host configuration and enables the
  socket-activated service.
- `sandboxctl image build` builds a Debian or Ubuntu guest image in a temporary
  builder virtual machine.
- `sandboxctl image install` imports a verified local image bundle.
- `sandboxctl create`, `start`, `stop`, `reset`, and `delete` manage sandboxes.
- `sandboxctl list` shows sandbox state.
- `sandboxctl ssh` runs a command through the generated SSH alias.
- `sandboxctl ssh-config` prints the SSH `Include` instruction and refreshes
  the managed SSH file.
- `sandboxctl paths` prints the user and host data paths.
- `sandboxctl version` prints the version.

## Development checks

Run the code checks and build the local commands with:

```sh
make check
make build
```

These manual targets start virtual machines. They need KVM access and an
installed, configured sandboxd service:

- `make manual-bootstrap-test` checks guest bootstrap and SSH.
- `make manual-builder-test` checks the builder image and its tools.
- `make manual-image-build` checks the guest image build pipeline and stages
  its output bundle.

These manual targets check sandboxd state and disk operations. They need an
installed, configured sandboxd service. The reset test also starts a guest and
needs KVM access:

- `make manual-delete-test` checks stopped-sandbox deletion.
- `make manual-reset-test` checks state-disk reset. Set `IMAGE` to select an
  installed image other than `default`.

The `make stage-overlay-image` and `make overlay-proof` targets support the
SquashFS and OverlayFS storage proof. The staged overlay image is for boot
testing. It does not include the configured `dev` account or SSH bootstrap.

## Data and safety

The operator's SSH keys and generated SSH files stay in the user's config,
data, and state directories. Privileged state and installed images stay under
`/var/lib/sandboxctl`.

Each sandbox has its own writable state disk. The base image is shared and
read-only. `sandboxctl reset` removes changes from the writable disk.
`sandboxctl delete` removes the sandbox and its writable disk.

See [docs/SANDBOX-RUNTIME-V2.md](docs/SANDBOX-RUNTIME-V2.md) for the runtime
design. See [docs/PROGRESS.md](docs/PROGRESS.md) for implementation and test
status.
