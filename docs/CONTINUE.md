# Continuation

Follow the implementation order in [SANDBOX-RUNTIME-V2.md](SANDBOX-RUNTIME-V2.md), section 18.
Use [PROGRESS.md](PROGRESS.md) to resume the current work.

The Phase 1 SquashFS and OverlayFS proof passed.
The temporary single-root virtual machine test also passed.
Keep that test available until the jailed runtime passes an integration boot.

Section 18, step 6 and stopped-instance creation are complete. Section 18, step 7 has daemon start and stop RPCs and a jailed SDK supervisor. The supervisor checks process identity and stores a private runtime record. The CLI start and stop commands use the daemon.

The runtime lifecycle and image import have unit and gRPC integration tests. `make stage-overlay-image` stages the Phase 1 files as an importable bundle. The daemon now prepares and removes per-instance TAP devices. The user has booted and tested VMs on this host through serial and SSH. The Codex command environment does not expose `/dev/kvm`, so run the jailed serial boot from the user's host terminal. Add routed guest networking after serial boot works. Build and provision a guest image before testing SSH. Keep the manual VM test available until the jailed runtime passes an integration boot.
