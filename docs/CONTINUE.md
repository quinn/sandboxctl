# Continuation

## Active work

The active phase is GitHub release validation.
Read `packaging/local/README.md` for the package build and installation plan.
Use `docs/PROGRESS.md` for completed work, checks, and remaining validation.
Use `docs/SANDBOX-RUNTIME-V2.md` as the authoritative runtime specification.

## Package builds and releases

Use `packaging/local/PKGBUILD` for local installation and GitHub releases.
The separate Arch User Repository (AUR) recipe has been removed.
The release workflow uses a GitHub-hosted runner with an Arch Linux container.
It builds an existing version tag and creates a draft release.
Release scripts verify package checksums, builder artifacts, and the remote tag commit before upload.
They refuse to replace files in a published release.
Publication remains a separate maintainer action.

Keep completed builder bundles under the XDG data directory.
Preserve the existing file-presence lookup and manual `make clean-builder` invalidation.
The workflow cache uses a manual revision shared across default-branch runs.
Source changes do not invalidate the builder cache automatically.
Package installation, explicit host setup, and builder import remain separate operations.
An installed image with a different version causes an image-name conflict.
Use the guarded image commands when replacement is required.

Package and release fixture tests pass.
Formatting, vet, Go tests, Protocol Buffer lint, shell syntax checks, and workflow YAML parsing pass.
A stamped build reports the selected version through `sandboxctl version`.
The first GitHub run exposed a fixture that depended on the caller's file mask.
The fixture now sets its intended mode explicitly and has a subprocess regression check.
The complete release package build remains unverified.
The next release task is to retry `v0.1.0` with the correction through `.github/workflows/release.yaml`.

## Documentation privacy

Project documents use generic examples and product defaults.
Do not record developer identities, personal task names, workstation configuration, account status, or local instance inventories.
Keep required upstream license notices and source attribution.
The documentation privacy review is complete.
Publication retains the existing Git history.
Earlier Git revisions can retain removed information.

## Port forwarding and SSH

`sandboxctl forward NAME [HOST_PORT:]GUEST_PORT` is implemented.
The daemon saves mappings in version 3 instance records.
It migrates version 2 records when it reads them.
It binds host loopback ports and uses native Go SSH with a separate forwarding key.
The CLI installs the restricted public key through operator SSH access.
The daemon does not read the operator private key.
Native SSH follows OpenSSH's host-key order with trusted-key algorithms first.
Exact host-key verification remains required.
Host-key trust uses the stable instance identifier.
Recreated sandboxes do not inherit trust from reused names or addresses.
Managed known-hosts files require private ownership and mode `0600`.

Stop closes tunnels and retains saved mappings.
Start and daemon reconciliation restore mappings for verified running instances.
Reset retains mappings and clears the forwarding host-key pin.
`--list` reports saved mappings and connection errors.
`--remove GUEST_PORT` closes the listener and active streams.
Forwarding registration and an HTTP request through a host listener have passed integration validation.

## Git tasks and Codex guests

Create and start attach one repository to a sandbox.
The branch and remote use the sandbox name.
The operator CLI stores task associations in private XDG state and uses Git over SSH.
`sandboxctl pull` retrieves committed work and the guest report.
Delete pulls all attached tasks before removing any selected sandbox.

The Codex example uses the documented native installer with a pinned version.
It installs the complete package without Node.js.
Guest image export preserves numeric ownership and file modes.
The build validates guest home and SSH ownership before creating SquashFS.
The explicit `--copy-codex-auth` flag transfers file-based credentials over SSH.
Credentials remain outside the image and daemon API.
A full subscription login and coding task round trip remain separate acceptance checks.

## Runtime validation

Section 18 of the runtime specification is complete.
The Phase 1 SquashFS and OverlayFS proof passed.
Jailed runtime, guest bootstrap, restart, state persistence, deletion, reset, network isolation, and image build checks have passed.
Dockerfile acceptance checks cover multiple stages, context filtering, guest provisioning, and rejected inputs.
The runtime removes stale log FIFOs only after verifying that no process uses the allocated identity.
The daemon service has systemd filesystem and process restrictions.
The clean supported Arch host acceptance check in section 19 remains open.
Run manual checks with the targets documented in `e2e/README.md`.
