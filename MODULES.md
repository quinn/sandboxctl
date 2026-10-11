# Change areas and checks

Use this guide to find the files and checks for a change. A change can affect
more than one area. Run the extra checks for every area that the change affects.

## Checks for every code change

Run these checks for every code change:

```sh
make check
make build
git diff --check
```

`make check` formats Go files, runs `go vet`, runs all Go tests, and lints the
Protocol Buffers. It can change Go files because it runs `gofmt -w`. Review
those changes before you finish.

## Protocol Buffers

The schema defines the gRPC API between `sandboxctl` and `sandboxd`. The client
and daemon code use the generated Go types and service interfaces. Regenerate
those files from the schema. Do not edit generated files by hand.

Files and folders: `proto/`, `buf.yaml`, `buf.gen.yaml`, generated files in
`internal/controlpb/`, protocol metadata and validation in
`internal/controlproto/`, and related client and daemon code in
`internal/client/` and `internal/daemon/`.

Extra check: run `make generate`, then `make check`.

## Package recipe or packaging files

The Arch package recipe builds and installs the CLI, daemon, command helpers,
Firecracker and jailer binaries, systemd units, and the packaged builder image.
The local package is used by `make install`. The AUR recipe describes the
published package. The systemd files define the socket-activated daemon.
Host setup itself is performed by `sandboxctl host install`; it creates
root-owned state and network configuration and enables the socket.

Files and folders: `packaging/local/` for the local Arch package recipe and
package data, `packaging/aur/` for the AUR recipe and its instructions,
`packaging/systemd/` for the daemon service and socket units, and related
package build scripts under `scripts/`.

Extra check: run `bash -n packaging/local/PKGBUILD` and
`makepkg --printsrcinfo` from `packaging/local/`.

## Image or builder scripts

These files support preparing and staging images. The scripts do not implement
the user-facing image build operation.

The overlay scripts download and verify the kernel and Ubuntu SquashFS used by
the Phase 1 proof. They build the overlay initramfs and create or reset its
ext4 state disk. `stage-overlay-image.sh` packages those proof assets in the
user data directory.

The builder scripts prepare the internal builder VM image. `builder-base.Dockerfile`
defines the seed filesystem. `download-builder-tools.sh` downloads and checks
the pinned BuildKit tools. `stage-builder-image.sh` uses Podman to build the
seed, adds the BuildKit tools, creates the VM image bundle, and stages it in
the user data directory. `make stage-builder-image` runs the required asset,
initramfs, and tool preparation steps before it runs that staging script. This
is a development and packaging step. `sandboxctl image build` does not build
the internal builder image; it uses an installed builder image to build a
guest image.

The Go package in `internal/imagebuild/` implements guest image builds inside a
temporary builder VM. The command in `cmd/sandboxctl/image/build/` parses user
options and calls the daemon. The daemon runs the image-build package. These
pieces use the internal builder image, but they do not run the host-side
Podman staging workflow.

Files and folders: image and builder scripts under `scripts/`,
`scripts/builder-base.Dockerfile`, `internal/imagebuild/`,
`cmd/sandboxctl/image/`, and the related targets in `Makefile` and
`make/phase1.make`.

Extra check: check the syntax of each changed script. Run
`make stage-builder-image` when the staged builder image itself needs testing.

## Guest boot or bootstrap

The guest initramfs runs before the guest's normal init system. It mounts the
base SquashFS read-only, mounts the per-sandbox ext4 state disk, combines them
with OverlayFS, and starts the guest system. The guest bootstrap service then
reads Firecracker metadata. It configures the hostname, network, SSH account,
and authorized key, and writes a completion marker. This setup runs inside the
guest and does not mount the guest filesystem on the host.

Files and folders: `cmd/guest/overlay-init/`,
`cmd/guest/overlay-init/overlayinit/`, `internal/overlayboot/`,
`cmd/guest/bootstrap/`, `cmd/guest/bootstrap/guestbootstrap/`, and image or
initramfs scripts under `scripts/`. The guest bootstrap unit and related guest
image files are under `internal/imagebuild/`.

Extra check: run `make manual-bootstrap-test`.

## Image build behavior

The `sandboxctl image build` command accepts a digest-pinned container base
image or a local Dockerfile and build context. It sends the request to
`sandboxd`. The daemon starts a temporary builder VM and runs BuildKit inside
that VM. The build creates a Debian or Ubuntu guest filesystem, adds
sandboxctl's guest setup, then writes a bootable image bundle. The command
installs the image by default. It can stage the bundle without installing it.

The command-line parser, VM orchestration, build context handling, BuildKit
configuration, guest provisioning, builder connection, and bundle finalizer
are in `cmd/sandboxctl/image/build/`. Shared image packages under
`internal/imagebuild/` handle manifests, bundles, and installation. The
manual tests cover the fixed base-image path, a Dockerfile build, and rejected
Dockerfile inputs.

Files and folders: `cmd/sandboxctl/image/build/`, the shared packages that
remain under `internal/imagebuild/`, image-build scripts under `scripts/`, and
related RPC definitions and handlers under `proto/` and `internal/daemon/`.

Extra check: run the matching test: `make manual-image-build`,
`make manual-dockerfile-build`, or `make manual-dockerfile-rejections`.

## Sandbox lifecycle behavior

The CLI commands validate user input and send lifecycle requests to `sandboxd`
over the Unix socket. The daemon stores instance records and allocations in
protected host state. It manages the state disk and TAP device, then uses the
Firecracker SDK and jailer to start or stop a VM. Delete and reset have
additional state checks. Delete refuses a running sandbox and releases its
allocation only after cleanup. Reset replaces the writable state disk for a
stopped sandbox.

Files and folders: lifecycle commands under `cmd/sandboxctl/`, shared
operations in `internal/app/`, RPC handlers in `internal/daemon/`, VM control
in `internal/runtime/`, records and allocations in `internal/store/`,
`internal/hoststate/`, disk handling in `internal/disk/`, and TAP handling in
`cmd/sandboxd/network/`.

Extra check: run the matching test: `make manual-delete-test` or
`make manual-reset-test`.

## Documentation only

User instructions and design records describe how to install, configure, and
use the tool. Keep command names and behavior aligned with the implementation
and authoritative runtime design. `MODULES.md` explains which checks apply to
each change area.

Files and folders: `README.md`, `MODULES.md`, and Markdown files under `docs/`.

Extra check: run `git diff --check` and review the text for correctness and
clarity.

Manual tests start virtual machines or change host state. Run them on a host
that has the configured `sandboxd` service and the required KVM access.
