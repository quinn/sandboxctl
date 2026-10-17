# sandboxctl

`sandboxctl` creates and runs Linux development sandboxes as Firecracker virtual machines.
Each sandbox has a private writable disk and a read-only base image.
The tool manages the virtual machine, host network, and Secure Shell (SSH) settings.

The host must run Arch Linux on x86-64.
It must have a working Kernel-based Virtual Machine (KVM) device at `/dev/kvm`.
Guest images must use Debian or Ubuntu.

Names in command examples are sample values.
Network addresses are documented product defaults or loopback examples.

## Install and prepare the host

### Install a GitHub release

Replace `OWNER/REPO` with the project repository.

Import the project signing key once:

```sh
curl -fLO https://github.com/OWNER/REPO/releases/latest/download/sandboxctl-release.asc
sudo pacman-key --add ./sandboxctl-release.asc
sudo pacman-key --finger 0C238727A2833F79959610A3C85E0879A4F87FB5
```

Check that the displayed fingerprint is `0C238727A2833F79959610A3C85E0879A4F87FB5`.
Then trust that key locally:

```sh
sudo pacman-key --lsign-key 0C238727A2833F79959610A3C85E0879A4F87FB5
```

Install or upgrade both packages with one command:

```sh
sudo pacman -U \
  https://github.com/OWNER/REPO/releases/latest/download/sandboxctl-x86_64.pkg.tar.zst \
  https://github.com/OWNER/REPO/releases/latest/download/sandboxctl-builder-x86_64.pkg.tar.zst
```

No release version is required in the commands.
The links select published releases and exclude drafts.
Pacman downloads the matching signatures and verifies both packages with the trusted project key.
The application package includes the commands, daemon, Firecracker, and jailer.
The builder package contains the completed builder image.
Install both packages in the same transaction.
The application requires the matching version of the builder package.
Users do not need the image build tools.
Continue with [host configuration](#configure-the-host) after installation.

### Configure the host

Install the host service and network rules:

```sh
sudo sandboxctl host install --operator "$USER"
```

This command adds the operator to the `sandboxctl` group.
Start a new login session if the command changes your group membership.
Routine sandbox commands do not need `sudo`.

Host installation defaults to user and group ID ranges of `200000:65536` and the address pool `172.30.0.0/16`.
Pass `--uid-range`, `--gid-range`, or `--address-pool` to select different values.

Initialize the user files and import the packaged builder image.
Then check the host:

```sh
sandboxctl init
sandboxctl doctor
```

`sandboxctl ssh-config` prints an OpenSSH `Include` line.
Add that line to `~/.ssh/config` yourself.
`sandboxctl` does not edit that file.
After you create a sandbox, check that OpenSSH reads its managed configuration with:

```sh
ssh -G sandbox-NAME
```

The generated `HostKeyAlias` uses the sandbox's instance identifier.
Host-key checks use this identifier for both the sandbox alias and its guest address.
Recreating a sandbox creates a new identity, even when the name or address is reused.
Stopping and starting a sandbox retain its identity and trusted host key.
SSH asks you to trust the host key on the first interactive connection.
After upgrading from address-based host-key checks, connect once to confirm each sandbox's key.
The tool keeps old address entries but does not transfer their trust to an instance identifier.
SSH still rejects a changed host key for the same instance.

## Build and install a guest image

`sandboxctl init` imports the builder image from the builder package through the privileged service.
It verifies the image files and installs them in the protected image store.
You do not need to build or select the builder image.

Build a guest image from the pinned Ubuntu 24.04 base image:

```sh
sandboxctl image build dev-image
```

When you do not set `--base-image`, `--context`, or `--dockerfile`, the command uses the pinned Ubuntu 24.04 base image and guest version.
The build supports Ubuntu and Debian.
It uses the installed `builder` image by default.
Set `--guest-version` for an explicit base image or Dockerfile build.
You can set another base image with a registry name and an immutable Secure Hash Algorithm 256-bit (SHA-256) digest:

```sh
sandboxctl image build dev-image \
  --base-image ubuntu:24.04@sha256:224a1869083a311ef3f13648a154ba79832fbef6364d31493642ca03082da254 \
  --guest-version 24.04
```

The command installs the finished image by default.
Set `--install=false` to build and stage the image without installing it.

Use this command to install a staged image:

```sh
sandboxctl image install dev-image --version sha256:IMAGE_DIGEST
```

Image builds store user-owned bundles under `$XDG_DATA_HOME/sandboxctl/images/NAME/DIGEST/`.
Image installation reads a bundle from this path and sends it to `sandboxd`.
The default `XDG_DATA_HOME` is `~/.local/share`.
List installed images in the protected store with:

```sh
sandboxctl image list
```

Delete an installed image by name:

```sh
sandboxctl image delete dev-image
```

The command asks for confirmation.
Use `--yes` to skip the prompt in a script.
The command refuses to delete an image that a saved sandbox uses.
Delete those sandboxes first.
Each image name has one installed version.
The command finds that version for you.
It removes the installed image from the protected host store.
It does not remove a staged bundle in the XDG data directory.

The image build needs host network rules from host installation.
The builder VM also needs network access to the image registry and package servers.
The build checks that the guest's distribution ID and version match the requested values before it publishes the image.

Build a custom image from a local Dockerfile and context:

```sh
sandboxctl image build app-image \
  --context ./image \
  --target runtime \
  --build-arg APP_VERSION=1.2.3 \
  --guest-version 24.04
```

The command uses `./image/Dockerfile` because the context is `./image`.
You can set another file with `--dockerfile`.
In Dockerfile mode, the context defaults to `.`.
The Dockerfile must be inside the context.
Use `--build-arg` once for each build argument.
BuildKit applies Docker ignore rules before it sends context files to the builder.
It uses `Dockerfile.dockerignore` when that file exists.
Otherwise, it uses `.dockerignore`.

The Dockerfile must produce a Debian or Ubuntu systemd guest.
Pin each external image source with a SHA-256 digest.
The build rejects unpinned sources and remote `ADD` inputs.
It does not support remote contexts, registry credentials, secret mounts, SSH mounts, or privileged BuildKit options.

BuildKit uses `RUN`, `COPY`, `ADD`, build arguments, and stages to create the guest filesystem.
`ENV`, `USER`, and `WORKDIR` apply during the image build.
They do not set the guest login environment or SSH user.
`CMD` and `ENTRYPOINT` do not control VM startup.
The guest init system runs as process identifier (PID) 1.
Required sandboxctl provisioning runs after the selected stage.

The command writes the image bundle to your user data directory.
It installs the image by default.
Set `--install=false` to defer installation.
The command then prints the install command.
Install the image before you create a sandbox from it.

## Build a Codex guest image

The [Codex example folder](examples/codex) demonstrates a guest image with a coding agent.
The example files are in the source repository.
Download or clone the repository to use this example.
Run the following build command from the repository root:

```sh
sandboxctl image build codex-image \
  --context ./examples/codex \
  --guest-version 24.04
```

The example pins Ubuntu 24.04 and Codex 0.160.0.
It uses the [documented standalone installer](https://developers.openai.com/codex/cli/) with an explicit release version.
The installer verifies the native package with the release SHA-256 digest.
It installs the complete Codex package, Git, and ripgrep.
Node.js is not required.
The image stores the package under `/usr/local/lib/codex` and exposes `codex` in `/usr/local/bin`.
The installation-only `CODEX_HOME` setting does not change the guest's login or rules directory.
The image contains no account credentials.
To update Codex, change `CODEX_VERSION` in the Dockerfile.

Edit the example [AGENTS.md](examples/codex/AGENTS.md) to set the base coding rules.
Provisioning copies it from `/etc/skel/.codex/AGENTS.md` to `/home/dev/.codex/AGENTS.md` when it creates the `dev` account.
Codex loads these global rules before repository rules.
See [Codex instruction discovery](https://developers.openai.com/codex/guides/agents-md).

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

Enable device code login in your ChatGPT security settings first.
Open the printed link in your host browser.
Enter the printed code.
The login uses your ChatGPT subscription.
See the [Codex authentication instructions](https://developers.openai.com/codex/auth#login-on-headless-devices).

The login stays on the sandbox's private writable disk.
Stop and start keep it.
Reset and delete remove it.

To copy your host login instead, add `--copy-codex-auth` when you start the sandbox:

```sh
sandboxctl start agent1 --copy-codex-auth
```

The command reads `$CODEX_HOME/auth.json`.
It uses `~/.codex/auth.json` when `CODEX_HOME` is not set.
It waits up to two minutes for the guest SSH port and the transfer.
SSH can ask you to accept the guest host key.
The command copies the file to the SSH user's `~/.codex/auth.json`.
It replaces any existing guest login.
It sets directory mode `0700` and file mode `0600`.
The transfer uses SSH standard input.
Credentials do not enter the daemon API or image.

The flag applies to this start command only.
Without the flag, the command does not read your Codex login.
If the host file is missing or the transfer fails, the command returns an error.
The sandbox stays running.
The flag requires file-based Codex credentials.
It cannot copy credentials from an operating system keyring.
Do not combine it with `--dry-run`.

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

Stop a sandbox to keep its writable state disk.
Reset the disk to remove guest changes while keeping the sandbox and its image.
Delete a stopped sandbox to remove its state disk and release its host resources.

```sh
sandboxctl stop dev1
sandboxctl reset dev1
sandboxctl start dev1
sandboxctl stop dev1
sandboxctl delete dev1
```

Delete asks for confirmation.
Add `--yes` to skip the prompt.
The tool refuses to delete a running sandbox.
After confirmation, delete runs `sandboxctl pull` for an attached repository before it removes the sandbox.
If the pull fails, delete keeps the sandbox and returns an error.
`--yes` skips confirmation but still requires a successful pull.
Delete does not remove the shared image.

Delete all sandboxes that use one image with:

```sh
sandboxctl delete --image dev-image
```

The command lists the matching sandboxes and asks for confirmation.
It deletes them only when all are stopped.
Stop running sandboxes first.
Add `--yes` to skip the prompt in a script.
The command pulls results from all matching task sandboxes before it deletes any of them.
If any pull fails, it keeps all matching sandboxes and returns an error.

The default state disk is 20 GiB.
Set `--disk-size` when you create a sandbox to choose another size.
`--cpus`, `--memory`, and `--image` also have defaults.
Run `sandboxctl help` for command syntax.

Reset creates new guest SSH host keys.
Reset retains the instance identifier.
Verify the new key before you remove the old trusted key.
Get the `hostkeyalias` value from `ssh -G sandbox-NAME`.
Get the known-hosts path from `sandboxctl paths`.
Then remove the old entry with:

```sh
ssh-keygen -R INSTANCE_HOST_KEY_ALIAS -f KNOWN_HOSTS_PATH
```

## Forward a guest port to the host

Use `sandboxctl forward` to reach a guest service through a host port.
The command forwards Transmission Control Protocol (TCP) connections through SSH.
Start the sandbox before you forward a port.
Run the guest service on `127.0.0.1` or `0.0.0.0`.

For a guest service on port 3000, run this command on the host:

```sh
sandboxctl forward dev1 3000
```

The argument selects the guest port.
The command reuses the last assigned host port for this mapping when it is available.
For a new mapping, it first tries the guest port as the host port when that port is at least 1024.
If that host port is in use, it selects another available host port automatically.
The command prints the selected host address and port.
Open that address in your browser when the guest service is a web server.
Connections go to `127.0.0.1:3000` inside `dev1`.
The host listener binds to `127.0.0.1` and is available only on this host.

Set a preferred host port with `HOST_PORT:GUEST_PORT`:

```sh
sandboxctl forward dev1 8080:3000
```

The first port is the preferred host port.
The second port is the guest port.
If port 8080 is in use, the command selects another available host port and prints it.
Use the printed host port to connect.
Host ports range from 1024 to 65535.
Guest ports range from 1 to 65535.

The CLI sends the mapping to `sandboxd` through its Unix socket.
The first registration installs a separate daemon forwarding public key in the guest.
The CLI uses your operator key through native Go SSH for this setup.
It asks for confirmation when the instance host key has not been trusted.
The daemon keeps its private forwarding key in the sandbox's root-owned directory.
The guest key permits only local TCP forwarding to loopback ports.
It cannot start a shell, run a command, or open reverse forwarding.
The daemon creates and manages the SSH connection with Go's `golang.org/x/crypto/ssh` package.
It checks the sandbox instance's trusted host key.
Native SSH uses OpenSSH's host-key preference order for algorithms supported by Go.
Algorithms for trusted keys come first.
The daemon verifies the selected key against its saved pin.
It listens on the selected host port and forwards each connection through SSH to the guest port.
The command prints the selected endpoint and returns.
Forwarding continues after you close the terminal.
The guest service can start after the forward is configured.
Use `--list` to check whether the SSH connection is active.
The daemon retries interrupted connections.
Each sandbox supports up to 32 mappings and 128 active connections.

List the saved mappings and their current state with:

```sh
sandboxctl forward dev1 --list
```

Remove a mapping by its guest port with:

```sh
sandboxctl forward dev1 --remove 3000
```

Removing a mapping closes its listener and active forwarded connections.

### Saved port mappings

The daemon saves mappings in the sandbox's root-owned instance record at `/var/lib/sandboxctl/instances/NAME/instance.json`.
Each mapping contains the guest port, preferred host port when supplied, and last assigned host port.
The record's instance identifier binds the mappings to that sandbox.
The file uses mode `0600`.
The instance directory uses mode `0700`.
The daemon writes the record atomically.

Later starts reuse the saved host port when it is available.
A new preferred host port replaces the saved preference.
If the saved port is in use, the daemon selects another port and updates the record.
Stop closes listeners and forwarded connections while preserving the mappings.
Start restores saved forwards automatically.
A daemon restart restores forwards only for running sandboxes whose process identity has been verified.
Recreating a sandbox gives it a new instance identifier and a new mapping record.
Reset preserves mappings and clears the daemon's trusted host key.
Verify the new fingerprint before you remove the old instance entry from the managed known-hosts file.
Run `sandboxctl forward NAME GUEST_PORT` to trust the new key and reinstall forwarding access.
Use `--replace-host-key` when you must replace an existing daemon host-key pin.
This option still requires successful verification against the operator's managed known-hosts file.
Delete removes the sandbox's saved mappings.

## Share code with a sandbox

Each sandbox holds one coding task.
The sandbox name is also the Git branch name and Git remote name.
Run `sandboxctl create` or `sandboxctl start` from your host repository to attach it to the sandbox.
Code setup runs as part of these commands.
Git transfers use the generated OpenSSH alias and the managed SSH key.
The host starts the transfer connections.
The guest does not need SSH access to the host or credentials for a remote Git service.

### Create and start a task sandbox

Commit the code that you want the agent to use.
Create a sandbox with a name that identifies the task:

```sh
sandboxctl create fix-parser --image codex-image
sandboxctl start fix-parser --copy-codex-auth
```

Create records the host repository and starting commit.
It creates the host branch `fix-parser` at that commit without changing your current branch or working files.
It creates host Git remote `fix-parser` for the guest's bare repository.
Start boots the guest and waits for SSH readiness.
It creates the guest's bare Git repository and pushes the starting commit to branch `fix-parser`.
A bare repository stores commits and branches without a working directory.
Start creates a guest working directory and checks out branch `fix-parser`.
It configures Git remote `fix-parser` in the guest working directory.
It prints the guest working directory path.
The guest repository uses your host Git author name and email address.
If these values are not configured, it uses `Sandbox agent <dev@sandbox.local>`.

| Setting | Value for this sandbox |
| --- | --- |
| Sandbox name | `fix-parser` |
| Git branch | `fix-parser` |
| Git remote | `fix-parser` |
| SSH alias | `sandbox-fix-parser` |
| Guest bare repository | `/home/dev/repos/fix-parser.git` |
| Guest working directory | `/home/dev/work/fix-parser` |

If the sandbox was created outside a Git repository, run its first start from the repository that you want to attach.
Start performs the same host setup when it first attaches a repository.
Later starts use the saved repository association and preserve the guest's existing commits and working files.
Create a separate sandbox for each independent task.
The commands reject an existing branch or remote with the same name unless it belongs to this sandbox.

The transfer includes committed files and their Git history.
It does not include uncommitted edits or untracked files.
It does not copy host Git credentials or hooks.
The host repository and guest working directory remain separate.

### Publish the agent's work

Connect to the guest working directory:

```sh
ssh sandbox-fix-parser
cd /home/dev/work/fix-parser
codex
```

Have the agent run the project checks and commit its changes on `fix-parser`.
Publish the commits to the guest's bare repository:

```sh
git push fix-parser fix-parser
```

This push uses a local guest path.
It does not connect to the host.
Uncommitted changes remain in the guest working directory until the agent commits them.

### Pull and review results

Pull the sandbox's results from the host:

```sh
sandboxctl pull fix-parser
```

The command uses the saved host repository association.
It publishes the guest branch `fix-parser` to the guest's bare repository and fetches it through host remote `fix-parser`.
It includes committed guest work even when the agent has not pushed it yet.
It prints the starting commit, result commit, and change summary.
Git stores the fetched result as `fix-parser/fix-parser`.
The first name identifies the remote.
The second name identifies the branch.
Pull does not check out a host branch or merge the result into your current branch.
It also saves `/REPORT.md` under `$XDG_STATE_HOME/sandboxctl/reports/fix-parser/` when the guest report exists.
It uses mode `0700` for the report directory and mode `0600` for the report file.
The saved report is `$XDG_STATE_HOME/sandboxctl/reports/fix-parser/REPORT.md`.
Reports must be smaller than 4 MiB.

Pull waits for guest SSH readiness.
If the sandbox is stopped, pull starts it temporarily and stops it after the transfer.
If the sandbox is running, pull leaves it running.
The command refuses to report success when the guest working directory has uncommitted edits or untracked files.
Commit the required files before you pull.
Pull does not create commits or discard guest changes.
Each task transfer has a ten-minute deadline.

Review the commits and diff:

```sh
git log HEAD..fix-parser/fix-parser
git diff HEAD...fix-parser/fix-parser
```

Apply the reviewed commits when your current branch can advance directly to the result:

```sh
git merge --ff-only fix-parser/fix-parser
```

If your host branch has diverged, use a normal merge or cherry-pick after you review the changes.

### Delete a task sandbox

Stop the sandbox and request deletion:

```sh
sandboxctl stop fix-parser
sandboxctl delete fix-parser
```

After you confirm deletion, the command runs the same pull flow before it removes the sandbox.
It temporarily starts the stopped guest to retrieve the commits and report.
It stops the guest before it deletes the state disk.
If startup, pull, or shutdown fails, deletion stops and the sandbox remains available for recovery.
Deletion keeps the host branch, Git remote, fetched results, and saved report.
Sandboxes without an attached repository do not require a Git pull.

The guest repository and working directory stay on the sandbox's private writable disk.
Stop and start keep them.
Reset and delete remove them.
Run `sandboxctl pull` before you reset a task sandbox.
Delete pulls the results automatically.

## Commands

- `sandboxctl init` creates user configuration and SSH files.
  It also installs the packaged builder image through sandboxd.
- `sandboxctl doctor` checks host setup, KVM, networking, SSH, installed images, and the packaged builder version.
- `sandboxctl host install` installs host configuration and enables the socket-activated service.
- `sandboxctl image build` builds a Debian or Ubuntu guest image in a temporary virtual machine.
  It accepts a digest-pinned base image or a local Dockerfile and context.
  It uses the builder image included in the Arch package.
  It installs the finished image by default.
  Set `--install=false` to stage it only.
- `sandboxctl image list` shows installed image names and versions.
- `sandboxctl image install` imports a verified local image bundle.
- `sandboxctl image delete` removes an installed image that no saved sandbox uses.
- `sandboxctl create`, `start`, `stop`, `reset`, and `delete` manage sandboxes.
  Use `sandboxctl delete --image IMAGE` to delete all stopped sandboxes for an image.
  Delete pulls task results before it removes a sandbox.
- `sandboxctl pull` retrieves a sandbox's committed code and report into the saved host repository and state directory.
  It preserves the host working files and restores a temporarily started sandbox to the stopped state.
- `sandboxctl list` shows sandbox state.
- `sandboxctl ssh` runs a command through the generated SSH alias.
- `sandboxctl forward NAME [HOST_PORT:]GUEST_PORT` forwards a guest TCP port to the host loopback address.
  It selects another host port automatically when the preferred port is in use.
  The daemon manages the connection after the command returns.
  Use `--list` to show mappings and `--remove GUEST_PORT` to remove one.
- `sandboxctl ssh-config` prints the SSH `Include` instruction and refreshes the managed SSH file.
- `sandboxctl paths` prints the user and host data paths.
- `sandboxctl version` prints the version.

## Troubleshooting

### Check the host and installed images

```sh
sandboxctl doctor
sandboxctl image list
```

The builder package supplies the builder bundle.
`sandboxctl init` verifies and imports it into the daemon's image store.
The package build cache is separate from installed images.

### Debug logs

Command dispatch, start, completion, and failure diagnostics use the debug log level.
Set `DEBUG=true` to print these diagnostics:

```sh
DEBUG=true sandboxctl list
```

These diagnostics are hidden when `DEBUG` is unset or has another value.
Command output and error messages remain visible.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for source builds, local installation, and development checks.
See the [package guide](packaging/local/README.md) for release builds and builder cache management.

## Data and safety

The operator's SSH keys and generated SSH files stay in the user's config, data, and state directories.
Privileged state and installed images stay under `/var/lib/sandboxctl`.
Git task associations stay under `$XDG_STATE_HOME/sandboxctl/tasks/`.
They record the host repository, starting commit, and sandbox identity.
Git and SSH task operations run as the operator.
They do not send host repository paths to sandboxd.

Each sandbox has its own writable state disk.
The base image is shared and read-only.
`sandboxctl reset` removes changes from the writable disk.
`sandboxctl delete` removes the sandbox and its writable disk.

See [docs/SANDBOX-RUNTIME-V2.md](docs/SANDBOX-RUNTIME-V2.md) for the runtime design.
See [docs/PROGRESS.md](docs/PROGRESS.md) for implementation and test status.

See [docs/DOCKERFILE-IMAGE-BUILDS.md](docs/DOCKERFILE-IMAGE-BUILDS.md) for Dockerfile build limits, provenance, and remaining acceptance checks.

## License

Original sandboxctl code is licensed under the GNU Affero General Public License, version 3 or any later version (AGPL-3.0-or-later).
Third-party code keeps its own license.
See [docs/LICENSING.md](docs/LICENSING.md) and `third_party/licenses/` for details and license texts.

## Use with Herdr

Herdr can connect to a sandbox through its generated OpenSSH alias.
First add the `Include` line from `sandboxctl ssh-config` to `~/.ssh/config`.
Create and start the sandbox.
Then attach from your host:

```sh
herdr --remote sandbox-dev1
```

Replace `sandbox-dev1` with the alias for your sandbox.
Herdr uses the normal OpenSSH configuration and authentication for this connection.
You can save the host in Herdr with a label:

```sh
herdr machine add sandbox-dev1 --label dev1
```

Herdr may ask to install its server in the guest when you add the machine.
Approve that step in Herdr.
`sandboxctl` does not install Herdr or change its settings.
See the [Herdr guide for connecting machines](https://herdr.dev/docs/connecting-machines/) for details.
