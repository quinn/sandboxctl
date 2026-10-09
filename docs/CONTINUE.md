# Continuation

Follow the implementation order in [SANDBOX-RUNTIME-V2.md](SANDBOX-RUNTIME-V2.md), section 18.
Use [PROGRESS.md](PROGRESS.md) to resume the current work.

The Phase 1 SquashFS and OverlayFS proof passed.
The temporary single-root virtual machine test also passed.
Keep that test available until the jailed runtime passes an integration boot.

Section 18, step 6 and stopped-instance creation are complete. Section 18, step 7 has daemon start and stop RPCs and a jailed SDK supervisor. The supervisor checks process identity and stores a private runtime record. The CLI start and stop commands use the daemon.

The runtime lifecycle and image import have unit and gRPC integration tests. The latest staged image includes a guest bootstrap binary and systemd unit in the initramfs. The unit runs before `ssh.socket`, reads token-protected Firecracker metadata, creates the `dev` account, installs the public key, sets the hostname, creates per-instance host keys, disables password SSH, and writes `/var/lib/sandboxctl/bootstrap-complete`. Required Go checks and initramfs staging pass. `make manual-bootstrap-test` creates a uniquely named image and sandbox, imports the image, starts the sandbox, checks the marker and hostname, and tests public-key SSH. It saves output in `tmp/logs/guest-bootstrap-test.log`. Each run uses a distinct image name because the daemon does not replace installed image names. The test checks that the installed package includes MMDS support and applies a timeout to each SSH probe. The latest run used an old daemon package, so the guest did not receive MMDS data. The user stopped the test and sandbox, installed `sandboxctl-local 0.1.0.r43.g8ade2c8-1`, and restarted sandboxd. The package revision includes MMDS support. Rerun `make manual-bootstrap-test` on the KVM host. The sandbox remains running after a test timeout for diagnosis. The pinned builder image and sandboxd VM factory are still needed. This environment has no `/dev/kvm`.
