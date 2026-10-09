# Implementation progress

Last updated: 2026-10-09

## Authority and order

`docs/SANDBOX-RUNTIME-V2.md` is the authoritative runtime design.
The risk-first storage proof must pass before sandboxd work starts.
Section 18 now records this gate.

## Current phase

The live guest bootstrap test passed on 2026-10-09. It confirmed the completion marker, runtime hostname, and public-key SSH login on first boot and after stopping and restarting the same VM. The SSH login after restart also confirmed that the guest host key remained stable. The test log is `tmp/logs/guest-bootstrap-test.log`.

The pinned builder seed image stages successfully. Its first live boot exposed an interactive `systemd-firstboot` prompt. Guest bootstrap completed, but the prompt blocked normal startup and SSH. The image recipe now masks `systemd-firstboot.service`. The rebuilt bundle version is `sha256:7792cf290cecc7d11550793d51f81b67336c251e6621f39a3df8889e921fe766`; it still needs a live boot test. The next test run must stop the failed VM `builder-1791557469-643214` first. `make manual-builder-test` now uses a unique image name for every run because the daemon does not replace installed images.

Added private guest serial capture at `/var/lib/sandboxctl/instances/NAME/console.log`. The log is limited to 4 MiB and uses mode `0600`. Added `slog` events for CLI command and daemon RPC lifecycles. The guest bootstrap logs each setup stage without logging the key. The first metadata lookup failed because the daemon stored metadata at the MMDS root while the guest requested `/latest/meta-data/`. The daemon now nests the document under `latest/meta-data`, and a unit test checks this tree shape. The bootstrap service sends its logs to the serial console. The latest live run confirms that metadata lookup now succeeds and locates the next blocker at hostname setup.

The Phase 1 storage proof passed. Section 18 steps 1 through 3 are complete. Step 4 host installation and doctor support are implemented and still need final integration checks on a disposable Arch host.

Section 18, step 6 is complete. Stopped-instance creation now uses the daemon-owned allocator and store. sandboxd reads host allocation settings and image versions from root-owned state. It creates the sparse ext4 disk, stores the SSH public key, and commits a version-2 instance record. The `create` command sends operation data through gRPC and refreshes the SSH fragment from daemon state. Image installation now imports a pinned local bundle through a bounded gRPC stream. The remaining SSH command still uses operator XDG records. Keep the temporary single-root test until the jailed runtime passes integration testing.

Section 18, step 7 now has start and stop RPCs and a sandboxd runtime supervisor. The supervisor starts the SDK machine through the jailer, checks the Firecracker process UID and GID, saves process identity before it returns, and cleans up after exit. Stop requests use the Firecracker shutdown API. The explicit `--force` option sends SIGKILL after checking the saved process identity. The daemon can inspect saved process identity after a restart. Unit tests cover start, stop, force stop, failed start, and a saved process record.

The supervisor resolves fixed image artifacts from a root-owned image directory. It requires a pinned manifest version and read-only kernel, initramfs, and SquashFS files. Runtime records use mode `0600` and are removed with a directory sync. The CLI start and stop commands now call the daemon RPCs. They do not use the temporary runner for normal operation.

The jailed runtime prepares an instance TAP before Firecracker starts. It assigns the allocated UID and GID, applies the host `/30` address, and brings the link up. It removes the TAP after the VM stops and preserves runtime state if TAP cleanup fails. The user confirmed that jailed Firecracker starts the guest and that SSH reaches the guest at its allocated address. The new bootstrap path is intended to install the per-instance key before SSH starts; this remains unverified on a live guest. Guest Internet routing, forwarding, and firewall rules are not implemented.

The E2B runtime checkout is retained in ignored `tmp/e2b-runtime` at commit `47096f195aca4a545a9079b706efb3a18f51cf3f`. The license, extraction record, and first Debian/Ubuntu provisioning profile are in place. There is no build command, BuildKit VM orchestration, image manifest, or SquashFS finalizer yet.

Phase 1 storage proof is complete. It booted with read-only `/dev/vda` and writable `/dev/vdb`. A guest test file persisted across restart and disappeared after state-disk reset. The proof runner checked the SquashFS digest on each run.

## Current manual test report

Iteration 1, the manual one-VM test, passed on 2026-10-05.
The `manualtest` guest booted, accepted the managed SSH key as `ubuntu`, and retained a marker under `/home/ubuntu` after a stop and restart.
The complete output is in the ignored file `tmp/logs/manual-test.log`.

The manual test command prepares the private disk before boot. It fixes guest home ownership, installs the public key, disables password and keyboard-interactive SSH, and validates the SSH server configuration. It waits for SSH on both boots. It checks persistence outside `/tmp`, which is cleared at boot.

The current staged test bundle uses the upstream Ubuntu SquashFS without running the image provisioning script. The initramfs now installs the fixed bootstrap program and its systemd unit into the writable OverlayFS before it starts the guest. The bootstrap replaces image host keys on first boot and preserves generated keys on later boots. This path is only a test bridge; it does not replace the planned BuildKit image builder.

## Completed

- The project uses the Firecracker Go SDK v1.0.0.
- The SDK runtime is behind an application-facing `runtime.Backend` interface.
- The current Go project passed `go vet ./...`, `go test ./...`, and `go build ./...` after the baseline interface change and Phase 1 tooling.
- The required `gofmt`, `go vet ./...`, and `go test ./...` checks pass after the latest changes.
- `bash -n` passes for the Phase 1 scripts and the manual test scripts.
- `git diff --check` passes.
- Firecracker and jailer v1.17.0 are installed in the current development environment.
- Added `cmd/overlay-init` to mount SquashFS read-only, mount ext4 state read-write, assemble OverlayFS, and switch to the guest init system.
- Added `cmd/overlay-proof` to start a temporary SDK VM with SquashFS as `/dev/vda` and ext4 state as `/dev/vdb`.
- The initramfs builder passed archive validation.
- The initramfs archive contains the expected `init` executable and passes `gzip -t`.
- Added `make download-overlay-assets` for fixed Firecracker CI v1.15 sources: x86-64 Linux 6.1.155 and Ubuntu 24.04 SquashFS.
- The overlay asset target records SHA-256 checksums and verifies them on later runs. It does not extract or convert the SquashFS image.
- The generated ext4 state image passed `e2fsck -fn` and uses mode `0600` in a mode `0700` directory.
- Unit tests cover drive access modes, mount layout, marker persistence, replacement state, and invalid marker data.
- The first guest boot reached the overlay and wrote its marker, then exposed the expected initramfs handoff defect: `pivot_root` returned `EINVAL` because the kernel's initramfs root cannot be detached with `pivot_root`.
- Replaced that handoff with the initramfs `switch_root` sequence: move the overlay mount over `/`, then `chroot` into it. The user must rebuild and rerun the VM proof to validate this change.
- Updated the proof runner to stop Firecracker if the guest does not stop after Ctrl-C.
- The user confirmed that the updated VM boot reaches the guest root prompt.
- The guest reports `/` as OverlayFS. A guest test file persisted across restart and disappeared after state-disk reset.
- Added a Protocol Buffers schema in `proto/control.proto`. Its protobuf package and generated Go package do not include `v1`.
- Added Buf v2 lint and generation configuration with pinned Go and gRPC plugins.
- Set Go 1.27 as the minimum version in `go.mod` and the README build instructions.
- Pinned gRPC v1.70.0 and compatible protobuf dependencies. Kept the Go module requirement at 1.27.0.
- Buf lint passes with explicit exceptions for the unversioned protobuf package and its root-level schema file.
- Added `cmd/sandboxd`, a Unix socket gRPC server, and a client package.
- Added API-version and request-ID metadata interceptors. The client creates a request ID for each call. The server validates the metadata and returns the request ID in response metadata.
- Added separate `Ping` and `HostStatus` RPCs. The schema has no negotiation RPC or versioned Go package path.
- Added message-size limits and operation deadlines. The socket server refuses to replace an existing path and uses mode `0600` for its socket.
- Added Unix socket client/server tests for both RPCs, request ID response metadata, invalid metadata, and socket permissions.
- Removed the thin client RPC wrappers. `client.Dial` now returns the gRPC connection and generated service client; callers set RPC deadlines.
- Added `sandboxd.socket` and `sandboxd.service` assets. The daemon can accept the systemd-passed Unix socket.
- Added activation checks for the process ID, descriptor count, and Unix socket type.
- `make check` passes after adding the systemd activation path.
- `systemd-analyze verify` passes for both units against a temporary install tree with the daemon binary and required systemd target units.
- Added `sandboxctl host install --operator USER` and `sandboxctl doctor` entry points.
- Moved command dispatch into `cmd/sandboxctl` and gave each CLI subcommand a package under `cmd/sandboxctl/<command>`.
- Kept reusable sandbox operations in `internal/app`; top-level dispatch and simple command validation live in the command packages.
- Moved CLI argument parsing and user-input validation into the matching `cmd/sandboxctl/<command>` package. Shared parsers and validators live in `internal/cmdutil`.
- Changed internal application and host-install APIs to accept typed options instead of command-line argument slices and raw range/address strings.
- Replaced the `sandboxctl` command switch with generic PATH-based dispatch to `sandboxctl-<command>` executables. Each command directory builds as a standalone `main` package. Nested `host install` dispatch uses `sandboxctl-host` and `sandboxctl-host-install`.
- Changed the detached runner to start the `sandboxctl-vmrun` executable through `PATH`. The `sandboxd` binary remains independent.
- Updated `make build` and the AUR package to build and install every `sandboxctl-*` helper.
- The external-dispatch tests confirm PATH discovery, unchanged command arguments, environment and working-directory inheritance, standard input/output/error, and child exit status.
- `make check`, `make build`, command dispatch smoke tests, shell syntax checks, and `git diff --check` pass.
- The host-install command validates the operator, identifier ranges, and address pool before calling the host installer.
- The host installer checks the Arch host, host-file range conflicts, route conflicts, package files, and matching Firecracker/jailer versions before it changes host state.
- The host installer creates the trusted group, root-owned state directories, host configuration, dedicated nftables table, IPv4 forwarding configuration, and enables the systemd socket.
- The host installer does not copy package-owned binaries or unit files.
- Added an initial `sandboxctl-git` AUR package draft. It builds and installs the CLI, daemon, and socket/service units. It depends on the Arch `firecracker` package, which includes both Firecracker and jailer.
- `makepkg --printsrcinfo`, `bash -n packaging/aur/PKGBUILD`, `make check`, and `make build` pass.
- `systemd-analyze verify` passes against a temporary package install tree.
- `make check` passes outside the sandbox. The sandbox blocks the Unix socket setup used by the new integration tests.
- `make check`, `make build`, and `git diff --check` pass after moving CLI parsing and validation to command packages.
- Added Go-style documentation comments to exported APIs across the internal packages. `go vet ./...` and `go test ./...` pass.
- Fixed the manual-test asset target so it creates a missing ext4 root drive from verified cached SquashFS assets instead of reporting an incomplete asset set as complete.
- Updated the manual VM test to use the downloaded Ubuntu image's existing `ubuntu` account. The test now requests public-key authentication so it does not fall back to a password prompt.
- Retained the E2B upstream checkout in ignored `tmp/e2b-runtime` and recorded commit `47096f195aca4a545a9079b706efb3a18f51cf3f` in `docs/E2B-SOURCE.md`.
- Copied the E2B Apache-2.0 license text to `third_party/licenses/e2b-apache-2.0.txt`.
- Added `internal/imagebuild/distro` with declared `os-release` parsing and Debian/Ubuntu package and init profiles. The profile omits E2B-only packages and boot services.
- Added `internal/imagebuild/provision` with a deterministic renderer and guest script. The script creates the `dev` account, configures passwordless sudo inside the guest, enforces key-only SSH, installs a metadata bootstrap service, validates early network values, and removes machine identity and host keys for per-VM regeneration.
- Added `internal/hoststate` with the `/var/lib/sandboxctl` directory layout, version-2 privileged instance records, random stable IDs, unknown-field preservation, validation, and atomic writes with file and directory sync.
- Updated host installation to create the image, instance, allocation, and jail directories as root-owned mode `0700`. This keeps privileged image and instance data outside operator-writable XDG directories.
- Added tests for private directory modes, symlink rejection, atomic instance record permissions, unknown-field preservation, schema rejection, and invalid names.
- Added separate `ListInstances` and `InspectInstance` RPCs. The daemon reads from the root-owned host-state store and maps invalid names and missing records to structured gRPC errors.
- Updated `cmd/sandboxd` to use `/var/lib/sandboxctl` for instance reads. Added Unix-socket integration tests for sorted listing, record fields, inspection, invalid names, and missing records.
- Regenerated Protocol Buffer Go files with the pinned Buf plugins.
- Changed `sandboxctl list` to read records from sandboxd. It supports the documented versioned JSON output.
- Removed the XDG record scan from `sandboxctl list`. The command does not import operator-side instance records.
- `make check`, `make build`, and `git diff --check` pass after the daemon-backed list change.
- The host-side image builder, OCI reference checks, BuildKit VM orchestration, image manifest, SquashFS finalizer, and integration boot are still open.
- Added a locked, atomic allocation registry for stable UID, GID, `/30`, guest IP, guest MAC, and TAP assignments. The allocator skips host TAP-name conflicts, detects corrupt duplicate assignments, and requires the documented cleanup conditions before it releases an allocation.
- Added allocator tests for stable assignments, uniqueness, TAP conflicts, exhaustion, release and reuse, concurrent requests, corrupt registries, and symlink rejection.
- `make check` and `git diff --check` pass after the host-state changes. The generated Debian and Ubuntu scripts pass POSIX shell syntax validation in the tests.
- `gofmt`, `go vet ./...`, and `go test ./...` pass after the allocator change. The sandbox blocked the Unix-socket integration tests. The full test suite passed after running it outside the sandbox.
- Added the `CreateInstance` gRPC method with operation-specific resource and image fields. The daemon validates the request and returns structured invalid, duplicate, missing-image, canceled, and deadline errors.
- Added host configuration loading with owner, file type, size, schema, identifier range, and address-pool checks. The host installer and daemon now share the same typed configuration.
- Added installed-image manifest lookup at `/var/lib/sandboxctl/images/NAME/manifest.json`.
- Added stopped-instance creation. It reserves resources under the allocation lock, creates a sparse ext4 state disk with mode `0600` and the allocated UID/GID, stores the guest public key, and atomically writes the instance record. A retry validates and reuses an existing ext4 disk. It refuses to truncate an unexpected file.
- The create command now accepts the documented CPU, memory, disk size, and image options. It prepares the operator key before the RPC and refreshes the managed SSH fragment after creation.
- Updated SSH configuration sync to read daemon instance state and use the `sandbox-NAME` alias.
- Added tests for config and image manifest validation, state disk formatting, create retries and duplicates, gRPC creation and structured errors, public-key storage, and SSH fragment generation.
- `make proto-lint`, `gofmt`, `go vet ./...`, and `go test ./...` pass. The complete Go checks passed outside the sandbox because its build cache is read-only.
- Added the jailed SDK configuration builder for the V2 boot layout. It gives the jailer the allocated UID and GID, uses cgroup version 2, and connects the read-only SquashFS and writable ext4 drives to the proven initramfs arguments.
- Added tests for jail identity, SDK chroot setup, boot inputs, drive access modes, TAP names, and rejected paths. `gofmt`, `go vet ./...`, `go test ./...`, and `git diff --check` pass.
- Confirmed that Firecracker and jailer are installed in this environment. `/dev/kvm` is absent, so live jailed VM validation cannot run here.
- Added separate `StartInstance` and `StopInstance` RPCs and regenerated the Protocol Buffer Go files with pinned Buf plugins.
- Added a sandboxd supervisor that starts and stops jailed SDK machines, verifies process UID and GID, records PID start time and host boot ID, handles daemon-restart stop requests, and removes failed or stopped jail state safely.
- Added strict lookup for pinned image artifacts. The kernel, initramfs, and SquashFS must be root-owned read-only regular files.
- Updated `sandboxctl start` and `sandboxctl stop` to use sandboxd. `stop --force` is required for SIGKILL.
- Added unit tests for runtime lifecycle operations, daemon RPC behavior, process identity, runtime record permissions, and boot artifact validation.
- `gofmt`, `go vet ./...`, `go test ./...`, `make proto-lint`, and `git diff --check` pass for this milestone.
- Added a manifest parser for pinned x86-64 images. It validates fixed artifact names, sizes, digests, guest details, and kernel arguments.
- The image version now derives from the manifest fields and artifact digests. The manifest rejects changed metadata or artifacts under an old version.
- Added streamed image import over gRPC. The CLI verifies local artifacts before transfer. sandboxd verifies chunk order, size, and digest, then atomically publishes read-only artifacts in the root-owned image store.
- Added `sandboxctl image install NAME --version VERSION`. It reads bundles from the XDG data directory.
- Added `make stage-overlay-image` to package the Phase 1 kernel, initramfs, and Ubuntu SquashFS as a local image bundle.
- The jailed SDK configuration now uses the required kernel arguments from the validated image manifest.
- The initramfs archive now uses reproducible metadata. Running `make stage-overlay-image` twice produces the same pinned version and reuses the staged bundle.
- Gave the Phase 1 downloader a separate checksum file. This avoids failures when a manual test changes `root.ext4` in the shared asset directory.
- Added importer, manifest parser, and gRPC integration tests. The staged bundle target completed with a temporary XDG data directory.
- `gofmt`, `go vet ./...`, `go test ./...`, `make proto-lint`, `make build`, shell syntax checks, and `git diff --check` pass.
- The image CLI validated the staged bundle before it tried sandboxd. The daemon is not running here, so this host could not test the live import call.
- Added per-instance MMDS data to the jailed runtime. It includes the hostname, SSH user, public key, address, gateway, and interface. The SDK configures MMDS before it starts the guest.
- Added early guest IPv4 setup through the Firecracker SDK and an MMDS link-local route in the initramfs.
- Changed `sandboxctl ssh` to inspect daemon-owned instance state, refresh the generated SSH fragment, and execute the normal `sandbox-NAME` alias through OpenSSH configuration.
- A live host test confirmed that the jailed VM starts and accepts TCP connections on guest SSH. The guest did not accept the managed public key. The image provisioning path must include and run the bootstrap service before key login can pass.
- Added `internal/network.TapManager` to create, validate, configure, and remove per-instance TAP devices with `/usr/bin/ip`. It checks allocated ownership and `/30` addressing and removes partial TAPs on setup failure.
- Wired TAP preparation and cleanup into the jailed runtime lifecycle. The runtime cleans stale TAP devices only after it confirms that the recorded Firecracker process is stopped. It keeps runtime records and jail state if TAP cleanup fails.
- Added tests for TAP command sequences, invalid addresses, owner mismatch, partial setup rollback, and runtime prepare/remove ordering.
- `go test ./internal/network ./internal/runtime ./cmd/sandboxd` passed for this change.
- The Codex command environment does not expose `/dev/kvm`; the user's earlier serial and SSH VM tests confirm that the host can run VMs. The jailed runtime still needs a live boot test. Guest routing, forwarding, and firewall rules are still open.
- Added `make install` with a local `sandboxctl-local` package recipe. It uses `yay` to build this working tree. The local package test uses a bare Git remote beside the checkout. It does not use GitHub.
- Created `../sandboxctl-remote.git` as the local `local` remote and pushed `main` to it. `yay` installed `sandboxctl-local 0.1.0.r21.g983e9b0-1`; the installed CLI and daemon are available in `/usr/bin`.

## Decisions

- Keep the current SDK runner available until the jailed SDK runtime completes an integration boot.
- Keep Phase 1 as an isolated development experiment.
- Do not start sandboxd, its protocol, or the host installer before the Phase 1 exit conditions pass.
- The `download-test-assets` target converts the SquashFS into a writable ext4 root and selects changing remote artifacts. It does not match the Phase 1 image model. Do not use it as Phase 1 input.
- Use gRPC over the Unix socket. Use Buf with pinned remote plugins for schema lint and code generation.
- Use the Unix socket's owner, group, and mode as the only client authorization check. Membership in the `sandboxctl` group grants access to the full daemon API.
- Use separate typed RPC methods. Do not use a generic `Call` method or a negotiation RPC.
- Carry API versions and request IDs in gRPC metadata. Do not add these fields to messages.

## Blockers

The Codex command environment does not expose `/dev/kvm` and blocks `sudo`. The user's host terminal can run VMs and privileged commands. Do not infer host KVM availability from the Codex command environment.
Buf remote generation requires access to the Buf Schema Registry. `make generate` fails inside the sandbox because that network call is blocked. The generated files are present from a network-enabled run.
The repository has no license file. The AUR draft uses the GitHub URL inferred from the workspace path. Confirm the public source URL and select a license before publication.

The user installed the package, imported the default image, and created `pkgtest`. Structured logging found and resolved the TAP parser issue: `ip tuntap show` returned the unrelated `tailscale0` line before the requested TAP. SDK debug output identified the Firecracker startup failure: the jailer reports `Failed to exec into Firecracker: No such file or directory`. The host's `/usr/bin/firecracker` is dynamically linked and requires `/lib64/ld-linux-x86-64.so.2`, which is absent from the jail. The official Firecracker jailer documentation requires a statically linked Firecracker binary paired with the same-version jailer. The package now bundles the official static v1.17.0 x86-64 pair. Host install and doctor now reject a dynamic Firecracker binary. The daemon now uses the package-installed paths under `/usr/lib/sandboxctl`.

The latest live bootstrap test passed on first boot and after a stop and restart. It loaded MMDS data, verified networking, created the `dev` account, prepared host keys, configured key-only SSH, applied the hostname without D-Bus, and wrote the completion marker. The second SSH login accepted the existing host key and used the persistent authorized key. This confirms that bootstrap state and host keys survive a VM restart.

The current image-build iteration has started. `internal/imagebuild/buildkit` renders a deterministic BuildKit Dockerfile from a digest-pinned base image and builds a bounded context from the distro profile and provisioning renderer. `internal/imagebuild/guest` provides bounded SSH commands, atomic context uploads, and size-limited streaming downloads from a private build directory. Its fixed service accepts only the `build` operation. It runs BuildKit, checks the built guest's account and SSH policy, removes machine-specific files, and creates a deterministic SquashFS. `internal/imagebuild/builder.Runner` now coordinates context upload, the fixed service call, SquashFS download, and VM cleanup through a narrow factory interface. The pinned builder seed image is staged, but it has not yet been booted as a VM. This environment has no `/dev/kvm`; live builder validation needs the user's KVM host. Guest Internet routing remains unimplemented, so remote base-image pulls are not yet expected to work.

Added `make download-builder-tools` to download the pinned BuildKit v0.34.0 Linux x86-64 archive into `tmp/config/builder/tools`. The script checks the release archive digest before extraction, verifies the expected binaries, and sets private directory and executable modes. It records the Apache-2.0 license source. The archive is downloaded and verified locally. `buildctl --version` and `buildkitd --version` report v0.34.0. Re-running the target verifies and reuses the archive. The target does not install tools on the host.

Added `make stage-builder-image` and a pinned Ubuntu 24.04 builder seed recipe. The recipe uses a pinned OCI digest and Ubuntu package snapshot. It includes the fixed guest build tools and creates a SquashFS bundle with the existing kernel and initramfs. The script verifies inputs, BuildKit archive digest, required guest paths, and artifact hashes. It stages the versioned bundle in the user's XDG data directory with private modes and reuses an existing bundle only after checksum validation. Added `make manual-builder-test` to create and start a VM and verify the bootstrap marker, BuildKit version, and SquashFS command over SSH. A live builder VM boot is still required. `make check`, shell syntax checks, and `git diff --check` pass.

Updated the fixed guest build service to start BuildKit for one build, wait for its Unix socket, report bounded startup logs on failure, and stop the daemon when the build exits. It also checks that `buildctl` and `buildkitd` are v0.34.0. The service requires these tools at `/usr/local/bin` and `mksquashfs` at `/usr/bin`. `make check`, shell syntax checks, and `git diff --check` pass outside the sandbox. The reproducible builder image and sandboxd VM factory remain open. This environment still cannot boot a VM because it has no `/dev/kvm`.

Added a static guest bootstrap binary to the Phase 1 initramfs. Before `switch_root`, overlay init installs the binary and a systemd unit into the writable guest layer. The unit is wanted by `sockets.target` and ordered before `ssh.socket` and `ssh.service`. The bootstrap reads token-protected Firecracker metadata, validates the fixed schema, checks the guest address and route, creates the `dev` account, installs the authorized key, enforces key-only SSH, sets the hostname, creates guest host keys, and writes an atomic completion marker. It removes host keys on first boot and retains them on later boots. The guest sudo policy is added only when the image has both `sudo` and `visudo`. The raw Ubuntu test image lacks those tools. The live test confirmed that the service did not write its expected files. Added private bounded serial capture and `slog` events to locate the failure on the next run. The manual test prints the console log path after a timeout and leaves the sandbox running for diagnosis.

## Next tasks

1. Boot the staged builder image on the user's KVM host. Verify SSH, `buildctl`, `buildkitd`, and `mksquashfs` inside the guest.
2. Add a sandboxd-managed ephemeral builder lifecycle and implement the runner's VM factory. Start only the fixed builder image and service. Do not add a general command RPC.
3. Export a validated image bundle through the bounded guest stream. Include kernel, initramfs, SquashFS, and manifest.
4. Add `sandboxctl image build` and run the build on a KVM host.
5. Add routed guest Internet access and firewall rules if the builder or guest cannot reach required network resources.
6. Move the remaining CLI instance operations from XDG records when their daemon RPCs are implemented.
7. Test host installation and doctor on a disposable Arch host before publication. Confirm the public Git source URL and project license before publishing the AUR package.
