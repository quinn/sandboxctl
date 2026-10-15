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
`internal/controlsettings/`, client code in `internal/client/`, and server code
in `cmd/sandboxd/daemon/`.

Extra check: run `make generate`, then `make check`.

## Package recipe or packaging files

The Arch package recipe builds and installs the CLI, daemon, command helpers,
Firecracker and jailer binaries, systemd units, and the packaged builder image.
The checkout-based recipe is used by `make install` and GitHub release builds.
The systemd files define the socket-activated daemon.
Host setup itself is performed by `sandboxctl host install`; it creates
root-owned state and network configuration and enables the socket.

Files and folders: `packaging/local/` for the Arch package recipe and instructions,
`.github/workflows/` for release automation,
`packaging/systemd/` for the daemon service and socket units, and related
package build scripts under `scripts/`.

Extra check: run `bash -n packaging/local/PKGBUILD` and
`makepkg --printsrcinfo` from `packaging/local/`.

## Image or builder scripts

These files support preparing and staging images. The scripts do not implement
the user-facing image build operation.

The active asset scripts download the pinned kernel and Ubuntu SquashFS used
by the builder image. They build the guest initramfs. `stage-builder-image.sh`
uses these files to prepare the builder image. The Phase 1 proof and its
state-disk helpers are under `tests/phase1/`. Manual validation scripts and
the complete installed-package E2E suite are under `e2e/`.

The builder scripts prepare the internal builder VM image. `builder-base.Dockerfile`
defines the seed filesystem. `download-builder-tools.sh` downloads and checks
the pinned BuildKit tools. `stage-builder-image.sh` first checks the builder
image at `$XDG_DATA_HOME/sandboxctl/images/builder/`, or
`~/.local/share/sandboxctl/images/builder/` when the variable is not set. It
exits without building when a complete bundle exists.
Otherwise, it downloads the required assets and tools, builds the initramfs,
uses Podman to build the seed, and stages the VM image bundle in the user image
store. `make clean-builder` calls this script with `--clean` to remove the
staged builder bundle. The package recipe copies the staged bundle into its
package staging directory. This is a development and packaging step.
`sandboxctl image build` does not build the internal builder image; it uses an
installed builder image to build a guest image.

The Go packages in `internal/imagebuild/` handle image manifests, bundles, and
installation. The command in `cmd/sandboxctl/image/build/` parses user options
and coordinates a guest image build. It asks `sandboxd` to create and start a
temporary builder VM. It then connects to that VM to run BuildKit and retrieve
the finished image. It asks `sandboxd` to stop and delete the VM when the build
ends. These pieces use the installed builder image. They do not run the
host-side Podman staging workflow.

Files and folders: active image and builder scripts under `scripts/`,
`scripts/builder-base.Dockerfile`, `internal/imagebuild/`,
`cmd/sandboxctl/image/`, tests under `e2e/`, and related targets in `Makefile`
and `e2e/e2e.make`.

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
`cmd/guest/overlay-init/overlayinit/`, `cmd/guest/bootstrap/`,
`cmd/guest/bootstrap/guestbootstrap/`, and image or
initramfs scripts under `scripts/`. The overlay initramfs installs the guest
bootstrap binary and service. The guest image provisioning script and service
setup are under `cmd/sandboxctl/image/build/provision/`. Manual guest tests are
under `e2e/steps/`.

Extra check: run `make manual-bootstrap-test`.

## Phase 1 proof and manual validation

Keep the temporary direct Firecracker proof and its state-disk helpers in
`tests/phase1/`. Keep manual tests that start real sandboxes in
`e2e/steps/`. Keep the full installed-package suite in `e2e/`. These files
validate the runtime. They are not runtime or package scripts.

Files and folders: `tests/phase1/`, `e2e/`, and the proof and E2E targets in
`Makefile` and `e2e/e2e.make`.

Extra check: run the relevant target, such as `make phase1-overlay-proof` or
the matching `make manual-*` target. Run `bash -n` on changed shell scripts.

## Image build behavior

The `sandboxctl image build` command accepts a digest-pinned container base
image or a local Dockerfile and build context. The CLI asks `sandboxd` to start
a temporary builder VM. It connects to that VM and runs BuildKit there. The
build creates a Debian or Ubuntu guest filesystem, adds sandboxctl's guest
setup, then writes a bootable image bundle. The command installs the image by
default. It can stage the bundle without installing it.

The command-line parser, VM orchestration, build context handling, BuildKit
configuration, guest provisioning, builder connection, and bundle finalizer
are in `cmd/sandboxctl/image/build/`. Shared image packages under
`internal/imagebuild/` handle manifests, bundles, and installation. The
manual tests cover the fixed base-image path, a Dockerfile build, and rejected
Dockerfile inputs.

Files and folders: `cmd/sandboxctl/image/build/`, the shared packages that
remain under `internal/imagebuild/`, image-build scripts under `scripts/`, and
related RPC definitions under `proto/`, shared client-server settings under
`internal/controlsettings/`, and server handlers under
`cmd/sandboxd/daemon/`.

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
operations in `internal/app/`, RPC handlers in `cmd/sandboxd/daemon/`, VM
control in `internal/runtime/`, records and allocations in `internal/store/`,
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
