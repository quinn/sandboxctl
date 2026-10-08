# Continuation

Follow the implementation order in [SANDBOX-RUNTIME-V2.md](SANDBOX-RUNTIME-V2.md), section 18.
Use [PROGRESS.md](PROGRESS.md) to resume the current work.

The Phase 1 SquashFS and OverlayFS proof passed.
The temporary single-root virtual machine test also passed.
Keep that test available until the jailed runtime passes an integration boot.

Section 18, step 6 and stopped-instance creation are complete. Section 18, step 7 has daemon start and stop RPCs and a jailed SDK supervisor. The supervisor checks process identity and stores a private runtime record. The CLI start and stop commands use the daemon.

The runtime lifecycle and image import have unit and gRPC integration tests. `make stage-overlay-image` stages the Phase 1 files as an importable bundle. The user confirmed that jailed Firecracker starts the guest and that SSH reaches the guest address. The guest did not accept the managed key. The runtime now supplies per-instance MMDS data and early IPv4 settings. The image staging target still does not run the provisioning renderer. Connect provisioning to repeatable image building, then reinstall the image and retest `sandboxctl ssh NAME`. The Codex command environment does not expose `/dev/kvm`, so live VM validation must run from the user's host terminal.
