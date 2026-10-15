# Implementation progress

## Active phase

The active phase is review of the first draft release.
Release packaging is implemented.
The GitHub build and draft creation for `v0.1.0` passed.
The clean supported Arch host acceptance check in section 19 also remains open.

## Documentation privacy review

Reviewed project documentation, example instructions, package instructions, and license records.
Removed personal task names, local image identifiers, workstation inventories, network observations, installation timelines, and account status.
Progress and continuation notes now describe implementation status and acceptance results.
Command examples use generic names, standard guest paths, and documented product defaults.
Public upstream source identifiers and required license notices remain available.
Related test fixtures now use a generic operator name and interface name.
The remaining obsolete AUR metadata and module-guide references have been removed.
The privacy scan covered 59 tracked documentation and license files.
No known personal identifiers, private keys, or actual host-key fingerprints were found in the reviewed text after cleanup.
Remaining addresses are product defaults, public DNS servers, or loopback examples.
`make check` and `git diff --check` pass after cleanup.
The documentation privacy review is complete.
Publication retains the existing Git history.
Earlier Git revisions retain the removed text.
The cleaned documentation and release workflow are pushed to the default branch.
The release workflow is active.
The `v0.1.0` draft is ready for maintainer review.
The release workflow uses `.github/workflows/release.yaml`.
Project YAML files use the `.yaml` extension.

## Package builds and releases

Use one checkout-based recipe at `packaging/local/PKGBUILD`.
The separate Arch User Repository (AUR) recipe has been removed.
Local installation builds `sandboxctl-local` with a Git-derived version.
Release builds select `sandboxctl` with the existing version tag.
Package builds embed the selected version in the CLI and daemon.
Both profiles include the builder manifest, kernel, initramfs, and SquashFS at `/usr/share/sandboxctl/builder`.

The existing XDG cache retains its file-presence lookup and manual invalidation through `make clean-builder`.
Source changes do not trigger automatic builder rebuilds.
The builder staging script and package cache lookup remain unchanged.
Package installation, explicit host setup, and verified builder import remain separate operations.
Image-name conflicts require the existing guarded image commands.

`scripts/build-release.sh` requires a clean checkout at the version tag and runs `make check`.
It creates a verified package, `SHA256SUMS`, and `release.json` in a new output directory.
`scripts/publish-release.sh` verifies these files and the remote tag commit before draft upload.
It supports lightweight and annotated tags.
It refuses to replace files in a published release.
The manual workflow uses a GitHub-hosted runner with an Arch Linux container.
It builds as an unprivileged user with rootless Podman and the `vfs` storage driver.
Only the draft upload job receives repository write permission.
Actions use pinned commit IDs.
The workflow caches only completed builder bundles with a manual cache revision.

Fixture tests cover both package profiles, cache reuse, staging, paths with spaces, incomplete bundles, symbolic links, version parsing, tag identity, clean checkouts, output refusal, checksums, builder corruption, and draft uploads.
Formatting, vet, Go tests, Protocol Buffer lint, shell syntax checks, workflow YAML parsing, and whitespace checks pass.
A stamped build reports the selected version.
GitHub validation exposed a test fixture that depended on the caller's file mask.
The fixture now sets its requested mode explicitly.
A subprocess regression check runs the metadata test with file mask `077`.
CI setup restores mapping helper file capabilities for rootless user namespaces.
CI now mounts a Docker volume at `/var/tmp` for build scratch space.
The preflight check builds a small image without network access before package compilation.
CI uses Buildah chroot isolation and shares the outer container's network namespace for build commands.
These environment changes preserve the tagged package source and builder cache strategy.
The complete GitHub workflow passed with these corrections.
The `v0.1.0` draft contains the Arch package, `SHA256SUMS`, and `release.json`.
Both jobs verified the package and builder before draft upload.
GitHub saved the completed builder cache under the default branch with manual revision `1`.
The release remains a draft.
The next task is maintainer review before publication.

## Port forwarding

`sandboxctl forward NAME [HOST_PORT:]GUEST_PORT` is implemented.
The daemon saves mappings and the trusted guest host key in version 3 instance records.
Version 2 records migrate when read.
Host ports are optional.
The daemon binds loopback listeners and selects another port when a preferred or saved port is occupied.
It retains the listener to avoid a port-selection race.

The CLI installs a restricted daemon forwarding public key through operator SSH access.
The daemon owns a separate private key for each stable instance identifier.
It does not read operator private keys.
The guest key permits local TCP forwarding to loopback ports only.
It cannot request a terminal or run commands.
Native Go SSH carries forwarded connections.
Each sandbox shares one daemon SSH connection.
Stop closes tunnels and retains mappings.
Start and daemon reconciliation restore mappings for verified running instances.
Reset clears the forwarding host-key pin and retains mappings.
Removal closes listeners and active streams.

Tests cover parser bounds, migration, key privacy, port collisions, saved-port reuse, restart, process identity, key changes, limits, state write failures, cancellation, removal, and stop cleanup.
Native SSH tests verify connection sharing and byte transfer.
An OpenSSH fixture verifies the restricted-key policy.
Integration validation passed forwarding registration and an HTTP request through a host listener.
Required checks and race checks pass for this feature.

## SSH host-key identity and ordering

Host-key trust uses the daemon's stable instance identifier through `HostKeyAlias`.
Stop and start retain trust.
Recreated sandboxes get a new identity even when a name or address is reused.
Reset requires explicit verified host-key replacement.
Address-based entries do not transfer trust to a new instance.
Changed keys for the same instance remain rejected.

Native SSH follows OpenSSH's supported host-key preference order.
Trusted key algorithms are promoted without changing their relative order.
RSA keys use the corresponding SHA-512 and SHA-256 signature algorithms.
The daemon selects preferences from its saved host-key pin.
The CLI selects preferences from managed known-hosts entries.
Exact key verification remains required after negotiation.
Tests cover unknown hosts, multiple keys, hashed aliases, unrelated instances, old address entries, Ed25519, ECDSA, RSA, and changed keys.
A real OpenSSH fixture verifies selection and restricted forwarding access.

Managed known-hosts files must be owned by the operator and use mode `0600`.
The CLI repairs an operator-owned regular file before use.
It rejects symbolic links, special files, oversized files, and files owned by another user.
Regression tests and required checks pass.

## Codex guest image and credential transfer

The Codex example uses a pinned Ubuntu base and the documented native installer.
It installs the complete Codex package, Git, and ripgrep without Node.js.
The image contains base rules in `/etc/skel/.codex/AGENTS.md`.
Provisioning copies them into the guest account.
Private directories use mode `0700`.
Private files use mode `0600`.
Account credentials remain outside the image.

`sandboxctl start --copy-codex-auth` explicitly transfers a file-based host login.
The CLI reads `$CODEX_HOME/auth.json`, or `~/.codex/auth.json` by default.
It waits for guest SSH readiness and transfers the file through SSH standard input.
The guest checks the byte count and atomically replaces its login file.
Credentials remain outside the daemon API.
Transfer failure returns an error and leaves the sandbox running.
The flag cannot be combined with `--dry-run`.
Parser and transfer tests cover missing files, invalid files, cancellation, replacement, private permissions, partial input, symbolic links, and temporary-file cleanup.
Example image builds and normal Codex startup have passed integration checks.
A full subscription login and coding task round trip remain separate acceptance checks.

## Guest filesystem ownership

Guest root filesystem export uses tar instead of BuildKit's local directory exporter.
Extraction preserves numeric owners, file modes, and safe symbolic links.
This prevents private skeleton files from retaining root ownership when copied into the guest account.
The finalizer verifies home and SSH directory ownership before creating SquashFS.
Render tests and image build validation pass.

## Git task workflow

Create records the current repository and starting commit.
Start can attach a repository and prepare the guest bare repository and checkout.
The sandbox name identifies the branch and remote.
Stable instance identifiers reject stale task associations.
Private XDG state stores task records, operation locks, and reports.
Git and SSH transfers run in the operator CLI.
The daemon protocol retains its existing lifecycle methods.
Guest rules cover the task branch, commits, publication, and `/REPORT.md`.

`sandboxctl pull` publishes committed guest work and fetches it into a host remote-tracking branch.
It saves `/REPORT.md` when present.
It rejects dirty guest work and preserves host working files.
It temporarily starts stopped guests and restores their stopped state after success or failure.
Cleanup uses an independent deadline after cancellation.
Delete pulls every attached task before deleting any selected sandbox.
Startup, pull, or stop failures retain the selected sandboxes.
Deleted tasks retain host branches, remotes, fetched results, and reports.
Tests use real Git repositories with temporary transports and simulated daemon lifecycle.
They cover round trips, edits, conflicts, stable identity, interrupted setup, cancellation, and guarded deletion.
Git pull before sandbox deletion has passed integration validation.
A full guest coding task round trip remains open.

## Image builds and package distribution

`sandboxctl image build` supports digest-pinned base images and local Dockerfile contexts.
The packaged builder runs BuildKit inside a temporary VM.
The command applies required guest provisioning after the selected build stage.
Dockerfile mode supports stage selection and explicit build arguments.
Context snapshots apply Docker ignore rules, validate safe paths, and record input digests.
Relative context paths resolve before containment checks.
Container startup settings do not control VM startup.
The guest init system runs as process identifier (PID) 1.
The build validates the distribution and exact guest version before publication.

Image builds install the finished image by default.
`--install=false` stages the bundle without installation.
User-owned bundles use `$XDG_DATA_HOME/sandboxctl/images/NAME/DIGEST/`.
The daemon keeps a separate root-owned image store.
Image listing and deletion use daemon validation.
Deletion rejects images used by saved sandboxes.
The package includes the completed builder bundle.
`sandboxctl init` verifies and imports it through the bounded gRPC stream.
Podman is required for package image construction only.
Unit tests cover options, paths, ignore rules, symbolic links, file changes, archive limits, policies, transfer errors, build errors, cancellation, and provenance.
VM checks passed base-image builds, Dockerfile stages, file modes, provisioning, SSH, restart persistence, and rejected inputs.
Rejected builds publish no image manifest.
The E2B-derived source record and license notices remain in the project.

## Runtime implementation sequence

The Phase 1 SquashFS and OverlayFS proof passed before control-plane implementation.
The proof checks read-only shared storage, a private writable state disk, and guest-only OverlayFS assembly.
The temporary runner remains under `tests/phase1/` for proof validation.
It is not the production runtime.

Section 18 is implemented.
The control API uses Protocol Buffers and gRPC over a Unix socket.
Interceptors validate API version and request ID metadata.
The privileged daemon uses systemd socket activation.
Host installation configures validated operator access, protected state, and network policy.
The package includes matching static Firecracker and jailer binaries.
The jailed runtime uses a separate allocated user and group identity for each sandbox.
Allocation registries track user IDs, group IDs, addresses, and TAP devices.
They use locked atomic state updates and reject duplicate or corrupt assignments.
Stopped-instance creation prepares the private state disk and image references.
The supervisor verifies process identity before lifecycle operations and stale-resource cleanup.
Deletion refuses running sandboxes.
Reset replaces only a stopped sandbox's writable state disk.

Guest bootstrap reads token-protected metadata and configures hostname, network, account, and SSH access.
It creates host keys on first boot and retains them after restart.
The image finalizer checks guest distribution, init, account, ownership, and SSH policy.
Routed TAP networking provides guest outbound access and isolation.
Host checks distinguish managed routes from address-pool conflicts.
The daemon validates protected image files on behalf of the CLI.
Jailed boot, guest bootstrap, state persistence, concurrent guests, network isolation, stopped deletion, and reset have passed integration checks.
The daemon service uses systemd filesystem and process restrictions.
Runtime cleanup removes stale Firecracker log FIFOs only after identity checks.
Tests reject active identities, unexpected files, and symbolic links during cleanup.
Direct Herdr commands, SSH wrappers, and firectl runtime code are absent from the active implementation.
The clean supported Arch host acceptance check in section 19 remains open.

## Checks and next tasks

Use `make check` for formatting, vet, Go tests, and Protocol Buffer lint.
Use `make build` for binary compilation.
Use `git diff --check` for whitespace validation.
Use `e2e/README.md` for installed-package and manual VM checks.
Keep private test outputs outside tracked documents.
Record acceptance results without personal host inventories or account details.
Retain generic examples and product defaults in future documentation updates.
Run the first GitHub release workflow after choosing a version tag.
Review the draft before publication.
Complete the full guest coding task and clean supported Arch host acceptance checks when suitable test environments are available.
