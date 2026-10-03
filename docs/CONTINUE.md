# Continuation instructions

Follow the milestones in this file. Complete each milestone before you start the next milestone.

## Current SSH approach

The tool owns one SSH configuration file in its XDG configuration directory.
The user can manually include that file in the main SSH configuration.
Run `sandboxctl ssh-config` to refresh the file and print the include block.
Use `ssh sandboxctl-NAME` to connect after preparing the guest.
Use ordinary Herdr remote attach through the same alias.

Milestone 1 now includes the managed SSH configuration tests.
Complete it before image and network work.

## Prepare the development host

Install these packages on Arch Linux:

```sh
sudo pacman -S --needed go firecracker openssh nftables iproute2 e2fsprogs
```

Build `firectl` from its source until an Arch package is selected:

```sh
git clone https://github.com/firecracker-microvm/firectl.git
cd firectl
go build ./...
```

Put the `firectl` executable in a directory in `PATH`.

Confirm KVM access:

```sh
test -r /dev/kvm -a -w /dev/kvm
```

## Run the initial checks

Run:

```sh
make check
make build
./bin/sandboxctl help
./bin/sandboxctl init
./bin/sandboxctl paths
```

The current environment passes `go vet ./...` and `go test ./...`.

## Milestone 1: correct the scaffold

Complete these tasks first:

1. Run all current tests. **Complete.**
2. Correct all compile errors and test errors. **Complete.**
3. Add tests for XDG path resolution. **Complete.**
4. Add tests for atomic instance writes. **Complete.**
5. Add a process-start test with a fake `firectl` executable. **Complete.**
6. Add a process-stop test. **Complete.**
7. Add a test for stale PID files. **Complete.**
8. Test managed SSH configuration generation. **Complete.**
9. Test the manual include with sandbox aliases and unrelated hosts. **Complete.**

Exit condition:

```sh
make check
```

This command must complete without an error.

## Milestone 2: base image

Create a reproducible base-image builder.

The image must contain:

- A supported Linux distribution.
- An OpenSSH server.
- A non-root `dev` user.
- `sudo` with a documented policy.
- A first-boot `sandboxctl` service.
- CA certificates.
- Git.
- A supported Herdr installation path.

The first-boot service must read Firecracker MicroVM Metadata Service (MMDS) data. It must install the authorized SSH key. It must set the host name.

Do not download an unverified image. Pin each source and verify its checksum.

Exit condition:

- A clean build produces the same image contents.
- A microVM starts from the image.
- The `dev` user accepts only the supplied key.
- Password authentication is off.

## Milestone 3: network helper

Create the bridge, TAP devices, routes, and nftables rules.

The helper must be idempotent. It must not remove rules that another application owns.

Replace the hash-based address in the scaffold. Use an allocation registry. Detect address and TAP-name conflicts.

Exit condition:

- Two sandboxes can run at the same time.
- Each sandbox has a different address and TAP device.
- The host can SSH to each sandbox.
- Each sandbox can reach the Internet.
- One sandbox cannot use the other sandbox's root drive.

## Milestone 4: image and key provisioning

Change `sandboxctl create` to accept a base image name. Do not require raw kernel and root-drive paths for normal use.

Create a private writable root drive for each sandbox. Supply the public SSH key and host name through MMDS.

Wait for SSH readiness after start. Use a bounded timeout. Show a useful error if boot fails.

Exit condition:

```sh
sandboxctl create dev1 --cpus 4 --memory 8192
sandboxctl start dev1
sandboxctl ssh dev1 -- uname -a
```

All commands must work without changes to `~/.ssh`.

## Milestone 5: safe deletion

Add:

```sh
sandboxctl delete NAME
```

The command must refuse to delete a running sandbox. It must require confirmation unless `--yes` is present.

Delete only files inside the selected managed instance directory. Never delete the base image, kernel, SSH key, or unrelated log files.

## Milestone 6: packaging

Create an Arch `PKGBUILD`. Add shell completion for Bash, Zsh, and Fish.

Do not add a system service requirement. An optional service can prepare the host network.

## Handoff format

Use this format when work stops:

```text
Completed:
- ...

Checks:
- ...

Open problems:
- ...

Next task:
- ...
```
