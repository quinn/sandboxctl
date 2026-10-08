  # sandboxctl

`sandboxctl` manages local Firecracker development sandboxes. It owns the SSH configuration for those sandboxes.

This repository supports a manual one-VM test. Do not use it with important data.

## Intended result

The completed tool will provide this workflow:

```sh
sandboxctl init
sandboxctl image install default --version sha256:DIGEST
sandboxctl create dev1 --cpus 4 --memory 8192
sandboxctl start dev1
ssh sandbox-dev1
```

The user can include the managed configuration in the main SSH configuration.

## Current development functions

The current implementation can do these operations:

- Create the private application directories.
- Create a tool-owned Ed25519 SSH key.
- Create a tool-owned SSH configuration and known-hosts file.
- Save privileged instance records under `/var/lib/sandboxctl`.
- Allocate a unique host identity, `/30` network, and TAP name for each instance.
- Create a sparse ext4 state disk for a stopped instance.
- Generate the OpenSSH configuration fragment from daemon state.
- Create and configure a per-instance TAP, then start and stop a jailed VM through sandboxd.

The create operation needs an installed image manifest and the host service.
Image installation uses a verified local image bundle.

The current implementation cannot yet do these operations:

- Build a base image.
- Configure routed guest networking and firewall rules.
- Delete or reset a sandbox.

The jailed runtime has unit tests. It has not passed a live boot test on a KVM host.

## Build

Install Go 1.27 or a later compatible version. Then run:

```sh
make check
make build
```

The build creates `bin/sandboxctl`, `bin/sandboxd`, and one `bin/sandboxctl-*`
executable for each CLI command. Use the Make target to run the local CLI:

```sh
make sandboxctl ARGS="help"
```

Build and install a local Arch package:

```sh
make install
```

This target uses `yay` to build and install the current checkout. The checkout
must have a Git remote. The local development remote is the bare repository
`../sandboxctl-remote.git`, configured as `local`. Push commits to it before
running this target. This target does not use GitHub. The AUR draft in
`packaging/aur` uses the configured GitHub `main` branch.

## Initialize and inspect

Run these commands first:

```sh
make sandboxctl ARGS="init"
make sandboxctl ARGS="paths"
make sandboxctl ARGS="list"
```

## Phase 1 storage proof

The proof uses the x86-64 kernel and Ubuntu 24.04 SquashFS artifacts from
Firecracker CI v1.15. The asset target uses fixed source URLs. It records
SHA-256 checksums for the downloaded files.

Download the assets with:

    make download-overlay-assets

The files go to these paths:

    tmp/config/images/default/vmlinux
    tmp/config/images/default/ubuntu.squashfs

The target verifies the checksums on later runs. Move the files and checksum
manifest out of the way if you want to download a fresh copy.

Do not use `download-test-assets` for this proof.
That target selects changing upstream artifacts and creates an ext4 root image.
The v2 design requires pinned image digests and a read-only SquashFS base.

Build the initramfs and create a state disk:

    make overlay-initramfs
    make overlay-state-disk

Run the proof on a Linux host with Firecracker and read/write access to `/dev/kvm`:

    make overlay-proof

This target also downloads the pinned assets when they are missing.

The serial output reports whether the overlay created or retained its test marker.
Stop the VM after the first run.
Run `make overlay-proof` again and confirm that the output reports a retained marker.
Stop the VM again.
Run `make overlay-state-reset` and type `RESET` at the prompt.
Run `make overlay-proof` and confirm that the output reports a new marker.

The proof checks the SquashFS digest after each VM run.
Do not start sandboxd implementation until these checks pass.

## Import the Phase 1 image

The image import command reads a local image bundle from the XDG data directory.
Build the bundle with:

```sh
make stage-overlay-image
```

The target stages the verified kernel and SquashFS with the project initramfs.
It writes the bundle under `~/.local/share/sandboxctl/images/default/VERSION`.
It prints the pinned version and the install command.
The bundle uses the Phase 1 guest image. It is for serial boot tests.
It does not provide the `dev` user or SSH setup.

The install command sends the manifest and artifacts to sandboxd.
The CLI verifies each artifact before it sends the bytes.
The daemon verifies the sizes and SHA-256 digests again.
The daemon publishes the image only after all checks pass.
The daemon stores the image under `/var/lib/sandboxctl/images/default`.

## Temporary single-root VM test

This test uses a single ext4 root drive with the current development runner.
It does not test the SquashFS and OverlayFS runtime design.

Download the upstream test kernel and Ubuntu filesystem:

    make download-test-assets

The files go under `tmp/config/images/default/`.
The target records the exact upstream artifact URLs and local SHA-256 checksums.
It keeps valid existing files on later runs.
If the directory contains a valid kernel and SquashFS but no `root.ext4`, the target builds the ext4 root drive from the cached SquashFS. It does not download or replace the source files in this case.
Run `make download-test-assets REFRESH=1` to select the latest upstream artifacts again.
Install `curl`, `python3`, `squashfs-tools`, and `e2fsprogs` first.
The target uses `sudo` to preserve guest filesystem ownership while it extracts and converts the image.
It keeps the resulting image owned by your user with mode `0600`.

The kernel is ready to use as a Firecracker kernel.
The ext4 image is a converted copy of Firecracker's Ubuntu example filesystem.
It may need guest account, SSH, key, and network setup before it meets the requirements below.
The manifest records the selected artifact version and source URLs.

Prepare a Linux guest image before you create a sandbox. It must have:

- A root file system on /dev/vda.
- The `ubuntu` user from the downloaded test image.
- An OpenSSH server.
- The virtio_net driver and an eth0 network interface.
- The sandboxctl public key in `ubuntu`'s authorized_keys.
- Public key authentication enabled.

The downloaded Ubuntu test image uses UID and GID 1000 for `ubuntu`.
The test command prepares the sandbox's private disk before it starts the VM. It assigns the locked account an unknown random password hash so Pluggable Authentication Modules (PAM) can accept public-key logins. It disables password and keyboard-interactive login in the guest. It adds the tool-owned public key without removing other authorized keys, then validates the OpenSSH server configuration. This step uses `sudo` to mount the private disk and bind-mount `/dev` for guest tools. It does not change the base image.

Enable the guest SSH server in the image.
Do not mount or modify the source drive while the VM runs.
The tool copies the source drive to a private instance drive.
The tool never starts the source drive.

Check that 172.30.0.0/24 is free on the host.
Use a disposable root drive for this test.
The script creates one TAP device and removes it after the VM stops.
The script creates a private sandbox disk and keeps it after the test.
The script does not set up guest Internet access.

Run the test with the downloaded or prepared kernel and root drive:

    make download-test-assets
    make manual-test

The default sandbox name is `manualtest`. Pass `NAME=dev1` to use another name.
The command saves the complete test output to `tmp/logs/manual-test.log` with mode `0600`.
The log includes the final pass or failure message.

The script checks Firecracker and KVM access.
It creates the TAP device and sandbox with SSH user `ubuntu`.
It prints the SSH include block when you need to add it to your main SSH configuration.
It waits for you to add the block and accept the guest host key.
It writes a test file, stops and restarts the VM, and checks that the file remains.
It stops the VM and removes the TAP device when the test ends.
The private sandbox disk remains so you can inspect or remove it later.

If the script stops after it creates the sandbox, fix the reported issue and run the same command again.
The script reuses an existing sandbox with the same name. It does not replace that sandbox's private disk when you change the base image. Use a new name to copy the updated base image.

The test needs Firecracker, sudo access for TAP setup, and read/write access to /dev/kvm.
It does not configure NAT, DNS, guest Internet access, or an image builder.

## Private SSH environment

The tool stores SSH data in these directories:

```text
$XDG_CONFIG_HOME/sandboxctl/ssh_config
$XDG_DATA_HOME/sandboxctl/keys/
$XDG_STATE_HOME/sandboxctl/known_hosts
```

The tool owns `ssh_config`. It replaces this file from daemon-owned sandbox records.
Do not edit this file.

Run this command to initialize SSH and print the include block:

```sh
sandboxctl ssh-config
```

Append the printed block to your main SSH configuration yourself.
With the default paths, the block is:

```sshconfig
Host *
    Include "/home/quinn/.config/sandboxctl/ssh_config"
```

Use the path printed by the command. The path follows `XDG_CONFIG_HOME`.
The `Host *` line ends any previous host section for the include.
Earlier matching settings can take precedence over the included settings.
Place the block before general defaults if those defaults cause a conflict.

Each sandbox has an alias such as `sandbox-dev1`.
The file also matches each recorded guest address.
The tool updates the file during `create` and `ssh-config`.
It writes the file atomically with mode `0600`.
It does not edit your main SSH configuration.
It keeps keys and known-hosts data in its own directories.

After you prepare and start the guest, use:

```sh
ssh sandbox-dev1
```

Check the effective configuration without connecting:

```sh
ssh -G sandbox-dev1
```


## Documents

- [Implementation specification](docs/SPEC.md)
- [Authoritative runtime v2 design](docs/SANDBOX-RUNTIME-V2.md)
- [Implementation progress](docs/PROGRESS.md)
- [Continuation instructions](docs/CONTINUE.md)
- [Design decisions](docs/DECISIONS.md)
- [Instructions for coding agents](AGENTS.md)

## Primary references

- [Firecracker documentation](https://github.com/firecracker-microvm/firecracker/tree/main/docs)
- [Firecracker Go SDK](https://github.com/firecracker-microvm/firecracker-go-sdk)
- [Herdr remote access](https://herdr.dev/docs/persistence-remote/)
- [Herdr machine connections](https://herdr.dev/docs/connecting-machines/)
- [ASD-STE100](https://www.asd-ste100.org/)
