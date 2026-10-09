# Continuation

Follow the implementation order in [SANDBOX-RUNTIME-V2.md](SANDBOX-RUNTIME-V2.md), section 18.
Use [PROGRESS.md](PROGRESS.md) to resume the current work.

The Phase 1 SquashFS and OverlayFS proof passed.
The temporary single-root virtual machine test also passed.
Keep that test available until the jailed runtime passes an integration boot.

Section 18, step 6 and stopped-instance creation are complete. Section 18, step 7 has daemon start and stop RPCs and a jailed SDK supervisor. The supervisor checks process identity and stores a private runtime record. The CLI start and stop commands use the daemon.

The latest staged image includes a guest bootstrap binary and systemd unit in the initramfs. The unit runs before `ssh.socket`, reads token-protected Firecracker metadata, creates the `dev` account, installs the public key, sets the hostname, creates per-instance host keys, disables password SSH, and writes `/var/lib/sandboxctl/bootstrap-complete`.

The live guest bootstrap test passed on 2026-10-09 on first boot and after a stop and restart. It confirmed the completion marker, runtime hostname, and public-key SSH login. SSH accepted the same host key after restart. Guest bootstrap writes `/etc/hostname` and calls `syscall.Sethostname`; it does not require D-Bus. The test log is `tmp/logs/guest-bootstrap-test.log`.

Guest bootstrap and state-disk persistence now have a working VM proof. The live builder VM test passed on 2026-10-09 with image `sha256:b939e76287883d113f12f7702d4f09ec5c24c2a16500e3220848a266225bc420`. It verified SSH, the bootstrap marker, BuildKit v0.34.0, and SquashFS 4.6.1. The user stopped the VM. The test log is `tmp/logs/builder-test.log`. Safe stopped-instance deletion passed against the installed daemon on 2026-10-09. The test created and removed a stopped sandbox and confirmed it was absent from the list. The test log is `tmp/logs/delete-test.log`.

`sandboxctl image build` now runs the fixed guest build service in a temporary VM through the installed daemon. It checks the installed builder version against the local source bundle. It validates the finished SquashFS and publishes a private, versioned bundle with the builder kernel and initramfs. It does not require a daemon change or package reinstall. The complete Go checks pass. Run `make manual-image-build` on the user's KVM host. The target uses the workspace CLI, an isolated XDG data/config/state tree, and writes `tmp/logs/image-build-test.log`. Guest Internet routing is still absent, so the first registry pull may fail. Confirm that the factory deletes the temporary VM after the build result, then add guest routing if registry access fails.

The seed image is built by `make stage-builder-image`. The manual test uses a unique image name each time because installed images cannot be replaced. The recipe enables SSH and installs the fixed tools. It does not provide guest Internet access.
