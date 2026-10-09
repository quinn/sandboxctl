# Continuation

Follow the implementation order in [SANDBOX-RUNTIME-V2.md](SANDBOX-RUNTIME-V2.md), section 18.
Use [PROGRESS.md](PROGRESS.md) to resume the current work.

The Phase 1 SquashFS and OverlayFS proof passed.
The temporary single-root virtual machine test also passed.
Keep that test available until the jailed runtime passes an integration boot.

Section 18, step 6 and stopped-instance creation are complete. Section 18, step 7 has daemon start and stop RPCs and a jailed SDK supervisor. The supervisor checks process identity and stores a private runtime record. The CLI start and stop commands use the daemon.

The runtime lifecycle and image import have unit and gRPC integration tests. `make stage-overlay-image` stages the Phase 1 files as an importable bundle. The user confirmed that jailed Firecracker starts and that SSH reaches the guest address. The guest did not accept the managed key. The runtime now supplies per-instance MMDS data and early IPv4 settings. `internal/imagebuild/buildkit` creates a pinned BuildKit context from the distro profile and provisioning renderer. `internal/imagebuild/guest` provides bounded SSH transfer and a fixed build/finalization service. `internal/imagebuild/builder.Runner` coordinates these steps and always closes the ephemeral VM through its factory interface. The pinned builder image and sandboxd VM factory are still needed. This environment has no `/dev/kvm`, `buildctl`, or `buildkitd`. Live VM validation must run from the user's host terminal after the builder image is available. The current staging target still packages the unprovisioned upstream SquashFS.
