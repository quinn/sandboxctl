# Continuation

Follow the implementation order in [SANDBOX-RUNTIME-V2.md](SANDBOX-RUNTIME-V2.md), section 18.
Use [PROGRESS.md](PROGRESS.md) to resume the current work.

The Phase 1 SquashFS and OverlayFS proof passed.
The temporary single-root virtual machine test also passed.
Keep that test available until the jailed runtime passes an integration boot.

Section 18, step 6 and stopped-instance creation are complete. Section 18, step 7 has daemon start and stop RPCs and a jailed SDK supervisor. The supervisor checks process identity and stores a private runtime record. The CLI start and stop commands use the daemon.

The latest staged image includes a guest bootstrap binary and systemd unit in the initramfs. The unit runs before `ssh.socket`, reads token-protected Firecracker metadata, creates the `dev` account, installs the public key, sets the hostname, creates per-instance host keys, disables password SSH, and writes `/var/lib/sandboxctl/bootstrap-complete`.

The live guest bootstrap test passed on 2026-10-09 on first boot and after a stop and restart. It confirmed the completion marker, runtime hostname, and public-key SSH login. SSH accepted the same host key after restart. Guest bootstrap writes `/etc/hostname` and calls `syscall.Sethostname`; it does not require D-Bus. The test log is `tmp/logs/guest-bootstrap-test.log`.

Guest bootstrap and state-disk persistence now have a working VM proof. The pinned builder seed bundle stages at `tmp/test-data/sandboxctl/images/builder/sha256:290371acc9e0256812683e4f00fff9c9286ef976563452ee3bbf07b05fc69a02` for this test run. The immediate next step is to run `make manual-builder-test` on the user's KVM host. It stages and installs the seed bundle, starts a VM, and checks the bootstrap marker and fixed tool versions over SSH. This environment has no `/dev/kvm`; live VM validation requires the user's KVM host.

The seed image is built by `make stage-builder-image`. Install the printed image with `sandboxctl image install builder --version VERSION`, create a sandbox with `--image builder`, and start it. The current recipe enables SSH and installs the fixed tools, but it does not yet have a custom builder bootstrap or runtime factory. Do not test remote OCI builds yet; guest Internet routing is not implemented.
