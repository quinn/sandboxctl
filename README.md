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
The package build also creates the pinned builder image and includes it in the
package. The package stores the builder files in `/usr/share/sandboxctl/builder`. Package installation does not import the image
into sandboxd. Podman is a package build dependency. It is not needed to run `sandboxctl` or build guest images after installation.

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

Initialize the user files and import the packaged builder image. Then check the
host:

```sh
sandboxctl init
sandboxctl doctor
```

`sandboxctl ssh-config` prints an OpenSSH `Include` line. Add that line to
`~/.ssh/config` yourself. `sandboxctl` does not edit that file. After you create
a sandbox, check that OpenSSH reads its managed configuration with:

```sh
ssh -G sandbox-NAME
```

## Build and install a guest image

`sandboxctl init` imports the builder image from the Arch package through the
privileged service. It verifies the image files and installs them in the
protected image store. You do not need to build or select the builder image.

Build a guest image with a digest-pinned base image:

```sh
sandboxctl image build dev-image \
  --base-image ubuntu:24.04@sha256:224a1869083a311ef3f13648a154ba79832fbef6364d31493642ca03082da254 \
  --guest-version 24.04
```

The build supports Ubuntu and Debian. It uses the installed `builder` image by
default. Use `--distribution debian` for a Debian base image. The base image
reference uses the image name and an immutable Secure Hash Algorithm 256-bit
(SHA-256) digest from its container registry. The example digest is the pinned
Ubuntu image used by this repository's live build test. The command prints the
digest and install command for the finished image. Run the printed install
command. It reads the image bundle from your user data directory.

```sh
sandboxctl image install dev-image --version sha256:IMAGE_DIGEST
```

The image build needs host network rules from host installation. The builder
VM also needs network access to the image registry and package servers.
The build checks that the guest's distribution ID and version match the
requested values before it publishes the image.

Build a custom image from a local Dockerfile and context:

```sh
sandboxctl image build app-image \
  --context ./image \
  --target runtime \
  --build-arg APP_VERSION=1.2.3 \
  --guest-version 24.04
```

The command uses `./image/Dockerfile` because the context is `./image`. You can
set another file with `--dockerfile`. In Dockerfile mode, the context defaults
to `.`. The Dockerfile must be inside the context. Use `--build-arg` once for
each build argument. BuildKit applies
Docker ignore rules before it sends context files to the builder. It uses
`Dockerfile.dockerignore` when that file exists. Otherwise, it uses
`.dockerignore`.

The Dockerfile must produce a Debian or Ubuntu systemd guest. Pin each external
image source with a SHA-256 digest. The build rejects unpinned sources and
remote `ADD` inputs. It does not support remote contexts, registry
credentials, secret mounts, SSH mounts, or privileged BuildKit options.

BuildKit uses `RUN`, `COPY`, `ADD`, build arguments, and stages to create the
guest filesystem. `ENV`, `USER`, and `WORKDIR` apply during the image build.
They do not set the guest login environment or SSH user. `CMD` and
`ENTRYPOINT` do not control VM startup. The guest init system runs as process
identifier (PID) 1. Required sandboxctl provisioning runs after the selected
stage.

The command writes the image bundle to your user data directory. It prints
the image digest and install command. Install the image before you create a
sandbox from it.

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

- `sandboxctl init` creates user configuration and SSH files. It also installs
  the packaged builder image through sandboxd.
- `sandboxctl doctor` checks host setup, KVM, networking, SSH, installed
  images, and the packaged builder version.
- `sandboxctl host install` installs host configuration and enables the
  socket-activated service.
- `sandboxctl image build` builds a Debian or Ubuntu guest image in a temporary
  virtual machine. It accepts a digest-pinned base image or a local Dockerfile
  and context. It uses the builder image included in the Arch package.
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
- `make manual-dockerfile-build` checks a local Dockerfile build, context
  filtering, build arguments, VM boot, SSH, and state persistence. Edit the
  builder name and digest in the Makefile when the installed test builder
  changes.
- `make manual-dockerfile-rejections` checks invalid Dockerfile builds,
  guest metadata, and context size limits. It confirms that failed builds do
  not publish an image.

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

See [docs/DOCKERFILE-IMAGE-BUILDS.md](docs/DOCKERFILE-IMAGE-BUILDS.md) for
Dockerfile build limits, provenance, and remaining acceptance checks.

## License

Original sandboxctl code is licensed under the GNU Affero General Public
License, version 3 or any later version (AGPL-3.0-or-later). Third-party code
keeps its own license. See [docs/LICENSING.md](docs/LICENSING.md) and
`third_party/licenses/` for details and license texts.

## Use with Herdr

Herdr can connect to a sandbox through its generated OpenSSH alias. First add
the `Include` line from `sandboxctl ssh-config` to `~/.ssh/config`. Create and
start the sandbox. Then attach from your host:

```sh
herdr --remote sandbox-dev1
```

Replace `sandbox-dev1` with the alias for your sandbox. Herdr uses the normal
OpenSSH configuration and authentication for this connection. You can save the
host in Herdr with a label:

```sh
herdr machine add sandbox-dev1 --label dev1
```

Herdr may ask to install its server in the guest when you add the machine.
Approve that step in Herdr. `sandboxctl` does not install Herdr or change its
settings. See the [Herdr guide for connecting machines](https://herdr.dev/docs/connecting-machines/)
for details.
