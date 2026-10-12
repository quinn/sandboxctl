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
The package build reuses the staged builder image when one exists. Otherwise,
it builds the image during package creation. The image is in
`$XDG_DATA_HOME/sandboxctl/images/builder/`. It uses `~/.local/share` when
`XDG_DATA_HOME` is not set. Run `make clean-builder` to remove this image and
force a fresh build. This does not remove an installed builder image. The
package includes the image at `/usr/share/sandboxctl/builder`. Package
installation does not import the image into sandboxd. Podman is a package
build dependency. It is not needed to run `sandboxctl` or build guest images
after installation.

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

Build a guest image from the pinned Ubuntu 24.04 base image:

```sh
sandboxctl image build dev-image
```

When you do not set `--base-image`, `--context`, or `--dockerfile`, the command
uses the pinned Ubuntu 24.04 base image and guest version. The build supports
Ubuntu and Debian. It uses the installed `builder` image by default. Set
`--guest-version` for an explicit base image or Dockerfile build. You can set
another base image with a registry name and an immutable Secure Hash Algorithm
256-bit (SHA-256) digest:

```sh
sandboxctl image build dev-image \
  --base-image ubuntu:24.04@sha256:224a1869083a311ef3f13648a154ba79832fbef6364d31493642ca03082da254 \
  --guest-version 24.04
```

The command installs the finished image by default. Set `--install=false` to
build and stage the image without installing it.

Use this command to install a staged image:

```sh
sandboxctl image install dev-image --version sha256:IMAGE_DIGEST
```

Image builds store user-owned bundles under
`$XDG_DATA_HOME/sandboxctl/images/NAME/DIGEST/`. Image installation reads a
bundle from this path and sends it to `sandboxd`. The default `XDG_DATA_HOME`
is `~/.local/share`. List installed images in the protected store with:

```sh
sandboxctl image list
```

Delete an installed image by name:

```sh
sandboxctl image delete dev-image
```

The command asks for confirmation. Use `--yes` to skip the prompt in a script.
The command refuses to delete an image that a saved sandbox uses. Delete those
sandboxes first. Each image name has one installed version. The command finds
that version for you. It removes the installed image from the protected host
store. It does not remove a staged bundle in the XDG data directory.

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

The command writes the image bundle to your user data directory. It installs
the image by default. Set `--install=false` to defer installation. The command
then prints the install command. Install the image before you create a
sandbox from it.

## Build a Codex guest image

Build the Codex example image:

```sh
sandboxctl image build codex-image \
  --context ./examples/codex \
  --guest-version 24.04
```

The example pins Ubuntu 24.04 and Codex 0.160.0. It verifies the Codex archive
with its release SHA-256 digest. It installs the standalone Linux x86-64
binary, Git, and ripgrep. Node.js is not required. The image contains no
account credentials. To update Codex, change both `CODEX_VERSION` and
`CODEX_SHA256` in the Dockerfile.

Edit `examples/codex/AGENTS.md` to set the base coding rules. Provisioning
copies it from `/etc/skel/.codex/AGENTS.md` to `/home/dev/.codex/AGENTS.md`
when it creates the `dev` account. Codex loads these global rules before
repository rules. See [Codex instruction
discovery](https://developers.openai.com/codex/guides/agents-md).

Create and start a sandbox from this image:

```sh
sandboxctl create agent1 --image codex-image --cpus 2 --memory 4096
sandboxctl start agent1
ssh sandbox-agent1
```

Sign in from the guest terminal:

```sh
codex login --device-auth
codex login status
```

Enable device code login in your ChatGPT security settings first. Open the
printed link in your host browser. Enter the printed code. The login uses
your ChatGPT subscription. See the [Codex authentication
instructions](https://developers.openai.com/codex/auth#login-on-headless-devices).

The login stays on the sandbox's private writable disk. Stop and start keep
it. Reset and delete remove it.

To copy your host login instead, add `--copy-codex-auth` when you start the
sandbox:

```sh
sandboxctl start agent1 --copy-codex-auth
```

The command reads `$CODEX_HOME/auth.json`. It uses `~/.codex/auth.json` when
`CODEX_HOME` is not set. It waits up to two minutes for the guest SSH port and
the transfer. SSH can ask you to accept the guest host key. The command copies
the file to the SSH user's `~/.codex/auth.json`. It replaces any existing guest
login. It sets directory mode `0700` and file mode `0600`. The transfer uses
SSH standard input. Credentials do not enter the daemon API or image.

The flag applies to this start command only. Without the flag, the command
does not read your Codex login. If the host file is missing or the transfer
fails, the command returns an error. The sandbox stays running. The flag
requires file-based Codex credentials. It cannot copy credentials from an
operating system keyring. Do not combine it with `--dry-run`.

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

Delete all sandboxes that use one image with:

```sh
sandboxctl delete --image dev-image
```

The command lists the matching sandboxes and asks for confirmation. It deletes
them only when all are stopped. Stop running sandboxes first. Add `--yes` to
skip the prompt in a script.

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
  and context. It uses the builder image included in the Arch package. It
  installs the finished image by default. Set `--install=false` to stage it only.
- `sandboxctl image list` shows installed image names and versions.
- `sandboxctl image install` imports a verified local image bundle.
- `sandboxctl image delete` removes an installed image that no saved sandbox uses.
- `sandboxctl create`, `start`, `stop`, `reset`, and `delete` manage sandboxes.
  Use `sandboxctl delete --image IMAGE` to delete all stopped sandboxes for an
  image.
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

The end-to-end (E2E) suite exercises the installed command and daemon. Install
the Arch package and configure the host first. Run the suite as the configured
operator:

```sh
make e2e
```

The suite does not install packages or use `sudo`. It uses a private user-data
directory under `tmp/e2e/` and removes test resources after successful checks.
See [e2e/README.md](e2e/README.md) for requirements and details.

You can also run one manual VM check at a time. These targets need KVM access
and an installed, configured `sandboxd` service:

- `make manual-bootstrap-test` checks guest bootstrap and SSH.
- `make manual-builder-test` checks the builder image and its tools.
- `make manual-image-build` checks the guest image build pipeline and stages
  its output bundle.
- `make manual-dockerfile-build` checks a local Dockerfile build, context
  filtering, build arguments, VM boot, SSH, and state persistence.
- `make manual-dockerfile-rejections` checks invalid Dockerfile builds,
  guest metadata, and context size limits. It confirms that failed builds do
  not publish an image.

These manual targets check sandboxd state and disk operations. They need an
installed, configured sandboxd service. The reset test also starts a guest and
needs KVM access:

- `make manual-delete-test` checks stopped-sandbox deletion.
- `make manual-reset-test` checks state-disk reset. Set `IMAGE` to select an
  installed image other than `default`.

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
