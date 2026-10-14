# Continuation

## Port forwarding

The workspace implements `sandboxctl forward NAME [HOST_PORT:]GUEST_PORT`.
The daemon saves mappings in version 3 instance records.
It migrates version 2 records when they are read.
It binds host loopback ports and uses a separate forwarding key through native Go SSH.
The first registration installs the restricted public key in the guest.
Guest setup uses the operator key from XDG storage.
The daemon does not read that operator key.
Native SSH now uses OpenSSH's host-key order with trusted-key algorithms first.
The daemon uses its saved pin to select the first algorithm.
CLI setup uses the managed known-hosts entries.
The installed daemon needs an update for this ordering fix.
`--list` reports saved mappings and connection errors.
`--remove GUEST_PORT` closes the listener and its streams.
Stop closes tunnels.
Start and daemon reconciliation restore mappings for verified running instances.
Reset preserves mappings and clears the forwarding host-key pin.
The new CLI and daemon must both be installed before use.
Existing images need no rebuild for this feature.
Verification results are recorded in `docs/PROGRESS.md`.


The Codex example now uses the documented native installer at `https://chatgpt.com/codex/install.sh` with release 0.160.0.
It installs the complete package and requires no Node.js runtime.
The previous example copied only the CLI binary and failed during normal startup.
The running `weight-tracker` guest was repaired with the official installer.
Normal Codex startup reached the sign-in screen.
No login or model task was performed during that check.
The updated example image was built and staged at `sha256:9132d988263d0947a63726421e682e64f197e19c61af19ec2a884d0390b6a6a6`.
The running guest uses the old installed image and already has the repaired package in its writable disk.
Replace the installed image after the operator requests removal of the dependent sandbox.

The guest image builder now preserves numeric ownership and file modes through tar export and extraction inside the builder VM.
The former local exporter assigned root ownership to private skeleton files.
The operator created a new `weight-tracker` sandbox from the installed `codex` image.
Its Codex directory ownership was repaired in the running guest.
The fixed workspace CLI built and staged a corrected `codex` image.
The live build passed guest home and SSH ownership validation before creating SquashFS.
The operator requested deletion of the sandbox and replacement of the old image.
The Git pull and deletion of the new `weight-tracker` sandbox succeeded.
The old installed image was removed.
The corrected `codex` image is installed at `sha256:a33d40dd666c63823d3b2eded7af7bf6f59b60f6102eb91b885d9a4885f35cf8`.
At completion of that image replacement, no sandboxes remained.
Required Go checks, build, and shell syntax checks pass.
See PROGRESS.md for image staging and installation status.

The SSH configuration now binds host-key trust to the daemon's stable instance identifier with `HostKeyAlias`.
Stop and start keep the alias.
Recreated sandboxes get a new alias even when the name or address is reused.
Reset keeps the alias and requires verified explicit host-key replacement.
The workspace passes formatting, vet, tests, and build.
The operator reinstalled the system CLI on 2026-10-13.
The live SSH identity check passed after the operator approved the current instance's fingerprint.
The automatic Git pull and deletion of `weight-tracker` succeeded.
At completion of that deletion, no sandboxes remained.
The `codex` example image is built and installed.

The active phase is the Git task workflow documented in README.md.
Create and start attach one repository to a sandbox.
The branch and remote use the sandbox name.
The operator CLI stores the association in private XDG state and uses Git over SSH.
`sandboxctl pull` retrieves committed work and the guest report.
Delete pulls all attached tasks before it removes any selected sandbox.
The next task is a live guest check on a host with KVM access.
See PROGRESS.md for completed checks and remaining validation.

The Arch package build now creates and includes the pinned builder image.
`sandboxctl init` verifies it and installs it through the daemon image stream.
`sandboxctl image build` selects the packaged builder name and digest by
default. `sandboxctl doctor` checks that the exact packaged version is
installed. Content-based builder names let package updates keep old images.

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

The `sandboxd` service now has systemd filesystem and process restrictions. `systemd-analyze verify` passes. The live service reports exposure 7.4. A fresh VM started and accepted public-key SSH after the package upgrade and daemon restart. `sandboxctl doctor` passed. The disposable VM was stopped and deleted. Restarting an older acceptance VM exposed a stale Firecracker log FIFO. The supervisor removes that exact FIFO after it confirms that no process uses the VM's allocated UID or GID. Runtime tests cover cleanup and refusal to remove unexpected paths. After package installation and daemon restart, `builtguest-1791582281` started, accepted SSH, then accepted SSH again after a stop and restart. The VM was left stopped.

The seed image is built by `make stage-builder-image`. The manual test uses a unique image name each time because installed images cannot be replaced. The recipe enables SSH and installs the fixed tools. It does not provide guest Internet access.

Section 18 implementation work is complete. The active instance model no longer writes `herdr_label`. The runtime cleanup audit found no direct Herdr commands, SSH or SCP wrappers, or firectl runtime code. Revisit Section 19 when a clean supported Arch host is available.
