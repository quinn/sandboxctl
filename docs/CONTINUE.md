# Continuation

Follow the implementation order in [SANDBOX-RUNTIME-V2.md](SANDBOX-RUNTIME-V2.md), section 18.
Use [PROGRESS.md](PROGRESS.md) to resume the current work.

The Phase 1 SquashFS and OverlayFS proof passed.
The temporary single-root virtual machine test also passed.
Keep that test available until the jailed runtime passes an integration boot.

Section 18, step 6 and stopped-instance creation are complete. Section 18, step 7 has daemon start and stop RPCs and a jailed SDK supervisor. The supervisor checks process identity and stores a private runtime record. The CLI start and stop commands use the daemon.

The latest staged image includes a guest bootstrap binary and systemd unit in the initramfs. The unit runs before `ssh.socket`, reads token-protected Firecracker metadata, creates the `dev` account, installs the public key, sets the hostname, creates per-instance host keys, disables password SSH, and writes `/var/lib/sandboxctl/bootstrap-complete`.

The last live run started the VM and received metadata. SSH answered but rejected the managed key. After the user stopped the VM, the writable state disk had no bootstrap marker, authorized key, or SSH configuration drop-in. The guest bootstrap service did not complete. The code now captures the guest serial console in `/var/lib/sandboxctl/instances/NAME/console.log`. This file is private and size-limited. The CLI and daemon also log command and RPC lifecycle events with `slog`. Guest bootstrap logs each setup stage without logging the key. These changes pass the full Go checks. They have not yet been installed or tested on the KVM host.

Run `make install`, restart `sandboxd.service`, then run `make manual-bootstrap-test`. If it fails, stop the reported sandbox and inspect its console file with `sudo tail -n 120 /var/lib/sandboxctl/instances/NAME/console.log`. This environment has no `/dev/kvm`. The pinned builder image and sandboxd VM factory remain open.
