  # sandboxctl

`sandboxctl` manages local Firecracker development sandboxes. It owns the SSH configuration for those sandboxes.

This repository supports a manual one-VM test. Do not use it with important data.

## Intended result

The completed tool will provide this workflow:

```sh
sandboxctl init
sandboxctl create dev1 --cpus 4 --memory 8192
sandboxctl start dev1
ssh sandboxctl-dev1
```

The user can include the managed configuration in the main SSH configuration.

## Current functions

The scaffold can do these operations:

- Create the private application directories.
- Create a tool-owned Ed25519 SSH key.
- Create a tool-owned SSH configuration and known-hosts file.
- Make a private sparse copy of a prepared root drive for each sandbox.
- Save sandbox records as JSON files.
- Start Firecracker through the Firecracker Go software development kit (SDK) when the TAP device exists.
- Stop the SDK runner and its Firecracker process.
- Open an SSH connection to a recorded sandbox.

The scaffold cannot yet do these operations:

- Download or build a Firecracker guest kernel.
- Build the base root file system.
- Put the SSH public key in a guest.
- Create the host bridge, TAP devices, or NAT rules.
- Remove a sandbox.
- Prevent network-address collisions.

## Build

Install Go 1.27 or a later compatible version. Then run:

```sh
make check
make build
```

The binary will be at `bin/sandboxctl`.

## Initialize and inspect

Run these commands first:

```sh
./bin/sandboxctl init
./bin/sandboxctl paths
./bin/sandboxctl list
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

Do not use the old `download-test-assets` flow for this proof.
That flow selects changing upstream artifacts and creates an ext4 root image.
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
Do not start the sandboxd migration until these checks pass.

## Legacy single-root VM test

This section describes the old single-ext4-root scaffold.
The runtime v2 plan replaces this workflow.

Download the upstream test kernel and Ubuntu filesystem:

    make download-test-assets

The files go under `tmp/config/images/default/`.
The target records the exact upstream artifact URLs and local SHA-256 checksums.
It keeps valid existing files on later runs.
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
- A dev user.
- An OpenSSH server.
- The virtio_net driver and an eth0 network interface.
- The sandboxctl public key in dev's authorized_keys.
- Public key authentication enabled.

Run sandboxctl init.
Run sandboxctl ssh-config and add its printed block to your main SSH configuration.
Mount the prepared guest drive and install the printed public key.
The example assumes the guest account and group are both named dev.
It also assumes that dev has user ID and group ID 1000.
Use the numeric values from your guest image if they differ.

    sudo mkdir -p /mnt/sandboxctl-guest
    sudo mount -o loop /path/to/base.ext4 /mnt/sandboxctl-guest
    sudo install -d -m 0700 /mnt/sandboxctl-guest/home/dev/.ssh
    sudo install -m 0600 \
      "${XDG_DATA_HOME:-$HOME/.local/share}/sandboxctl/keys/client_ed25519.pub" \
      /mnt/sandboxctl-guest/home/dev/.ssh/authorized_keys
    sudo chown -R 1000:1000 /mnt/sandboxctl-guest/home/dev/.ssh
    sudo umount /mnt/sandboxctl-guest

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
    make manual-test NAME=dev1

The script checks Firecracker and KVM access.
It creates the TAP device and sandbox.
It prints the SSH include block when you need to add it to your main SSH configuration.
It waits for you to add the block and accept the guest host key.
It writes a test file, stops and restarts the VM, and checks that the file remains.
It stops the VM and removes the TAP device when the test ends.
The private sandbox disk remains so you can inspect or remove it later.

If the script stops after it creates the sandbox, fix the reported issue and run the same command again.
The script reuses an existing sandbox with the same name.

The test needs Firecracker, sudo access for TAP setup, and read/write access to /dev/kvm.
It does not configure NAT, DNS, guest Internet access, or an image builder.

## Private SSH environment

The tool stores SSH data in these directories:

```text
$XDG_CONFIG_HOME/sandboxctl/ssh_config
$XDG_DATA_HOME/sandboxctl/keys/
$XDG_STATE_HOME/sandboxctl/known_hosts
```

The tool owns `ssh_config`. It replaces this file from the saved sandbox records.
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

Each sandbox has an alias such as `sandboxctl-dev1`.
The file also matches each recorded guest address.
The tool updates the file during `init`, `create`, and `ssh-config`.
It writes the file atomically with mode `0600`.
It does not edit your main SSH configuration.
It keeps keys and known-hosts data in its own directories.

After you prepare and start the guest, use:

```sh
ssh sandboxctl-dev1
herdr --remote sandboxctl-dev1
```

Check the effective configuration without connecting:

```sh
ssh -G sandboxctl-dev1
```


## Documents

- [Implementation specification](docs/SPEC.md)
- [Authoritative runtime v2 design](docs/SANDBOX-RUNTIME-V2.md)
- [Migration progress](docs/PROGRESS.md)
- [Continuation instructions](docs/CONTINUE.md)
- [Design decisions](docs/DECISIONS.md)
- [Instructions for coding agents](AGENTS.md)

## Primary references

- [Firecracker documentation](https://github.com/firecracker-microvm/firecracker/tree/main/docs)
- [Firecracker Go SDK](https://github.com/firecracker-microvm/firecracker-go-sdk)
- [Herdr remote access](https://herdr.dev/docs/persistence-remote/)
- [Herdr machine connections](https://herdr.dev/docs/connecting-machines/)
- [ASD-STE100](https://www.asd-ste100.org/)
