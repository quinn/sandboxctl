# Migration progress

Last updated: 2026-10-03

## Authority and order

`docs/SANDBOX-RUNTIME-V2.md` is the authoritative runtime design.
The risk-first storage proof must pass before sandboxd work starts.
Section 18 now records this gate.

## Current phase

Phase 1: prove a SquashFS lower filesystem with an ext4 OverlayFS state disk.

The proof must boot one Firecracker VM with read-only `/dev/vda` and writable `/dev/vdb`.
It must write a file, stop and restart the VM, and confirm that the file remains.
It must replace the state disk and confirm that the file is gone.
Use serial output. Do not add networking or sandboxd to this proof.

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
- The user confirmed that the updated VM boot reaches the guest root prompt. State-disk restart and replacement checks remain.
- The guest reports `/` as OverlayFS. The user wrote `/root/overlay-proof-test`, flushed it with `sync`, and read back the expected contents. Stop the VMM from a second host terminal because serial input sends Ctrl-C and `exit` to the guest.

## Decisions

- Keep the existing SDK runner as migration input until a new SDK runtime completes an integration boot.
- Keep Phase 1 as an isolated development experiment.
- Do not start sandboxd, its protocol, or the host installer before the Phase 1 exit conditions pass.
- The old `download-test-assets` flow converts the SquashFS into a writable ext4 root and selects changing remote artifacts. It does not match the Phase 1 image model. Do not use it as Phase 1 input.

## Blockers

- `/dev/kvm` is absent in the current development environment.
- The current container blocks `sudo` privilege escalation.
- The pinned kernel and SquashFS image have not been downloaded in this workspace.

The current development environment can build and test code. It cannot run the Firecracker boot proof because `/dev/kvm` is not available.

## Next tasks

1. Write a test file in the guest and run `sync`.
2. Stop and restart the VM. Confirm the file remains and the init marker reports retention.
3. Replace the state disk. Confirm the guest test file is gone and the init marker is new.
4. Confirm the SquashFS digest remains unchanged.
5. Start the sandboxd protocol only after the Phase 1 exit conditions pass.
