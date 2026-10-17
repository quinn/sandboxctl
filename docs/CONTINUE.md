# Continuation

## Active work

The active phase is maintainer review of the verified signed release.
Read `packaging/local/README.md` for the package build and installation plan.
Use `docs/PROGRESS.md` for completed work, checks, and remaining validation.
Use `docs/SANDBOX-RUNTIME-V2.md` as the authoritative runtime specification.

## Package builds and releases

Use `packaging/local/PKGBUILD` for local installation and GitHub releases.
The split recipe produces application and builder packages from shared build inputs.
Local builds use `sandboxctl-local` and `sandboxctl-builder-local`.
Release builds use `sandboxctl` and `sandboxctl-builder`.
The application requires the matching builder package version and release.
Only the builder package owns `/usr/share/sandboxctl/builder`.
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
The `v0.1.0` workflow passed with the split-package layout.
The draft contains both package archives, checksums, and release metadata.
The updated verifier requires both split archives and records their checksums in release metadata schema version 2.
The checksum file covers both archives and the metadata file.
Real `makepkg` fixture builds and release verification pass for the split layout.
The completed builder cache is saved under the default branch with manual revision `1`.
CI restores mapping helper capabilities, uses separate Buildah scratch storage, and shares the outer container's network namespace.
A fixture permission correction has a subprocess regression check for private file masks.
The workflow is `.github/workflows/release.yaml`.
The full checks and the package tests under file mask `077` pass.
The split-package workflow restored the completed builder cache and skipped builder image construction.
The installation guides select the latest published release without a version argument.
They document one-time signing-key trust and one URL-based installation transaction.
Fixed release asset names preserve the tagged version inside each package.
The dedicated project key and signing subkey are created.
The `releases` GitHub environment is restricted to the default `main` branch.
Its signing key and passphrase secrets are configured.
Only the signing job receives these secrets.
The package build job produces unsigned archives and does not receive signing credentials.
The signing and draft upload jobs require signatures that match the pinned project public key.
Signed-package fixtures and the required checks pass.
The build, signing, and draft upload jobs passed.
Independent checks of the downloaded packages, builder artifacts, checksums, and signatures passed.
The public key and source commit match the pinned key and release tag.
The draft includes both packages, both signatures, the public key, and release metadata.
The existing builder cache was reused.
Review the unpublished draft before publication.
Latest-release downloads require a published release.
The fixed-name draft rebuild passed.
Only the two fixed-name packages and release metadata remain in the draft.
The uploaded package digests and tag commit match the metadata.
The package split and release validation are complete.
Review the draft before publication.

## User and contributor guides

Keep installed-user instructions in the README.
Use `CONTRIBUTING.md` for source builds, local installation, and development checks.
Use `packaging/local/README.md` for package builds, releases, and builder cache management.
The README must not require a Makefile for routine use.
The Codex example remains in the source repository.
Link to the example folder and state the repository-root requirement.
Do not add example distribution or new example-management commands.
Documentation links, shell syntax, and whitespace checks pass.
The draft refresh passed and includes the corrected README.
The package digests and source commit match the uploaded metadata.
The builder cache was reused.

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
