# Implementation progress

## Package build and installation plan

The operator requested one checkout-based package recipe for local and GitHub release builds.
The separate Arch User Repository (AUR) recipe and plan were removed.
`packaging/local/PKGBUILD` retains the local `sandboxctl-local` profile.
Release builds select the `sandboxctl` profile with the existing version tag.
Package builds embed the package version in the CLI and daemon.
The recipe includes the builder manifest, kernel, initramfs, and SquashFS in `/usr/share/sandboxctl/builder`.
The existing XDG cache retains its file-presence lookup and manual invalidation through `make clean-builder`.
Source changes do not trigger an automatic builder rebuild.
The builder staging script and local recipe cache logic are unchanged.
Package installation, explicit host setup, and verified builder import remain separate operations.
`scripts/build-release.sh` requires a clean checkout at the version tag and runs `make check`.
It creates a verified package, `SHA256SUMS`, and `release.json` in a new output directory.
`scripts/publish-release.sh` verifies these files and the remote tag commit before draft upload.
It refuses to replace files in a published release.
The operator selected a GitHub-hosted runner with an Arch Linux container.
The manual workflow builds as an unprivileged user with rootless Podman and the `vfs` storage driver.
Only the draft upload job receives repository write permission.
Actions use pinned commit IDs.
The workflow caches only completed builder bundles with a manual cache revision.
The package plan documents release creation, user installation, builder version conflicts, and guarded image commands.
The README, runtime specification, continuation notes, and design decisions document this flow.
Fixture tests exercise the single package recipe and cache lookup with temporary bundles.
They verify cache reuse, staging, installed package paths, filenames with spaces, and refusal to package missing, empty, or symbolic-link cache artifacts.
The tests stop before any download or builder image construction.
Release tests cover package profiles, version parsing, tag identity, clean checkouts, output refusal, checksums, builder corruption, and draft upload behavior.
Package and release fixture tests pass.
`make check` passes formatting, vet, all Go tests, and Protocol Buffer lint.
Fresh `go test -count=1 ./...`, shell syntax checks, workflow YAML parsing, and `git diff --check` pass.
`make build SANDBOXCTL_BUILD_VERSION=0.2.0` passes.
The built CLI reports `0.2.0` through `sandboxctl version`.
`make check-builder` reports the existing local bundle at `sha256:0642a10332bfafee3407f5b14f959032d17f7c1a5d1e3c5738c9d3e98fb82b07` as complete.
No builder image rebuild, cache removal, package installation, or host setup was performed.
The GitHub workflow and a full release package build have not run.
The operator created `github.com/quinn/sandboxctl`.
The repository now uses `git@github.com:quinn/sandboxctl.git` as `origin`.
The existing `local` remote remains available.
SSH access to the empty GitHub repository passed.
GitHub CLI 2.100.0 is already installed.
Its saved login is invalid and needs operator authentication.
The next task is a workflow run after the operator selects a version and completes GitHub CLI authentication.
No GitHub release was created or published.

## SSH host-key algorithm ordering

Native Go SSH host-key selection now follows OpenSSH ordering in the installed CLI and daemon.
The operator approved OpenSSH ordering with trusted-key algorithms first.
CLI setup previously forced Ed25519.
The daemon used Go's default order, which preferred ECDSA and RSA before Ed25519.
The daemon could therefore select a different host key from the one setup verified and pinned.
The shared order now filters OpenSSH preferences through Go's supported algorithms.
Trusted key types are promoted without changing their relative order.
RSA keys use the corresponding SHA-512 and SHA-256 signature algorithms.
The daemon derives its first preference from its saved host-key pin.
CLI setup derives its first preferences from the managed known-hosts file.
Exact host-key verification remains required.
A regression test reproduces the original mismatch with Go's default order.
Multi-key tests verify Ed25519, ECDSA, and RSA pins during registration and restoration.
Changed keys still fail exact verification.
CLI preference tests cover unknown hosts, multiple keys, hashed aliases, unrelated instances, and old address entries.
The supported default order matches the installed OpenSSH client's reported order.
A real OpenSSH server with Ed25519 and ECDSA host keys verifies selection and restricted forwarding access.
`gofmt`, `go vet ./...`, `go test ./...`, race checks, `make build`, and `git diff --check` pass.
The fixes were committed and pushed as `5d69058`.
The operator reinstalled the CLI and daemon.
The saved `weight-tracker` mapping was active after reinstallation.
The installed `sandboxctl forward weight-tracker 5173` command completed successfully.
The mapping reports `active` at `127.0.0.1:5173`.
The guest web service returned HTTP status `200` through that listener.
The forwarding task is complete.
The next task is the operator's next requested change.

## Managed known-hosts permissions

The managed known-hosts permission repair is complete in the workspace.
The rejected operator file belonged to the operator and had mode `0644`.
The previous setup helper applied mode `0600` only during file creation.
The native SSH host-key check then rejected the existing file.
The shared helper now validates the opened regular file and operator ownership before restoring mode `0600` through its descriptor.
It rejects symbolic links and special files.
It preserves file identity and trusted-key contents.
Forwarding verification uses the same helper.
Changed host keys still require explicit verified replacement.
The regression tests and required Go checks passed.
The workspace CLI restored the live file to mode `0600` and registered port 5173 for `weight-tracker`.
The daemon then reported a host-key mismatch.
The live HTTP check failed with a connection reset.
The installed host-key algorithm correction passed the live forwarding check.
The original permission failure and the later host-key mismatch are resolved.

## Port forwarding implementation

Daemon-managed forwarding is complete in the workspace.
`sandboxctl forward NAME [HOST_PORT:]GUEST_PORT` registers a saved mapping and returns its host endpoint.
`--list` reports saved mappings and tunnel errors.
`--remove GUEST_PORT` removes a mapping and closes its listener and streams.
Host listeners bind only to `127.0.0.1`.
An occupied preferred or saved port triggers direct port-zero allocation.
The daemon shares one native Go SSH connection for each sandbox.
It retries interrupted connections and checks the pinned guest host key on each connection.
Each sandbox supports 32 mappings and 128 active connections.
Version 3 instance records store mappings and the trusted public host key.
Version 2 records migrate in memory and use version 3 on their next save.
Unknown record fields survive migration and updates.
Lifecycle and forwarding operations serialize record changes.
Stop closes tunnels and retains mappings.
Start and daemon reconciliation restore mappings for verified running instances.
Delete removes the forwarding key and saved mappings.
Reset preserves mappings and clears the forwarding host-key pin.
The CLI installs a separate restricted public key through native Go SSH.
The daemon does not read operator private keys.
A separate guest key file survives bootstrap in existing images.
The guest key cannot run commands, obtain a terminal, open reverse forwarding, or connect to other guest addresses.
The CLI uses the existing managed known-hosts file and stable instance alias.
Changed operator host keys remain rejected until the operator verifies and updates trust.
The runtime specification, README, command help, continuation notes, and design decisions describe the implemented behavior.
The SSH dependency is pinned at `golang.org/x/crypto` v0.58.0.
The dependency license inventory includes the updated pinned modules.
`gofmt`, `go vet ./...`, `go test ./...`, Buf lint, `make build`, and `git diff --check` pass.
Race checks pass for forwarding, host state, operator SSH, and daemon RPC packages.
Tests cover parser bounds, schema migration, key privacy, port collisions, saved-port reuse, daemon restart, process identity, key changes, mapping limits, write failure, cancellation, removal, and stop cleanup.
Native SSH integration tests verify shared connections and byte transfer.
A temporary OpenSSH server verifies the actual restricted-key policy.
The operator installed the CLI and daemon.
The live `weight-tracker` guest passed forwarding registration and an HTTP request through host port 5173.
The saved mapping was active after the SSH fixes were installed.
The next task is the operator's next requested change.

## Codex package installation

The documented standalone Codex installation is implemented.
Codex 0.160.0 requires a complete local package for its default background server startup.
The example previously copied only the CLI binary.
The Dockerfile now runs `https://chatgpt.com/codex/install.sh` with an explicit release version and non-interactive installation.
The installer verifies package release checksums.
The image stores the complete package in `/usr/local/lib/codex` and exposes its command in `/usr/local/bin`.
The image does not set `CODEX_HOME` for guest sessions.
The guest retains its login and rules under `/home/dev/.codex`.
The example places the operator's curated-plugin cache blocker in `/etc/skel`.
Provisioning copies it when it creates `dev`.
This step does not require `sudo` or an existing guest home during the Dockerfile build.
The build checks package metadata, helper binaries, and the CLI version.
Node.js is not required.
The same installer repaired the running `weight-tracker` guest at Codex 0.160.0.
Normal startup reached the sign-in screen without the incomplete-package error.
The startup check exited without signing in or submitting a task.
`gofmt`, `go vet ./...`, `go test ./...`, and `git diff --check` pass.
The updated example built and staged `codex` at `sha256:9132d988263d0947a63726421e682e64f197e19c61af19ec2a884d0390b6a6a6` on 2026-10-13.
The build removed its temporary builder sandbox.
The staged image has not been installed because the running guest uses the old installed image.
The running guest already has the repaired complete package.
The next task is replacement of the installed image when the operator requests removal of its dependent sandbox.

## Guest filesystem ownership

Ownership preservation in guest image export is complete in the workspace.
The running `weight-tracker` guest reported a root-owned `/home/dev/.codex` directory with mode `0700`.
BuildKit's local directory exporter assigned root ownership to all exported files.
Guest bootstrap repaired the home and SSH directory owners but did not repair the skeleton files.
The builder now exports a tar archive and extracts it inside the builder VM with numeric owners and original file modes.
The builder checks ownership of the guest home and SSH directories before creating SquashFS.
The running guest's Codex directory and rules now belong to `dev` with modes `0700` and `0600`.
An extraction regression test covers numeric ownership, private modes, executable files, and symbolic and hard links.
The manual Dockerfile check covers private skeleton rules and a custom non-root owner.
`gofmt`, `go vet ./...`, `go test ./...`, `make build`, shell syntax checks, and `git diff --check` pass.
The fixed workspace CLI built and staged `codex` at `sha256:a33d40dd666c63823d3b2eded7af7bf6f59b60f6102eb91b885d9a4885f35cf8` on 2026-10-13.
The live build passed the ownership checks before creating SquashFS.
The build removed its temporary builder sandbox.
The operator requested removal of the sandbox and old image.
The CLI stopped `weight-tracker`, pulled its Git task, and deleted it.
The starting and result commit were both `42cb6354524b455f01a36f155a3f2dcd139fc5d5`.
The CLI removed the old installed `codex` image and installed the corrected staged version.
No sandboxes remain.
The next task is a live Codex login and guest-change round trip from the corrected image.
The updated manual Dockerfile check has not run.

## SSH host-key identity

Host-key isolation by sandbox instance identifier is complete.
The daemon-backed SSH fragment now sets `HostKeyAlias sandbox-instance-ID` for each sandbox alias and guest address.
The CLI validates the daemon identifier before it changes the fragment.
Recreated sandboxes receive separate trust even when names or addresses are reused.
Configuration refresh preserves all known-hosts entries.
Existing sandboxes require host-key confirmation when they first use the new alias.
Reset keeps the identifier and still requires verified explicit host-key replacement.
Tests cover daemon identifier transfer, invalid identifiers, restart, address changes, name and address reuse, and deletion.
OpenSSH configuration resolution and known-host lookup checks cover these cases.
`gofmt`, `go vet ./...`, `go test ./...`, `make build`, and `git diff --check` pass.
The operator reinstalled the CLI on 2026-10-13.
The regenerated SSH fragment resolved the current `weight-tracker` instance identifier through OpenSSH.
SSH requested first-use trust for that identifier without rejecting the old address entry.
The operator approved the displayed fingerprint.
The automatic Git pull succeeded, and deletion removed `weight-tracker`.
The starting and result commit were both `42cb6354524b455f01a36f155a3f2dcd139fc5d5`.
At completion of that deletion, no sandboxes remained, and the `codex` image remained installed.
The next task is a live Codex login and guest-change round trip.

## Relative Dockerfile build context

Fixed image builds with a relative `--context` directory.
The snapshot builder now resolves the context to an absolute path before it resolves symbolic links and checks Dockerfile containment.
Regression tests cover inferred and explicit Dockerfiles, relative and absolute path combinations, the default current-directory context, and relative symbolic-link contexts.
Containment tests still reject Dockerfiles outside the context, including symbolic-link escapes.
`gofmt`, `go vet ./...`, `go test ./...`, `make build`, and `git diff --check` pass.
The relative context fix is complete.
The fixed CLI built and installed the `codex` example image with `--context ./examples/codex` on 2026-10-13.
The installed version is `sha256:dce89132919c2fc0a9e35f81ab1b44c5f7b83777e62e57d883c67b8026e13e80`.
The build removed its temporary builder sandbox.
The logs confirm deletion of the old instance and reuse of its address by a new instance.
The instance-based host-key fix above removed the old address entry from this new instance's trust lookup after the CLI upgrade.
The Git pull and sandbox deletion then succeeded.

## CLI debug diagnostics

Command dispatch, start, completion, and failure diagnostics now use the debug log level.
Both CLI entry points enable that level only when `DEBUG=true`.
Normal command output and error messages remain visible.
Subprocess tests cover dispatch and helper logging, disabled values, successful commands, and errors.
This change is complete.
`gofmt`, `go vet ./...`, `go test ./...`, `make build`, and `git diff --check` pass.

## Git task workflow

Implemented the Git task workflow in README.md.
Create records the current repository and starting commit.
Start can attach a repository and prepares the guest's bare repository and task checkout.
The sandbox name identifies the branch and remote.
Task records use the daemon's stable instance ID to reject stale associations.
Private XDG state stores task records, operation locks, and reports.
Git and SSH transfers run in the operator CLI.
The daemon protocol uses its existing lifecycle methods.
Git is now a host package dependency.
The Codex guest's base rules cover the task branch and remote, committing and publishing work, and the private `/REPORT.md` handoff.

`sandboxctl pull` publishes committed guest work, fetches it into a host remote-tracking branch, and saves `/REPORT.md` when present.
It rejects dirty guest work and preserves host working files.
It temporarily starts stopped guests and restores stopped state after success or failure.
Cleanup uses its own deadline after cancellation.
Delete pulls every attached task before it deletes any selected sandbox.
Startup, pull, or stop failures retain the selected sandboxes.
Deleted tasks retain host branches, remotes, fetched results, and reports.

Focused tests use real Git repositories and guest shell scripts with a local transport and simulated daemon lifecycle.
They cover round trips, source and guest edits, branch and remote conflicts, stable identity, interrupted setup, cancellation, and guarded deletion.
The existing VM lifecycle checks run outside Git to avoid attaching the source checkout.
The implementation phase is complete.
`gofmt`, `go vet ./...`, `go test ./...`, `make build`, shell syntax checks, and `git diff --check` pass.
The next task is a live guest check on the supported host.
This command environment has no `/dev/kvm`, so live VM validation has not run.

## Codex guest image example

Added `examples/codex/Dockerfile` for a Ubuntu 24.04 guest with Codex 0.160.0.
The base image uses the existing pinned digest. The Codex archive uses the
SHA-256 digest from the official release metadata. The image uses the
standalone Linux x86-64 binary. It includes Git and ripgrep. The build
context includes only the Dockerfile, ignore file, and base `AGENTS.md`.
The image installs the rules in `/etc/skel/.codex/AGENTS.md` with mode `0600`.
The `.codex` directory uses mode `0700`. Provisioning copies these rules
into the `dev` account's Codex home when it creates the account.
Account credentials remain outside the image.

The Codex guest image example is complete. Added `--copy-codex-auth` to
`sandboxctl start`. The explicit flag replaces the guest login on each use.
The CLI reads `$CODEX_HOME/auth.json`, or `~/.codex/auth.json` by default.
It waits for the guest SSH port. It transfers the file through the normal
SSH alias after guest bootstrap. The transfer writes a temporary private
file, checks its byte count, and atomically replaces the guest file.
Directory mode is `0700`. File mode is `0600`. Credentials stay outside
the daemon API and image. A copy failure returns an error and leaves the
sandbox running. The flag cannot be combined with `--dry-run`.

The Codex login transfer and base rules example are complete. Parser and workflow tests
cover the explicit flag, startup errors, copy errors, and invalid daemon
responses. Transfer tests cover host paths, missing and invalid host files,
SSH wait cancellation, replacement, private permissions, partial input,
directory symlinks, and temporary-file cleanup. The next task is to build
the Codex image and confirm the startup transfer and subscription login in
a live guest.

Checks: The Codex archive digest matches official release metadata.
`gofmt`, `go vet ./...`, `go test ./...`, `make build`, and `git diff --check` passed.
The image build and live Codex login have not run.

The project now uses AGPL-3.0-or-later for original sandboxctl code. The
official GNU license text is in `LICENSE`. The Arch packages install it and
the third-party license texts. `docs/LICENSING.md` records the dependency
review and retained E2B, Firecracker, and BuildKit terms.

## User-owned image bundles

Use `$XDG_DATA_HOME/sandboxctl/images/NAME/DIGEST/` as the single user-owned
image location. Image staging, installation input, package building, and manual
builder tests use this path. The Arch package copies the staged builder bundle
into its temporary package data before installation. `sandboxd` keeps its
separate root-owned runtime image store under `/var/lib/sandboxctl/images/`.
Moved the existing builder bundle from `~/.cache/sandboxctl/images/` into the
XDG data image store. The stage script reuses the moved bundle. Shell syntax,
package metadata, `make check`, and `git diff --check` pass.

## Dockerfile image build

Image builds install the finished image by default. Set `--install=false` to
stage the image bundle without installing it.
Use `sandboxctl image list` to show installed image names and versions.
Use `sandboxctl image delete NAME` to remove an installed image that no saved
sandbox uses. Each image name has one installed version. The command finds that
version and leaves the staged bundle in place.

Implemented `sandboxctl image build` with local Dockerfile and context input.
BuildKit runs the Dockerfile inside the existing temporary builder VM. The
command infers `<context>/Dockerfile` when the user sets `--context` without
`--dockerfile`. Dockerfile mode defaults the context to `.`. The command
supports stage selection and explicit build arguments. It applies
Docker ignore rules, validates and hashes the context snapshot, and records
input digests in image provenance. Required sandboxctl provisioning follows
the selected build stage. `CMD` and `ENTRYPOINT` do not control VM startup.
The guest init system runs as PID 1.

The E2B review used the retained runtime checkout at commit
`47096f195aca4a545a9079b706efb3a18f51cf3f`. Its separate SDK translates a
limited Dockerfile into template steps. No new E2B code extraction is planned.

The live Dockerfile test passed on 2026-10-11. It verified local `COPY`,
executable file modes, multiple stages, build arguments, ignore rules, a
non-root final `USER`, container startup settings, guest bootstrap, public-key
SSH, systemd PID 1, and state persistence after restart. The test stopped and
deleted its VM. A guest-version mismatch check now runs during provisioning.
The live Dockerfile test passed again with that check enabled. The base-image
build passed again with exact guest-version validation. The live rejection
test rejected a failed `RUN`, an unpinned source, a `scratch` result, a guest
version mismatch, and an oversized Dockerfile. Each rejection published no
image manifest. See `tmp/logs/dockerfile-build-test.log`,
`tmp/logs/dockerfile-rejection-test.log`, and
`tmp/logs/image-build-test.log`. The clean Arch host acceptance test remains
deferred.

## Builder package distribution

The Arch package build now creates the pinned builder image and places its
manifest, kernel, initramfs, and SquashFS under `/usr/share/sandboxctl/builder`.
The package build uses Podman. The installed tool does not need Podman.
`sandboxctl init` verifies the package bundle and installs it through the
existing bounded gRPC stream. It skips an exact installed match. The bundle
name includes a fingerprint of all three boot artifacts, so package updates
can install a new builder without replacing an existing image. The image build
command reads the builder name and digest from the packaged manifest unless
the caller supplies both override options. `sandboxctl doctor` checks that the
packaged version is installed.

Last updated: 2026-10-11

Moved the Phase 1 proof runner and state-disk helpers to `tests/phase1/`.
The end-to-end suite and its manual test steps now live under `e2e/`. The root
Makefile includes the Phase 1 targets from `tests/phase1/phase1.make` and the
E2E targets from `e2e/e2e.make`. Active builder asset scripts remain under
`scripts/` because the package build uses them. `make e2e` requires an
installed package and configured host. It runs as the operator and uses the
installed `sandboxctl` command.

## Authority and order

`docs/SANDBOX-RUNTIME-V2.md` is the authoritative runtime design.
The risk-first storage proof must pass before sandboxd work starts.
Section 18 now records this gate.

## Current phase

The live guest bootstrap test passed on 2026-10-09. It confirmed the completion marker, runtime hostname, and public-key SSH login on first boot and after stopping and restarting the same VM. The SSH login after restart also confirmed that the guest host key remained stable. The test log is `tmp/logs/guest-bootstrap-test.log`.

The live builder seed VM test passed on 2026-10-09. The first boot exposed an interactive `systemd-firstboot` prompt. The image recipe now masks `systemd-firstboot.service`. The next test reached guest bootstrap and verified BuildKit v0.34.0 and SquashFS 4.6.1 over public-key SSH. The passing image version was `sha256:b939e76287883d113f12f7702d4f09ec5c24c2a16500e3220848a266225bc420`; the VM was `builder-1791558598-670462`. The log is `tmp/logs/builder-test.log`. The user stopped the VM. Each test uses a unique image name because sandboxd does not replace installed images. Safe stopped-instance deletion is implemented and covered by Go and RPC tests. The installed-daemon test passed on 2026-10-09. It created and deleted `delete-test-1791562050-734105` and verified that the instance was absent from the list. The test log is `tmp/logs/delete-test.log`. The test exposed a host-only deletion bug: Go's `net.InterfaceByName` missing-interface error was treated as a hard failure. The supervisor now enumerates interfaces, accepts an absent TAP, and has a regression test.

The live image-build test passed on 2026-10-09. It built and published `guest-1791582281-1022084` at `sha256:b545b7dac58a352e546fabc07d045520a075685195e94ad714b6e44b68609574`. The generated image booted and completed bootstrap over SSH. The same VM retained a guest file and bootstrap marker after a stop and restart. Two VMs from the same image ran at the same time with separate state disks. Both accepted key-based SSH and completed bootstrap. The guest with outbound HTTPS access could not connect to the peer's open SSH port. Both test VMs remain running: `builtguest-1791582281` and `builtguest-peer-1791582281`. The test log is `tmp/logs/image-build-test.log`.

Added the `ResetInstance` protocol method, `sandboxctl reset NAME`, atomic state-disk replacement, and supervisor checks that reject running or concurrently changing instances. Reset keeps the instance record, image, SSH key, and resource allocation. Added `make manual-reset-test` for a disposable guest. `make check` and `make build` pass. The live test result follows.

The live reset test passed on 2026-10-09 with image `guest-1791582281-1022084`. It wrote a marker to the guest, stopped the VM, reset its state disk, and started it again. The marker was absent after reset, while guest bootstrap and public-key SSH worked. The test stopped and deleted its disposable sandbox. The log is `tmp/logs/reset-test.log`. The script uses a private temporary known-hosts file and clears it after reset because the guest creates new SSH host keys.

Added private guest serial capture at `/var/lib/sandboxctl/instances/NAME/console.log`. The log is limited to 4 MiB and uses mode `0600`. Added `slog` events for CLI command and daemon RPC lifecycles. The guest bootstrap logs each setup stage without logging the key. The first metadata lookup failed because the daemon stored metadata at the MMDS root while the guest requested `/latest/meta-data/`. The daemon now nests the document under `latest/meta-data`, and a unit test checks this tree shape. The bootstrap service sends its logs to the serial console. The latest live run confirms that metadata lookup now succeeds and locates the next blocker at hostname setup.

The Phase 1 storage proof passed. Section 18 steps 1 through 3 are complete. Step 4 host installation and doctor support are implemented and still need final integration checks on a disposable Arch host.

Section 18, step 6 is complete. Stopped-instance creation now uses the daemon-owned allocator and store. sandboxd reads host allocation settings and image versions from root-owned state. It creates the sparse ext4 disk, stores the SSH public key, and commits a version-2 instance record. The `create` command sends operation data through gRPC and refreshes the SSH fragment from daemon state. Image installation now imports a pinned local bundle through a bounded gRPC stream. The remaining SSH command still uses operator XDG records. Keep the temporary single-root test until the jailed runtime passes integration testing.

Section 18, step 7 now has start and stop RPCs and a sandboxd runtime supervisor. The supervisor starts the SDK machine through the jailer, checks the Firecracker process UID and GID, saves process identity before it returns, and cleans up after exit. Stop requests use the Firecracker shutdown API. The explicit `--force` option sends SIGKILL after checking the saved process identity. The daemon can inspect saved process identity after a restart. Unit tests cover start, stop, force stop, failed start, and a saved process record.

The supervisor resolves fixed image artifacts from a root-owned image directory. It requires a pinned manifest version and read-only kernel, initramfs, and SquashFS files. Runtime records use mode `0600` and are removed with a directory sync. The CLI start and stop commands now call the daemon RPCs. They do not use the temporary runner for normal operation.

The jailed runtime prepares an instance TAP before Firecracker starts. It assigns the allocated UID and GID, applies the host `/30` address, and brings the link up. It removes the TAP after the VM stops and preserves runtime state if TAP cleanup fails. The user confirmed that jailed Firecracker starts the guest and that SSH reaches the guest at its allocated address. The new bootstrap path is intended to install the per-instance key before SSH starts; this remains unverified on a live guest. Guest Internet routing, forwarding, and firewall rules are not implemented.

The E2B runtime checkout is retained in ignored `tmp/e2b-runtime` at commit `47096f195aca4a545a9079b706efb3a18f51cf3f`. The license, extraction record, and first Debian/Ubuntu provisioning profile are in place. There is no build command, BuildKit VM orchestration, image manifest, or SquashFS finalizer yet.

Phase 1 storage proof is complete. It booted with read-only `/dev/vda` and writable `/dev/vdb`. A guest test file persisted across restart and disappeared after state-disk reset. The proof runner checked the SquashFS digest on each run.

## Current manual test report

Iteration 1, the manual one-VM test, passed on 2026-10-05.
The `manualtest` guest booted, accepted the managed SSH key as `ubuntu`, and retained a marker under `/home/ubuntu` after a stop and restart.
The complete output is in the ignored file `tmp/logs/manual-test.log`.

The manual test command prepares the private disk before boot. It fixes guest home ownership, installs the public key, disables password and keyboard-interactive SSH, and validates the SSH server configuration. It waits for SSH on both boots. It checks persistence outside `/tmp`, which is cleared at boot.

The current staged test bundle uses the upstream Ubuntu SquashFS without running the image provisioning script. The initramfs now installs the fixed bootstrap program and its systemd unit into the writable OverlayFS before it starts the guest. The bootstrap replaces image host keys on first boot and preserves generated keys on later boots. This path is only a test bridge; it does not replace the planned BuildKit image builder.

## Completed

- The project uses the Firecracker Go SDK v1.0.0.
- The SDK runtime is behind an application-facing `runtime.Backend` interface.
- The current Go project passed `go vet ./...`, `go test ./...`, and `go build ./...` after the baseline interface change and Phase 1 tooling.
- The required `gofmt`, `go vet ./...`, and `go test ./...` checks pass after the latest changes.
- `bash -n` passes for the Phase 1 scripts and the manual test scripts.
- `git diff --check` passes.
- Firecracker and jailer v1.17.0 are installed in the current development environment.
- Added `cmd/guest/overlay-init` to mount SquashFS read-only, mount ext4 state read-write, assemble OverlayFS, and switch to the guest init system.
- Added `tests/phase1/overlay-proof` to start a temporary SDK VM with SquashFS as `/dev/vda` and ext4 state as `/dev/vdb`.
- The initramfs builder passed archive validation.
- The initramfs archive contains the expected `init` executable and passes `gzip -t`.
- Added the pinned Firecracker CI v1.15 kernel and Ubuntu 24.04 SquashFS downloads. The active target is now `make download-builder-base-assets`.
- The overlay asset target records SHA-256 checksums and verifies them on later runs. It does not extract or convert the SquashFS image.
- The generated ext4 state image passed `e2fsck -fn` and uses mode `0600` in a mode `0700` directory.
- Unit tests cover drive access modes, mount layout, marker persistence, replacement state, and invalid marker data.
- The first guest boot reached the overlay and wrote its marker, then exposed the expected initramfs handoff defect: `pivot_root` returned `EINVAL` because the kernel's initramfs root cannot be detached with `pivot_root`.
- Replaced that handoff with the initramfs `switch_root` sequence: move the overlay mount over `/`, then `chroot` into it. The user must rebuild and rerun the VM proof to validate this change.
- Updated the proof runner to stop Firecracker if the guest does not stop after Ctrl-C.
- The user confirmed that the updated VM boot reaches the guest root prompt.
- The guest reports `/` as OverlayFS. A guest test file persisted across restart and disappeared after state-disk reset.
- Added a Protocol Buffers schema in `proto/control.proto`. Its protobuf package and generated Go package do not include `v1`.
- Added Buf v2 lint and generation configuration with pinned Go and gRPC plugins.
- Set Go 1.27 as the minimum version in `go.mod` and the README build instructions.
- Pinned gRPC v1.70.0 and compatible protobuf dependencies. Kept the Go module requirement at 1.27.0.
- Buf lint passes with explicit exceptions for the unversioned protobuf package and its root-level schema file.
- Added the `cmd/sandboxd` command and its Unix socket gRPC server package, plus a client package.
- Added API-version and request-ID metadata interceptors. The client creates a request ID for each call. The server validates the metadata and returns the request ID in response metadata.
- Added separate `Ping` and `HostStatus` RPCs. The schema has no negotiation RPC or versioned Go package path.
- Added message-size limits and operation deadlines. The socket server refuses to replace an existing path and uses mode `0600` for its socket.
- Added Unix socket client/server tests for both RPCs, request ID response metadata, invalid metadata, and socket permissions.
- Removed the thin client RPC wrappers. `client.Dial` now returns the gRPC connection and generated service client; callers set RPC deadlines.
- Added `sandboxd.socket` and `sandboxd.service` assets. The daemon can accept the systemd-passed Unix socket.
- Added activation checks for the process ID, descriptor count, and Unix socket type.
- `make check` passes after adding the systemd activation path.
- `systemd-analyze verify` passes for both units against a temporary install tree with the daemon binary and required systemd target units.
- Added `sandboxctl host install --operator USER` and `sandboxctl doctor` entry points.
- Moved command dispatch into `cmd/sandboxctl` and gave each CLI subcommand a package under `cmd/sandboxctl/<command>`.
- Kept reusable sandbox operations in `internal/app`; top-level dispatch and simple command validation live in the command packages.
- Moved CLI argument parsing and user-input validation into the matching `cmd/sandboxctl/<command>` package. Shared parsers and validators live in `internal/cmdutil`.
- Changed internal application and host-install APIs to accept typed options instead of command-line argument slices and raw range/address strings.
- Replaced the `sandboxctl` command switch with generic PATH-based dispatch to `sandboxctl-<command>` executables. Each command directory builds as a standalone `main` package. Nested `host install` dispatch uses `sandboxctl-host` and `sandboxctl-host-install`.
- Changed the detached runner to start the `sandboxctl-vmrun` executable through `PATH`. The `sandboxd` binary remains independent.
- Updated `make build` and the AUR package to build and install every `sandboxctl-*` helper.
- The external-dispatch tests confirm PATH discovery, unchanged command arguments, environment and working-directory inheritance, standard input/output/error, and child exit status.
- `make check`, `make build`, command dispatch smoke tests, shell syntax checks, and `git diff --check` pass.
- The host-install command validates the operator, identifier ranges, and address pool before calling the host installer.
- The host installer checks the Arch host, host-file range conflicts, route conflicts, package files, and matching Firecracker/jailer versions before it changes host state.
- The host installer creates the trusted group, root-owned state directories, host configuration, dedicated nftables table, IPv4 forwarding configuration, and enables the systemd socket.
- The host installer does not copy package-owned binaries or unit files.
- Added an initial `sandboxctl-git` AUR package draft. It builds and installs the CLI, daemon, and socket/service units. It depends on the Arch `firecracker` package, which includes both Firecracker and jailer.
- `makepkg --printsrcinfo`, `bash -n packaging/aur/PKGBUILD`, `make check`, and `make build` pass.
- `systemd-analyze verify` passes against a temporary package install tree.
- `make check` passes outside the sandbox. The sandbox blocks the Unix socket setup used by the new integration tests.
- `make check`, `make build`, and `git diff --check` pass after moving CLI parsing and validation to command packages.
- Added Go-style documentation comments to exported APIs across the internal packages. `go vet ./...` and `go test ./...` pass.
- Fixed the manual-test asset target so it creates a missing ext4 root drive from verified cached SquashFS assets instead of reporting an incomplete asset set as complete.
- Updated the manual VM test to use the downloaded Ubuntu image's existing `ubuntu` account. The test now requests public-key authentication so it does not fall back to a password prompt.
- Retained the E2B upstream checkout in ignored `tmp/e2b-runtime` and recorded commit `47096f195aca4a545a9079b706efb3a18f51cf3f` in `docs/E2B-SOURCE.md`.
- Copied the E2B Apache-2.0 license text to `third_party/licenses/e2b-apache-2.0.txt`.
- Added `cmd/sandboxctl/image/build/distro` with declared `os-release` parsing and Debian/Ubuntu package and init profiles. The profile omits E2B-only packages and boot services.
- Added `cmd/sandboxctl/image/build/provision` with a deterministic renderer and guest script. The script creates the `dev` account, configures passwordless sudo inside the guest, enforces key-only SSH, installs a metadata bootstrap service, validates early network values, and removes machine identity and host keys for per-VM regeneration.
- Added `internal/hoststate` with the `/var/lib/sandboxctl` directory layout, version-2 privileged instance records, random stable IDs, unknown-field preservation, validation, and atomic writes with file and directory sync.
- Updated host installation to create the image, instance, allocation, and jail directories as root-owned mode `0700`. This keeps privileged image and instance data outside operator-writable XDG directories.
- Added tests for private directory modes, symlink rejection, atomic instance record permissions, unknown-field preservation, schema rejection, and invalid names.
- Added separate `ListInstances` and `InspectInstance` RPCs. The daemon reads from the root-owned host-state store and maps invalid names and missing records to structured gRPC errors.
- Updated `cmd/sandboxd` to use `/var/lib/sandboxctl` for instance reads. Added Unix-socket integration tests for sorted listing, record fields, inspection, invalid names, and missing records.
- Regenerated Protocol Buffer Go files with the pinned Buf plugins.
- Changed `sandboxctl list` to read records from sandboxd. It supports the documented versioned JSON output.
- Removed the XDG record scan from `sandboxctl list`. The command does not import operator-side instance records.
- `make check`, `make build`, and `git diff --check` pass after the daemon-backed list change.
- The host-side image builder, OCI reference checks, BuildKit VM orchestration, image manifest, SquashFS finalizer, and integration boot are still open.
- Added a locked, atomic allocation registry for stable UID, GID, `/30`, guest IP, guest MAC, and TAP assignments. The allocator skips host TAP-name conflicts, detects corrupt duplicate assignments, and requires the documented cleanup conditions before it releases an allocation.
- Added allocator tests for stable assignments, uniqueness, TAP conflicts, exhaustion, release and reuse, concurrent requests, corrupt registries, and symlink rejection.
- `make check` and `git diff --check` pass after the host-state changes. The generated Debian and Ubuntu scripts pass POSIX shell syntax validation in the tests.
- `gofmt`, `go vet ./...`, and `go test ./...` pass after the allocator change. The sandbox blocked the Unix-socket integration tests. The full test suite passed after running it outside the sandbox.
- Added the `CreateInstance` gRPC method with operation-specific resource and image fields. The daemon validates the request and returns structured invalid, duplicate, missing-image, canceled, and deadline errors.
- Added host configuration loading with owner, file type, size, schema, identifier range, and address-pool checks. The host installer and daemon now share the same typed configuration.
- Added installed-image manifest lookup at `/var/lib/sandboxctl/images/NAME/manifest.json`.
- Added stopped-instance creation. It reserves resources under the allocation lock, creates a sparse ext4 state disk with mode `0600` and the allocated UID/GID, stores the guest public key, and atomically writes the instance record. A retry validates and reuses an existing ext4 disk. It refuses to truncate an unexpected file.
- The create command now accepts the documented CPU, memory, disk size, and image options. It prepares the operator key before the RPC and refreshes the managed SSH fragment after creation.
- Updated SSH configuration sync to read daemon instance state and use the `sandbox-NAME` alias.
- Added tests for config and image manifest validation, state disk formatting, create retries and duplicates, gRPC creation and structured errors, public-key storage, and SSH fragment generation.
- `make proto-lint`, `gofmt`, `go vet ./...`, and `go test ./...` pass. The complete Go checks passed outside the sandbox because its build cache is read-only.
- Added the jailed SDK configuration builder for the V2 boot layout. It gives the jailer the allocated UID and GID, uses cgroup version 2, and connects the read-only SquashFS and writable ext4 drives to the proven initramfs arguments.
- Added tests for jail identity, SDK chroot setup, boot inputs, drive access modes, TAP names, and rejected paths. `gofmt`, `go vet ./...`, `go test ./...`, and `git diff --check` pass.
- Confirmed that Firecracker and jailer are installed in this environment. `/dev/kvm` is absent, so live jailed VM validation cannot run here.
- Added separate `StartInstance` and `StopInstance` RPCs and regenerated the Protocol Buffer Go files with pinned Buf plugins.
- Added a sandboxd supervisor that starts and stops jailed SDK machines, verifies process UID and GID, records PID start time and host boot ID, handles daemon-restart stop requests, and removes failed or stopped jail state safely.
- Added strict lookup for pinned image artifacts. The kernel, initramfs, and SquashFS must be root-owned read-only regular files.
- Updated `sandboxctl start` and `sandboxctl stop` to use sandboxd. `stop --force` is required for SIGKILL.
- Added unit tests for runtime lifecycle operations, daemon RPC behavior, process identity, runtime record permissions, and boot artifact validation.
- `gofmt`, `go vet ./...`, `go test ./...`, `make proto-lint`, and `git diff --check` pass for this milestone.
- Added a manifest parser for pinned x86-64 images. It validates fixed artifact names, sizes, digests, guest details, and kernel arguments.
- The image version now derives from the manifest fields and artifact digests. The manifest rejects changed metadata or artifacts under an old version.
- Added streamed image import over gRPC. The CLI verifies local artifacts before transfer. sandboxd verifies chunk order, size, and digest, then atomically publishes read-only artifacts in the root-owned image store.
- Added `sandboxctl image install NAME --version VERSION`. It reads bundles from the XDG data directory.
- Added a local test-image staging script for the Phase 1 kernel, initramfs, and Ubuntu SquashFS. It now lives under `e2e/steps/` and runs through `make stage-bootstrap-test-image`.
- The jailed SDK configuration now uses the required kernel arguments from the validated image manifest.
- The initramfs archive now uses reproducible metadata. Running `make stage-bootstrap-test-image` twice produces the same pinned version and reuses the staged bundle.
- Gave the Phase 1 downloader a separate checksum file. This avoids failures when a manual test changes `root.ext4` in the shared asset directory.
- Added importer, manifest parser, and gRPC integration tests. The staged bundle target completed with a temporary XDG data directory.
- `gofmt`, `go vet ./...`, `go test ./...`, `make proto-lint`, `make build`, shell syntax checks, and `git diff --check` pass.
- The image CLI validated the staged bundle before it tried sandboxd. The daemon is not running here, so this host could not test the live import call.
- Added per-instance MMDS data to the jailed runtime. It includes the hostname, SSH user, public key, address, gateway, and interface. The SDK configures MMDS before it starts the guest.
- Added early guest IPv4 setup through the Firecracker SDK and an MMDS link-local route in the initramfs.
- Changed `sandboxctl ssh` to inspect daemon-owned instance state, refresh the generated SSH fragment, and execute the normal `sandbox-NAME` alias through OpenSSH configuration.
- A live host test confirmed that the jailed VM starts and accepts TCP connections on guest SSH. The guest did not accept the managed public key. The image provisioning path must include and run the bootstrap service before key login can pass.
- Added `cmd/sandboxd/network.TapManager` to create, validate, configure, and remove per-instance TAP devices with `/usr/bin/ip`. It checks allocated ownership and `/30` addressing and removes partial TAPs on setup failure.
- Wired TAP preparation and cleanup into the jailed runtime lifecycle. The runtime cleans stale TAP devices only after it confirms that the recorded Firecracker process is stopped. It keeps runtime records and jail state if TAP cleanup fails.
- Added tests for TAP command sequences, invalid addresses, owner mismatch, partial setup rollback, and runtime prepare/remove ordering.
- `go test ./cmd/sandboxd/network ./internal/runtime ./cmd/sandboxd` passed for this change.
- The Codex command environment does not expose `/dev/kvm`; the user's earlier serial and SSH VM tests confirm that the host can run VMs. The jailed runtime still needs a live boot test. Guest routing, forwarding, and firewall rules are still open.
- Added `make install` with a local `sandboxctl-local` package recipe. It uses `yay` to build this working tree. The local package test uses a bare Git remote beside the checkout. It does not use GitHub.
- Created `../sandboxctl-remote.git` as the local `local` remote and pushed `main` to it. `yay` installed `sandboxctl-local 0.1.0.r21.g983e9b0-1`; the installed CLI and daemon are available in `/usr/bin`.

## Decisions

- Keep the current SDK runner available until the jailed SDK runtime completes an integration boot.
- Keep Phase 1 as an isolated development experiment.
- Do not start sandboxd, its protocol, or the host installer before the Phase 1 exit conditions pass.
- The retired `download-test-assets` workflow converted the SquashFS into a writable ext4 root and selected changing remote artifacts. It did not match the Phase 1 image model. The workflow and its direct-VM test scripts are now removed.
- Use gRPC over the Unix socket. Use Buf with pinned remote plugins for schema lint and code generation.
- Use the Unix socket's owner, group, and mode as the only client authorization check. Membership in the `sandboxctl` group grants access to the full daemon API.
- Use separate typed RPC methods. Do not use a generic `Call` method or a negotiation RPC.
- Carry API versions and request IDs in gRPC metadata. Do not add these fields to messages.

## Blockers

The Codex command environment does not expose `/dev/kvm` and blocks `sudo`. The user's host terminal can run VMs and privileged commands. Do not infer host KVM availability from the Codex command environment.
Buf remote generation requires access to the Buf Schema Registry. `make generate` fails inside the sandbox because that network call is blocked. The generated files are present from a network-enabled run.
The earlier license blocker is resolved by `LICENSE` and `third_party/licenses/`.
The AUR draft was removed when the operator selected GitHub release packaging.

The user installed the package, imported the default image, and created `pkgtest`. Structured logging found and resolved the TAP parser issue: `ip tuntap show` returned the unrelated `tailscale0` line before the requested TAP. SDK debug output identified the Firecracker startup failure: the jailer reports `Failed to exec into Firecracker: No such file or directory`. The host's `/usr/bin/firecracker` is dynamically linked and requires `/lib64/ld-linux-x86-64.so.2`, which is absent from the jail. The official Firecracker jailer documentation requires a statically linked Firecracker binary paired with the same-version jailer. The package now bundles the official static v1.17.0 x86-64 pair. Host install and doctor now reject a dynamic Firecracker binary. The daemon now uses the package-installed paths under `/usr/lib/sandboxctl`.

The latest live bootstrap test passed on first boot and after a stop and restart. It loaded MMDS data, verified networking, created the `dev` account, prepared host keys, configured key-only SSH, applied the hostname without D-Bus, and wrote the completion marker. The second SSH login accepted the existing host key and used the persistent authorized key. This confirms that bootstrap state and host keys survive a VM restart.

The current image-build iteration has started. `cmd/sandboxctl/image/build/buildkit` renders a deterministic BuildKit Dockerfile from a digest-pinned base image and builds a bounded context from the distro profile and provisioning renderer. `cmd/sandboxctl/image/build/guest` provides bounded SSH commands, atomic context uploads, and size-limited streaming downloads from a private build directory. Its fixed service accepts only the `build` operation. It runs BuildKit, checks the built guest's account and SSH policy, removes machine-specific files, and creates a deterministic SquashFS. `cmd/sandboxctl/image/build/builder.Runner` now coordinates context upload, the fixed service call, SquashFS download, and VM cleanup through a narrow factory interface. The pinned builder seed image passed a live boot and tool check. This environment has no `/dev/kvm`; live builder validation needs the user's KVM host. Guest Internet routing remains unimplemented, so remote base-image pulls are not yet expected to work. Safe stopped-instance deletion is now implemented as a prerequisite. The production VM factory remains open.

Added `make download-builder-tools` to download the pinned BuildKit v0.34.0 Linux x86-64 archive into `tmp/config/builder/tools`. The script checks the release archive digest before extraction, verifies the expected binaries, and sets private directory and executable modes. It records the Apache-2.0 license source. The archive is downloaded and verified locally. `buildctl --version` and `buildkitd --version` report v0.34.0. Re-running the target verifies and reuses the archive. The target does not install tools on the host.

Added `make stage-builder-image` and a pinned Ubuntu 24.04 builder seed recipe. The recipe uses a pinned OCI digest and Ubuntu package snapshot. It includes the fixed guest build tools and creates a SquashFS bundle with the existing kernel and initramfs. The script verifies inputs, BuildKit archive digest, required guest paths, and artifact hashes. It stages the versioned bundle in the user's XDG data directory with private modes and reuses an existing bundle only after checksum validation. Added `make manual-builder-test` to create and start a VM and verify the bootstrap marker, BuildKit version, and SquashFS command over SSH. The live builder VM boot passed on 2026-10-09. `make check`, shell syntax checks, and `git diff --check` passed.

Updated the fixed guest build service to start BuildKit for one build, wait for its Unix socket, report bounded startup logs on failure, and stop the daemon when the build exits. It also checks that `buildctl` and `buildkitd` are v0.34.0. The service requires these tools at `/usr/local/bin` and `mksquashfs` at `/usr/bin`. `make check`, shell syntax checks, and `git diff --check` pass outside the sandbox. The reproducible builder image passed its live boot and tool check on 2026-10-09.

Added a static guest bootstrap binary to the Phase 1 initramfs. Before `switch_root`, overlay init installs the binary and a systemd unit into the writable guest layer. The unit is wanted by `sockets.target` and ordered before `ssh.socket` and `ssh.service`. The bootstrap reads token-protected Firecracker metadata, validates the fixed schema, checks the guest address and route, creates the `dev` account, installs the authorized key, enforces key-only SSH, sets the hostname, creates guest host keys, and writes an atomic completion marker. It removes host keys on first boot and retains them on later boots. The guest sudo policy is added only when the image has both `sudo` and `visudo`. The raw Ubuntu test image lacks those tools. The live test confirmed that the service did not write its expected files. Added private bounded serial capture and `slog` events to locate the failure on the next run. The manual test prints the console log path after a timeout and leaves the sandbox running for diagnosis.

## Next tasks

Added `cmd/sandboxctl/image/build/builder.DaemonVMFactory`. It creates a uniquely named builder with the installed sandboxd lifecycle API, checks that the installed image version matches the local bundle, scans the guest host key into a private temporary known-hosts file, waits for the bootstrap marker, and removes the builder on close. The factory starts only the fixed builder image. It adds no daemon RPC and requires no package reinstall.

Added `sandboxctl image build`. It validates the target name, distribution, guest version, pinned OCI base image, and builder version. It runs `builder.Runner` through the daemon-managed VM factory. The finalizer verifies the builder artifacts, writes the new SquashFS and manifest to a private staging directory, and publishes the bundle atomically. The command prints the install command. Unit tests cover builder cleanup, manifest integrity, bundle permissions, and input validation. Added `make manual-image-build` to stage and install a temporary builder image, run the workspace CLI against the installed daemon, and save a private log.

The first live image-build attempt installed the builder image and started a builder VM. The factory did not observe SSH readiness within two minutes. It stopped and deleted the VM. That cleanup also removed the guest console log. The daemon journal confirms Firecracker started but does not contain guest console output. Added SSH probe logging. The next run showed that `ssh-keyscan` emitted OpenSSH comment lines before the host key. The parser rejected those comments. Updated it to skip comments and reject output with no validated key. Added parser tests.

The host installer added its tagged rules ahead of UFW and Tailscale in the IPv4 FORWARD chain. The host policy remains `DROP`. The live image build confirmed guest DNS, registry access, BuildKit, SSH policy validation, SquashFS creation, bounded artifact download, and bundle publication. The builder's `mksquashfs` rejected `-repro-time`; changed it to the supported `-all-time 0`. The build finalizer now exposes only the artifact file to the SSH account. The generated image passed first boot, bootstrap, SSH, restart, state persistence, concurrent VM, separate-disk, outbound HTTPS, and guest isolation checks.

The latest host `doctor` run found two issues. It treated sandboxctl's active `/30` routes as address-pool conflicts, and it read root-owned image files directly. The route check now ignores only routes on generated sandbox TAP names. `sandboxd` now validates installed image bundles and returns their names and pinned versions through a new `ListImages` RPC. `doctor` uses that RPC. Added tests for both changes. `make check`, `make build`, and `git diff --check` pass with the default Go cache. The live daemon must be upgraded before rerunning `sandboxctl doctor`; two image-build acceptance VMs remain running and were not changed.

After the daemon upgrade, `sandboxctl doctor` passed every host check. The two running acceptance VMs use the same image version and different TAPs. Their Firecracker processes run under UIDs `200010` and `200011`, not root or the operator. OpenSSH resolved both generated aliases through the user's Include file. Both guests answered over public-key SSH. The first guest reached an HTTPS site and could not connect to the peer's SSH port. The runtime uses the same read-only SquashFS path for both VMs and creates a separate `state.ext4` path for each sandbox. The VMs remain running.

The clean-host Section 19 check is deferred until a clean supported Arch host is available. The current workstation is not a clean test host. The Section 18 runtime cleanup audit found no direct Herdr commands, SSH or SCP wrappers, or firectl runtime code.

Hardened the root `sandboxd` systemd service. The unit uses a private temporary directory, hides home directories, makes the filesystem read-only except for `/var/lib/sandboxctl`, and enables privilege, clock, kernel-log, module, personality, realtime, and syscall-architecture restrictions. `systemd-analyze verify` and `make check` pass. The live service reports exposure 7.4, down from 9.6. After package installation and daemon restart, a fresh VM started and accepted public-key SSH. `sandboxctl doctor` passed. The disposable VM was stopped and deleted. Restarting an older acceptance VM failed because its previous jail contained a stale Firecracker log FIFO; the fresh-VM test passed.

The clean-host Section 19 check is deferred until a clean supported Arch host is available. Completed the Section 18 cleanup audit. Removed the unused `herdr_label` field from the active instance model, create path, and example. The repository has no direct Herdr commands, SSH or SCP wrappers, or firectl runtime code. `make check` passes. The remaining acceptance task is the clean supported Arch host check in Section 19.

The live hardened-service smoke test exposed a restart failure on an older VM. Firecracker's SDK left its log FIFO in the jail root after the VM stopped. The supervisor now removes that exact FIFO during stale-resource cleanup, after it confirms that no process uses the VM's allocated UID or GID. It accepts the expected sandboxd or allocated VM ownership and rejects regular files, symlinks, and active identities. Runtime tests, `make check`, and `make build` pass. After package installation and daemon restart, the older acceptance VM started, accepted SSH, then accepted SSH again after a stop and restart. The VM was left stopped.
