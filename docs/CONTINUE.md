# Continuation

Follow the implementation order in [SANDBOX-RUNTIME-V2.md](SANDBOX-RUNTIME-V2.md), section 18.
Use [PROGRESS.md](PROGRESS.md) to resume the current work.

The Phase 1 SquashFS and OverlayFS proof passed.
The temporary single-root virtual machine test also passed.
Keep that test available until the jailed runtime passes an integration boot.

Section 18, step 6 and stopped-instance creation are complete. Section 18, step 7 has daemon start and stop RPCs and a jailed SDK supervisor. The supervisor checks process identity and stores a private runtime record. The CLI start and stop commands use the daemon.

The latest staged image includes a guest bootstrap binary and systemd unit in the initramfs. The unit runs before `ssh.socket`, reads token-protected Firecracker metadata, creates the `dev` account, installs the public key, sets the hostname, creates per-instance host keys, disables password SSH, and writes `/var/lib/sandboxctl/bootstrap-complete`.

The live guest bootstrap test passed on 2026-10-09 on first boot and after a stop and restart. It confirmed the completion marker, runtime hostname, and public-key SSH login. SSH accepted the same host key after restart. Guest bootstrap writes `/etc/hostname` and calls `syscall.Sethostname`; it does not require D-Bus. The test log is `tmp/logs/guest-bootstrap-test.log`.

Guest bootstrap and state-disk persistence now have a working VM proof. The first builder VM boot reached guest bootstrap, but the interactive `systemd-firstboot` wizard blocked SSH. The image recipe now masks that wizard. The first retry failed during image installation because the daemon already had an image named `builder`. The manual test now assigns a unique image name each time. Stop the failed VM `builder-1791557469-643214`, then rerun `make manual-builder-test` on the user's KVM host. This environment has no `/dev/kvm`; live VM validation requires the user's KVM host.

The seed image is built by `make stage-builder-image`. Install the printed image with `sandboxctl image install builder --version VERSION`, create a sandbox with `--image builder`, and start it. The current recipe enables SSH and installs the fixed tools, but it does not yet have a custom builder bootstrap or runtime factory. Do not test remote OCI builds yet; guest Internet routing is not implemented.
