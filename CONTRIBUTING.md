# Contributing

These instructions require a source checkout.
Run all `make` commands from the repository root.
Use the [README](README.md) to install and use the published packages.

## Build requirements

Use an Arch Linux x86-64 host.
Install Go 1.27 or later, Make, and Buf for source builds and checks.
Package fixture checks also use `makepkg`, fakeroot, Python, tar, and zstd.
The fixtures skip checks that need a missing tool.
See [MODULES.md](MODULES.md) for files and checks that apply to each change area.
Read [docs/SANDBOX-RUNTIME-V2.md](docs/SANDBOX-RUNTIME-V2.md) before changing runtime behavior.

## Install from a checkout

Install `yay` before local installation.
Commit checkout changes before running:

```sh
make install
```

The checkout must have no uncommitted changes.
The target uses `yay` to build and install `sandboxctl-local` and `sandboxctl-builder-local` from one split PKGBUILD.
The application requires the matching version of the builder package.
The build downloads the pinned Firecracker and jailer release.
It reuses a completed builder bundle from the existing local cache.
It builds the bundle with Podman on a cache miss.
Podman is a package build dependency.
Package installation does not configure the host or import the builder image.
Continue with [host configuration](README.md#configure-the-host) after installation.

## Package builds and releases

See the [package guide](packaging/local/README.md) for build dependencies, builder cache management, release builds, and draft uploads.
The local and release profiles use the same split PKGBUILD.
Keep the existing file-presence cache lookup and manual invalidation.
Source changes do not invalidate the builder cache automatically.
Package users do not need the build tools or cache-management commands.

## Development checks

Run the code checks and build the local commands with:

```sh
make check
make build
```

The end-to-end (E2E) suite exercises the installed command and daemon.
Install the Arch package and configure the host first.
Run the suite as the configured operator:

```sh
make e2e
```

The suite does not install packages or use `sudo`.
It uses a private user-data directory under `tmp/e2e/` and removes test resources after successful checks.
See [e2e/README.md](e2e/README.md) for requirements and details.

You can also run one manual virtual machine (VM) check at a time.
These targets need Kernel-based Virtual Machine (KVM) access and an installed, configured `sandboxd` service:

- `make manual-bootstrap-test` checks guest bootstrap and SSH.
- `make manual-builder-test` checks the builder image and its tools.
- `make manual-image-build` checks the guest image build pipeline and stages its output bundle.
- `make manual-dockerfile-build` checks a local Dockerfile build, context filtering, build arguments, VM boot, SSH, and state persistence.
- `make manual-dockerfile-rejections` checks invalid Dockerfile builds, guest metadata, and context size limits.
  It confirms that failed builds do not publish an image.

These manual targets check sandboxd state and disk operations.
They need an installed, configured sandboxd service.
The reset test also starts a guest and needs KVM access:

- `make manual-delete-test` checks stopped-sandbox deletion.
- `make manual-reset-test` checks state-disk reset.
  Set `IMAGE` to select an installed image other than `default`.

