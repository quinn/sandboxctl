# Continuation

Follow the implementation order in [SANDBOX-RUNTIME-V2.md](SANDBOX-RUNTIME-V2.md), section 18.
Use [PROGRESS.md](PROGRESS.md) to resume the current work.

The Phase 1 SquashFS and OverlayFS proof passed.
The temporary single-root virtual machine test also passed.
Keep that test available until the jailed runtime passes an integration boot.

Section 18, step 6 and stopped-instance creation are complete. Section 18, step 7 is underway. The SDK jailed configuration now uses the allocated UID and GID with the Phase 1 read-only SquashFS and writable ext4 layout. It does not start or supervise Firecracker yet.

Next, connect this configuration to sandboxd start and stop operations. Add process identity tracking and failed-start cleanup. The current host has no `/dev/kvm`, so run the live jailed boot on a Linux KVM host. Image import is still required for the complete create/start workflow. Keep the manual VM test available until the jailed runtime passes an integration boot.
