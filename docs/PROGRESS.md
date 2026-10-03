# Migration progress

Last updated: 2026-10-03

## Authority and order

`docs/SANDBOX-RUNTIME-V2.md` is the authoritative runtime design.
The risk-first storage proof must pass before sandboxd work starts.
Section 18 now records this gate.

## Current phase

Section 18, step 3: add systemd socket and service assets for `sandboxd`.
Section 18, step 2 is complete. The protocol uses a fake runtime backend and includes `Ping`, `HostStatus`, structured errors, request limits, and deadlines.

Phase 1 storage proof is complete. It booted with read-only `/dev/vda` and writable `/dev/vdb`. A guest test file persisted across restart and disappeared after state-disk reset. The proof runner checked the SquashFS digest on each run.

## Completed

- The project uses the Firecracker Go SDK v1.0.0.
- The SDK runtime is behind an application-facing `runtime.Backend` interface.
- The current Go project passed `go vet ./...`, `go test ./...`, and `go build ./...` after the baseline interface change and Phase 1 tooling.
- The required `gofmt`, `go vet ./...`, and `go test ./...` checks pass after the latest changes.
- `bash -n` passes for the Phase 1 scripts and the legacy test scripts.
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
- `make check` passes outside the sandbox. The sandbox blocks the Unix socket setup used by the new integration tests.

## Decisions

- Keep the existing SDK runner as migration input until a new SDK runtime completes an integration boot.
- Keep Phase 1 as an isolated development experiment.
- Do not start sandboxd, its protocol, or the host installer before the Phase 1 exit conditions pass.
- The old `download-test-assets` flow converts the SquashFS into a writable ext4 root and selects changing remote artifacts. It does not match the Phase 1 image model. Do not use it as Phase 1 input.
- Use gRPC over the Unix socket. Use Buf with pinned remote plugins for schema lint and code generation.
- Use the Unix socket's owner, group, and mode as the only client authorization check. Membership in the `sandboxctl` group grants access to the full daemon API.
- Use separate typed RPC methods. Do not use a generic `Call` method or a negotiation RPC.
- Carry API versions and request IDs in gRPC metadata. Do not add these fields to messages.

## Blockers

The current development container has no `/dev/kvm` and blocks `sudo`. It cannot repeat the guest proof or validate privileged host operations.
Buf remote generation requires access to the Buf Schema Registry. `make generate` fails inside the sandbox because that network call is blocked. The generated files are present from a network-enabled run.

## Next tasks

1. Add the systemd socket and service assets in section 18, step 3.
2. Keep privileged operations and VM lifecycle RPCs out of the control-protocol step until the host installer and persistent-state steps.
3. Continue through the section 18 migration sequence only after the checks pass.
