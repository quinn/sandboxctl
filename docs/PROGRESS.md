# Implementation progress

Last updated: 2026-10-07

## Authority and order

`docs/SANDBOX-RUNTIME-V2.md` is the authoritative runtime design.
The risk-first storage proof must pass before sandboxd work starts.
Section 18 now records this gate.

## Current phase

The Phase 1 storage proof passed. Section 18 steps 1 through 3 are complete. Step 4 host installation and doctor support are implemented and still need final integration checks on a disposable Arch host.

Current implementation work follows section 18, step 6. The daemon-owned state package defines `/var/lib/sandboxctl` and saves version-2 instance records atomically. The host installer creates the state directories as root-owned mode `0700`. sandboxd exposes list and inspect RPCs. `sandboxctl list` reads daemon state. The current create, start, stop, and SSH commands still use operator XDG records. Move them when the allocation and lifecycle RPCs are ready. Keep the temporary single-root test until the jailed runtime passes integration testing.

The E2B runtime checkout is retained in ignored `tmp/e2b-runtime` at commit `47096f195aca4a545a9079b706efb3a18f51cf3f`. The license, extraction record, and first Debian/Ubuntu provisioning profile are in place. There is no build command, BuildKit VM orchestration, image manifest, or SquashFS finalizer yet.

Phase 1 storage proof is complete. It booted with read-only `/dev/vda` and writable `/dev/vdb`. A guest test file persisted across restart and disappeared after state-disk reset. The proof runner checked the SquashFS digest on each run.

## Current manual test report

Iteration 1, the manual one-VM test, passed on 2026-10-05.
The `manualtest` guest booted, accepted the managed SSH key as `ubuntu`, and retained a marker under `/home/ubuntu` after a stop and restart.
The complete output is in the ignored file `tmp/logs/manual-test.log`.

The manual test command prepares the private disk before boot. It fixes guest home ownership, installs the public key, disables password and keyboard-interactive SSH, and validates the SSH server configuration. It waits for SSH on both boots. It checks persistence outside `/tmp`, which is cleared at boot.

Next: test the new provisioning renderer and finish the image-builder foundation. Then add BuildKit orchestration and finalization. Keep the manual one-VM test as a regression check.

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
- `make check` and `git diff --check` pass after the host-state changes. The generated Debian and Ubuntu scripts pass POSIX shell syntax validation in the tests.

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

The current development container has no `/dev/kvm` and blocks `sudo`. It cannot repeat the guest proof or validate privileged host operations.
Buf remote generation requires access to the Buf Schema Registry. `make generate` fails inside the sandbox because that network call is blocked. The generated files are present from a network-enabled run.
The repository has no configured Git remote or license file. The AUR draft uses the GitHub URL inferred from the workspace path. Confirm the public source URL and select a license before publication.

## Next tasks

1. Implement the UID, GID, address, and TAP registries in section 18 step 6. Add instance creation after these allocators can produce complete records.
2. Move the remaining CLI instance operations from XDG records when their daemon RPCs are implemented. Preserve the manual test path until the jailed runtime passes integration testing.
3. Finish the SDK and jailer runtime in section 18 step 7 before the builder VM depends on it.
4. Add image manifest validation and cold-boot SquashFS finalization. Keep OCI pulls and extraction inside the builder VM through BuildKit.
5. Test host installation and doctor on a disposable Arch host before publication. Confirm the public Git source URL and project license before publishing the AUR package.
