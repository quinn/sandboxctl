# Continuation

Follow the implementation order in [SANDBOX-RUNTIME-V2.md](SANDBOX-RUNTIME-V2.md), section 18.
Use [PROGRESS.md](PROGRESS.md) to resume the current work.

The Phase 1 SquashFS and OverlayFS proof passed.
The temporary single-root virtual machine test also passed.
Keep that test available until the jailed runtime passes an integration boot.

Section 18, step 6 and stopped-instance creation are complete. Section 18, step 7 has daemon start and stop RPCs and a jailed SDK supervisor. The supervisor checks process identity and stores a private runtime record. The CLI start and stop commands use the daemon.

The latest staged image includes a guest bootstrap binary and systemd unit in the initramfs. The unit runs before `ssh.socket`, reads token-protected Firecracker metadata, creates the `dev` account, installs the public key, sets the hostname, creates per-instance host keys, disables password SSH, and writes `/var/lib/sandboxctl/bootstrap-complete`.

The first live guest bootstrap test passed on 2026-10-09. It confirmed the completion marker, runtime hostname, and public-key SSH login. Guest bootstrap writes `/etc/hostname` and calls `syscall.Sethostname`; it does not require D-Bus. The manual test now stops and restarts the same sandbox, then checks bootstrap state and SSH again.

Run `make manual-bootstrap-test` on the KVM host to verify persistence and host-key stability across restart. If it fails, inspect the reported console file with `sudo tail -n 160 /var/lib/sandboxctl/instances/NAME/console.log`. This environment has no `/dev/kvm`. The next implementation phase is a pinned builder image and sandboxd builder-VM factory.
