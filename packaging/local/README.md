# Arch package builds and GitHub releases

## One package recipe

Use `packaging/local/PKGBUILD` for both local installation and GitHub releases.
The recipe copies source files from the checkout into package staging.
It does not clone a second source repository.
The project does not maintain an Arch User Repository (AUR) package.

| Build | Package name | Version | Installation |
| --- | --- | --- | --- |
| `make install` | `sandboxctl-local` | Git commit count and abbreviated commit ID | `yay` builds and installs the package |
| `make release TAG=v0.2.0` | `sandboxctl` | `0.2.0-1` | The user installs the downloaded package with `pacman -U` |

Both profiles package the same CLI, command helpers, daemon, systemd units, Firecracker, jailer, licenses, and builder bundle.
The two packages conflict because they install the same files.
The package manager asks to replace the installed profile when you switch profiles.
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
The package recipe copies the selected bundle into its own staging directory.
It installs a copy at `/usr/share/sandboxctl/builder`.
The cache and the installed package contain the same bundle data in separate directories.
Release artifact verification checks the packaged manifest identity and all three artifact checksums.
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
It verifies the package name, version, architecture, and builder contents.

The default output directory is `dist/v0.2.0/`.
The output contains:

```text
sandboxctl-0.2.0-1-x86_64.pkg.tar.zst
SHA256SUMS
release.json
```

`release.json` records the tag, source commit, package checksum, and builder identity.
`SHA256SUMS` covers the package and `release.json`.
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
The script verifies the local package and checksums before uploading.
It requires the remote tag to match the recorded source commit.
It creates a draft release or replaces files in an existing draft.
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

Download the package, `SHA256SUMS`, and `release.json` from the selected published release.
Put them in one directory.
Verify the files and install the package:

```sh
sha256sum -c SHA256SUMS
sudo pacman -U ./sandboxctl-0.2.0-1-x86_64.pkg.tar.zst
```

Use the filename from the selected release.
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
