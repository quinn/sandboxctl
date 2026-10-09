# Continuation

Follow the implementation order in [SANDBOX-RUNTIME-V2.md](SANDBOX-RUNTIME-V2.md), section 18.
Use [PROGRESS.md](PROGRESS.md) to resume the current work.

The Phase 1 SquashFS and OverlayFS proof passed.
The temporary single-root virtual machine test also passed.
Keep that test available until the jailed runtime passes an integration boot.

Section 18, step 6 and stopped-instance creation are complete. Section 18, step 7 has daemon start and stop RPCs and a jailed SDK supervisor. The supervisor checks process identity and stores a private runtime record. The CLI start and stop commands use the daemon.

The latest staged image includes a guest bootstrap binary and systemd unit in the initramfs. The unit runs before `ssh.socket`, reads token-protected Firecracker metadata, creates the `dev` account, installs the public key, sets the hostname, creates per-instance host keys, disables password SSH, and writes `/var/lib/sandboxctl/bootstrap-complete`.

The last live run started the VM and received metadata. SSH answered but rejected the managed key. The state disk had no bootstrap marker, authorized key, or SSH configuration drop-in. The guest serial log shows that systemd starts `sandboxctl-bootstrap.service` and then marks it failed. The bootstrap's own error was sent only to the guest journal. The service now sends its standard output and error to both the journal and serial console. The next run should show the failing bootstrap stage in `/var/lib/sandboxctl/instances/NAME/console.log`.

Run `make manual-bootstrap-test` to rebuild the initramfs and test the logging change. If it fails, inspect the reported console file with `sudo tail -n 160 /var/lib/sandboxctl/instances/NAME/console.log`. This environment has no `/dev/kvm`. The pinned builder image and sandboxd VM factory remain open.
