# Arch package builds and GitHub releases

These build instructions require a source checkout.
Run build commands from the repository root unless a command states another directory.
See [CONTRIBUTING.md](../../CONTRIBUTING.md) for source builds and development checks.

## One split package recipe

Use `packaging/local/PKGBUILD` for both local installation and GitHub releases.
The recipe copies source files from the checkout into package staging.
It does not clone a second source repository.
The project does not maintain an Arch User Repository (AUR) package.

| Build | Package names | Version | Installation |
| --- | --- | --- | --- |
| `make install` | `sandboxctl-local`, `sandboxctl-builder-local` | Git commit count and abbreviated commit ID | `yay` builds and installs both packages |
| `make release TAG=v0.2.0` | `sandboxctl`, `sandboxctl-builder` | `0.2.0-1` | The user installs both downloaded packages with one `pacman -U` command |

The recipe uses a `pkgname` array and separate `package_<name>()` functions.
The application package contains the command-line interface (CLI), command helpers, daemon, systemd units, Firecracker, and jailer.
The builder package contains only the completed builder bundle and its license records.
Each package installs licenses in its own package directory.
The application requires the matching builder package version and release.
Build both packages together.
Install or upgrade both packages in the same transaction.
The local and release variants of each package conflict because they install the same files.
The package manager asks to replace the installed variants when you switch profiles.
Package builds embed the package version in the CLI and daemon.
Direct `make build` builds use `0.1.0-dev` unless `SANDBOXCTL_BUILD_VERSION` is set.

## Builder cache

Preserve the existing local cache:

```text
${XDG_DATA_HOME:-$HOME/.local/share}/sandboxctl/images/builder/sha256:DIGEST/
```

Each finished bundle contains these files:

```text
manifest.json
vmlinux
initramfs.cpio.gz
root.squashfs
```

The staging script selects a cached bundle when all four files are present, nonempty, and not symbolic links.
It returns before downloading builder inputs, compiling the guest initramfs, or starting Podman.
Source changes do not invalidate the cache automatically.
The staging lookup does not verify artifact checksums.
Run `make check-builder` to inspect the stored bundle metadata and file sizes.
Run `make clean-builder` to remove the local cache before a required rebuild.
This command does not remove installed daemon images.

On a cache miss, the staging script downloads the pinned inputs and builds the builder filesystem with Podman.
It saves the completed bundle under its content digest.
It removes temporary Podman storage after the build.
The shared build step compiles the application once and selects the builder bundle once.
The package recipe copies the selected bundle into its own staging directory.
Only the builder package installs a copy at `/usr/share/sandboxctl/builder`.
The cache and the installed package contain the same bundle data in separate directories.
Release artifact verification checks both package identities and the application dependency on the builder package.
It checks the packaged manifest identity and all three builder artifact checksums.
`sandboxctl init` verifies the bundle again before import.

## Local installation

Use an Arch Linux x86-64 host.
Install `yay` before local installation.
Commit checkout changes before running:

```sh
make install
```

`yay` installs package build dependencies through the local recipe.
These include Go, Podman, Python, SquashFS tools, cpio, curl, and gzip.
The build downloads and verifies the pinned Firecracker and jailer archive.
The builder staging script reuses the local cache when possible.
The package installation does not enable the daemon socket or import the builder image.

## Release build on an Arch host

Install the package and check tools first:

```sh
sudo pacman -S --needed base-devel git go buf podman python squashfs-tools openssh nftables iproute2 e2fsprogs cpio curl gzip zstd
```

Configure rootless Podman for the build user.
The user needs subordinate user and group ID ranges in `/etc/subuid` and `/etc/subgid`.
The release build does not need `/dev/kvm` or a running sandboxd service.
It builds the seed image with Podman.

Create and push an existing `vMAJOR.MINOR.PATCH` tag through your normal Git release process.
Check out that tag with no uncommitted changes:

```sh
git checkout v0.2.0
make release TAG=v0.2.0
```

Run the build as an unprivileged user.
The script requires the tag to identify `HEAD`.
It runs `make check` before package creation.
It checks for source changes again after the checks.
It builds from the checkout with the single package recipe.
It uses temporary package staging and the existing builder cache.
It verifies both package names, versions, architectures, dependencies, and builder contents.

The default output directory is `dist/v0.2.0/`.
The output contains:

```text
sandboxctl-x86_64.pkg.tar.zst
sandboxctl-builder-x86_64.pkg.tar.zst
SHA256SUMS
release.json
```

Release asset filenames stay the same across versions.
The build renames the `makepkg` archives before release verification.
The package metadata retains the tagged version.
`release.json` uses schema version 2.
It records the tag, source commit, builder identity, and both package checksums in a `packages` array.
`SHA256SUMS` covers both packages and `release.json`.
Verification rejects missing packages, additional archives, mismatched versions, and mixed package contents.
The script refuses to overwrite an existing output directory.
A failed build can leave a partial output directory.
Use another output directory for a retry:

```sh
./scripts/build-release.sh v0.2.0 /path/to/new-output
```

To upload verified files to a draft release, install the GitHub CLI and authenticate it.
Run:

```sh
./scripts/publish-release.sh v0.2.0 dist/v0.2.0 OWNER/REPO
```

Replace `OWNER/REPO` with the GitHub repository.
The script verifies both local packages and checksums before uploading.
It requires the remote tag to match the recorded source commit.
It uploads both archives and creates a draft release or replaces files in an existing draft.
It refuses to replace files in a published release.
Review and publish the draft through GitHub when it is ready.

## GitHub Actions

The `Build GitHub release` workflow uses a GitHub-hosted Ubuntu runner with an Arch Linux container.
The container permits nested user namespaces for rootless Podman.
Package creation runs as an unprivileged `build` user.
Podman uses the `vfs` storage driver to avoid nested overlay mounts.
Buildah uses a separate Docker volume at `/var/tmp` for its context overlay scratch space.
The workflow checks a rootless build before compiling the package.
Build commands use Buildah chroot isolation and the outer CI container's network namespace.
The build job has read access to repository contents.
Only the draft upload job has write access to repository contents.
Action versions are pinned to commit IDs.

Run the workflow manually from the default branch.
Set `tag` to an existing version tag that contains the release scripts.
The build and upload jobs check out the exact tag.
The workflow creates a draft after all checks and package verification pass.
It does not create a tag or publish the release.

The workflow saves only the completed builder bundles through the Actions cache.
The cache key uses Linux, x86-64, and `builder_cache_revision`.
It does not use a source hash.
Keep the revision unchanged to reuse a builder across releases.
Increase the revision when a changed builder recipe or guest bootstrap needs a fresh image.
Dispatching from the default branch keeps release runs in the same cache scope.
An unavailable or evicted cache causes a normal builder rebuild.
Temporary Podman storage, credentials, and sandbox state are not cached.

## User installation and host setup

Download both packages from the latest published release.
Replace `OWNER/REPO` with the project repository.
Run these commands in a new directory:

```sh
curl -fLO https://github.com/OWNER/REPO/releases/latest/download/sandboxctl-x86_64.pkg.tar.zst
curl -fLO https://github.com/OWNER/REPO/releases/latest/download/sandboxctl-builder-x86_64.pkg.tar.zst
sudo pacman -U ./sandboxctl-x86_64.pkg.tar.zst ./sandboxctl-builder-x86_64.pkg.tar.zst
```

No release version is required in the commands.
The links select published releases and exclude drafts.
Release packages are currently unsigned.
Local installation uses the host's `LocalFileSigLevel` policy.
Direct installation from URLs requires signatures trusted by the host's remote-package policy.
Install both packages in the same transaction.
Users do not need Go, Podman, or the other image build tools.
The package does not run host setup during installation.
Configure the host explicitly:

```sh
sudo sandboxctl host install --operator "$USER"
```

Start a new login session if this command changes group membership.
Initialize operator files and import the packaged builder:

```sh
sandboxctl init
sandboxctl doctor
```

`sandboxctl init` verifies the packaged bundle and sends it to sandboxd.
The daemon installs a private copy in its root-owned image store.
An existing builder with the same name and version can be reused.
A different installed version causes an image-name conflict.
Package upgrades do not replace that image automatically.
Use the guarded image removal and installation commands when you need to replace it.
Image deletion refuses to remove an image used by a saved sandbox.
Routine sandbox operations do not require `sudo`.
