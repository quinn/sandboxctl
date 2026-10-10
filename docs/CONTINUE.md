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

`sandboxctl image build` runs the fixed guest build service in a temporary VM through the installed daemon. It checks the installed builder version against the local source bundle. It validates the finished SquashFS and publishes a private, versioned bundle with the builder kernel and initramfs. The live build passed and published `guest-1791582281-1022084` at `sha256:b545b7dac58a352e546fabc07d045520a075685195e94ad714b6e44b68609574`. The built image passed first boot, bootstrap, key-based SSH, and a stop/restart persistence check. Two VMs from the image ran at the same time. They used separate state disks, had outbound HTTPS, and could not connect to each other's SSH port. Both remain running for inspection. The test log is `tmp/logs/image-build-test.log`.

The finalizer now uses `mksquashfs -all-time 0`, which works with Ubuntu's SquashFS 4.6.1. It sets mode `0644` on the finished image and mode `0711` on its output directory so the SSH account can download it. `sandboxctl reset NAME` replaces only the stopped sandbox's state disk. The live reset test passed with an installed daemon and disposable guest.

The latest host `doctor` run found that managed TAP routes looked like address-pool conflicts and that the CLI could not read root-owned image files. The route check now skips only generated sandbox TAP names. A new `ListImages` RPC validates installed image bundles in sandboxd, and `doctor` uses it. After the daemon upgrade, `sandboxctl doctor` passed every check. Both running acceptance VMs answered through their OpenSSH Include aliases. Their Firecracker processes use distinct unprivileged UIDs. The guests share the same pinned read-only image and use separate writable state disks. The first guest had outbound HTTPS access and could not reach the peer's SSH service. The VMs remain running.

The clean-host Section 19 check is deferred until a clean supported Arch host is available. Section 18 cleanup review found no direct Herdr commands, SSH or SCP wrappers, or firectl runtime code.

The `sandboxd` service now has systemd filesystem and process restrictions. `systemd-analyze verify` passes. The live service reports exposure 7.4. A fresh VM started and accepted public-key SSH after the package upgrade and daemon restart. `sandboxctl doctor` passed. The disposable VM was stopped and deleted. Restarting an older acceptance VM failed because its previous jail contains a stale Firecracker log FIFO; the fresh-VM smoke test passed, so this is limited to restarting that old instance.

The seed image is built by `make stage-builder-image`. The manual test uses a unique image name each time because installed images cannot be replaced. The recipe enables SSH and installs the fixed tools. It does not provide guest Internet access.

Section 18 implementation work is complete. The active instance model no longer writes `herdr_label`. The runtime cleanup audit found no direct Herdr commands, SSH or SCP wrappers, or firectl runtime code. Revisit Section 19 when a clean supported Arch host is available.
