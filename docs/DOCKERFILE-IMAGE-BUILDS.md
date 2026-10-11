# Dockerfile guest image builds

Status: Implemented. The base-image and local Dockerfile modes are available.

Date: 2026-10-11

## 1. Purpose and authority

Let the operator build a custom guest image from a local Dockerfile and build
context. A build context is the directory of files available to `COPY` and
`ADD` instructions.

[SANDBOX-RUNTIME-V2.md](SANDBOX-RUNTIME-V2.md) remains authoritative for the
runtime, privilege boundary, boot sequence, and image format. This plan adds
an image input mode. It does not change the runtime architecture.

The previous runtime phase is complete. Its clean Arch host acceptance test
remains deferred. This phase does not depend on that test host.

## 2. Confirmed decisions

- Use the existing packaged builder image and temporary builder virtual
  machine (VM).
- Use BuildKit to execute the Dockerfile.
- Apply required sandboxctl provisioning after the user build.
- Support Debian and Ubuntu systemd guests on x86-64.
- Boot the guest init system as process identifier (PID) 1.
- Give `CMD` and `ENTRYPOINT` no role in VM startup.
- Keep the existing digest-pinned `--base-image` input mode.
- Run routine builds as the operator against the installed sandboxd service.

The operator enables application startup through guest system services. The
image builder does not generate a startup service from container commands.

## 3. E2B findings

The retained runtime checkout is `tmp/e2b-runtime`. Its commit is
`47096f195aca4a545a9079b706efb3a18f51cf3f`. Its extraction record is
[E2B-SOURCE.md](E2B-SOURCE.md).

Inspect these paths in that checkout:

- `packages/orchestrator/template-manager.proto`
- `packages/orchestrator/pkg/template/build/commands/executor.go`
- `packages/orchestrator/pkg/template/build/commands/run.go`
- `packages/orchestrator/pkg/template/build/commands/copy.go`
- `packages/orchestrator/pkg/template/build/phases/base/builder.go`
- `packages/orchestrator/pkg/template/build/phases/steps/builder.go`

The runtime receives a base image and template steps. It provisions the base
before it executes custom steps in guests. Its layer system caches results.
Its finalization uses Firecracker snapshots.

The Dockerfile translator lives in the separate E2B SDK repository. The SDK
source was reviewed on 2026-10-10. These links refer to its current `main`
branch, not the retained runtime commit.

The translator requires one `FROM`. It rejects multiple stages. It maps
`RUN`, file copies, user selection, and working directories to template steps.
It handles `ARG` through environment settings. It skips `EXPOSE` and `VOLUME`.
It maps `CMD` and `ENTRYPOINT` to an E2B start command. See the
[SDK Dockerfile parser](https://github.com/e2b-dev/E2B/blob/main/packages/js-sdk/src/template/dockerfileParser.ts).

The SDK applies ignore patterns, hashes copy inputs, and uploads missing
inputs. See the
[template SDK](https://github.com/e2b-dev/E2B/blob/main/packages/js-sdk/src/template/index.ts).

Use these sources for comparison only. Do not copy E2B's Dockerfile
translator, step executor, cache layers, snapshots, or `envd`. Keep the
existing E2B-derived distribution profiles and provisioning renderer.

## 4. Command contract

The command accepts a Dockerfile and context:

```sh
sandboxctl image build dev-image \
  --context ./image \
  --guest-version 24.04
```

Use the packaged builder by default. Keep the existing paired builder override
options. Keep `--distribution`, with `ubuntu` as its default.

Add these options:

| Option | Rule |
| --- | --- |
| `--guest-version VERSION` | Require the built guest's `VERSION_ID` to match this value. |
| `--dockerfile PATH` | Select a regular local Dockerfile. Resolve PATH from the current directory. Default to `Dockerfile` in the context directory. |
| `--context DIRECTORY` | Select the local context directory. Selecting this option selects Dockerfile input. Default to `.` in Dockerfile mode. |
| `--target NAME` | Select a Dockerfile stage. Default to the final stage. |
| `--build-arg NAME=VALUE` | Set one build argument. Permit repeated options with different names. |

Require exactly one input mode. `--base-image` selects base-image mode.
`--dockerfile`, `--context`, `--target`, or `--build-arg` selects Dockerfile
mode. In Dockerfile mode, infer `<context>/Dockerfile` when `--dockerfile` is
omitted. Default the context to `.`. Reject Dockerfile options in base-image
mode. Reject duplicate build argument names. Require explicit values. Do not
read argument values from the operator's environment.

Require the selected Dockerfile to be inside the context directory. Do not
accept standard input, remote context URLs, or Git context URLs in this phase.

Keep the output bundle. Install the finished guest image by default. Accept
`--install=false` to stage the bundle without installing it, and print the
image install command in that mode.

## 5. Dockerfile behavior

BuildKit retains its build semantics for supported instructions. The result
used by the VM is the provisioned filesystem.

| Setting | Build behavior | VM behavior |
| --- | --- | --- |
| `RUN`, `COPY`, `ADD` | BuildKit changes the filesystem. | The resulting files remain, subject to required provisioning and cleanup. |
| `ARG` | Supplies build parameters. | Does not create a login environment variable. |
| `ENV` | Applies to later build instructions. | Does not configure systemd or SSH sessions. |
| `USER`, `WORKDIR` | Applies to later build instructions. | Does not select the SSH account or its login directory. |
| `CMD`, `ENTRYPOINT` | Can exist in the user image configuration. | Does not start a guest process. |
| `EXPOSE` | Can exist in the user image configuration. | Does not change host firewall rules. |
| `VOLUME` | Retains BuildKit behavior. | Does not allocate or mount an additional VM disk. |
| `HEALTHCHECK`, `LABEL` | Can exist in the user image configuration. | Does not configure VM health checks or sandbox metadata. |

Support multiple stages and local file copies. Limit the selected result to
Linux x86-64. The result must contain a supported distribution and a usable
shell before provisioning. A minimal container filesystem is not necessarily
a usable VM image.

Require digest-pinned external image sources. Apply this rule to resolved
`FROM` inputs and external `COPY --from` inputs. Account for build arguments
and stage references. Do not validate Dockerfile syntax with regular
expressions. Use BuildKit source policy or its maintained parser for this
check. The first milestone must prove the chosen enforcement method.

Use the pinned built-in Dockerfile frontend. Do not permit a Dockerfile to
select an unpinned external frontend. Remote `ADD` inputs, private registry
credentials, secret mounts, SSH agent forwarding, and privileged BuildKit
entitlements are outside this phase.

Pinned images and recorded inputs do not guarantee identical output. Package
repositories and commands can produce changing results. Do not describe these
builds as fully reproducible.

## 6. Context preparation and transfer

Create `internal/imagebuild/buildcontext/` for context preparation. Keep file
walking, filtering, archive creation, limits, and hashing in this package.
Use `snapshot.go` for file collection and input identity. Use `ignore.go` for
ignore rules. Use `archive.go` for archive creation. Use `extract.go` for
shared archive validation and safe extraction. Add matching `_test.go` files.
Run extraction code only inside the builder VM.

Apply Docker ignore semantics before transfer. A Dockerfile-specific ignore
file takes precedence over the context's `.dockerignore`. Preserve pattern
order, negation, and `**` matching. Use a pinned maintained matcher if the
standard library cannot provide these semantics. Do not write a partial
matcher that treats Docker patterns as ordinary shell patterns.

Snapshot the accepted inputs into a private archive before starting the VM.
Keep the selected Dockerfile available even when an ignore rule excludes it
from ordinary context files. Transfer it separately from the filtered context.
Keep generated service files and provisioning inputs outside the user context.

Preserve relative file paths, executable modes, and safe symbolic links. Do
not follow symbolic links while collecting files. Reject links that escape
the context. Reject absolute archive paths, `..` paths, duplicate entries,
device nodes, sockets, and named pipes. Detect file changes during collection.
Never read a file outside the selected context through a link or path race.

Define limits for Dockerfile size, ignore file size, entry count, individual
file size, total file bytes, and archive bytes. Use named constants and report
which limit failed. Set private temporary files to mode `0600`. Set their
parent directories to mode `0700`.

Transfer the archive through the existing bounded guest SSH channel. Expand
it only inside the builder VM. Validate archive entries again during guest
extraction. Extraction must not overwrite control files or escape the guest
context directory through an earlier symbolic link.

Hash the exact accepted input snapshot. Use stable path ordering and archive
metadata. Record file content, relative paths, relevant modes, and link
targets. Do not include host ownership or collection timestamps in the hash.

## 7. Build and provisioning sequence

Use one temporary VM and one BuildKit daemon for the build. Keep all image
expansion and filesystem export inside that VM.

1. Validate command options and prepare the context snapshot.
2. Start the packaged builder through the existing daemon VM factory.
3. Wait for guest bootstrap and SSH readiness.
4. Upload the context archive, Dockerfile, and structured build settings.
5. Upload sandboxctl's fixed service and provisioning inputs separately.
6. Extract the context safely inside the VM.
7. Build the selected user stage with BuildKit.
8. Export that result as an Open Container Initiative (OCI) layout inside the VM.
9. Apply a generated provisioning Dockerfile to that result.
10. Export the provisioned root filesystem inside the VM.
11. Validate guest distribution, version, architecture, init, account, and SSH policy.
12. Remove machine identity, host keys, caches, logs, and temporary build files.
13. Stop BuildKit and synchronize filesystem writes.
14. Create SquashFS and return it through the bounded artifact channel.
15. Stage the verified bundle and its provenance atomically.
16. Stop and delete the temporary builder on success, failure, or cancellation.

For step 9, use the OCI result as a named BuildKit context. Build a fresh
`FROM scratch` stage. Copy the user result's filesystem into that stage.
Set a fixed root user, working directory, and provisioning environment. Copy
and run the existing provisioning script. This avoids inheriting container
environment settings or executing inherited `ONBUILD` instructions again.

BuildKit v0.34.0 documents named OCI layouts and frontend context aliases in
its [buildctl reference](https://github.com/moby/buildkit/blob/v0.34.0/docs/reference/buildctl.md).
Its [README](https://github.com/moby/buildkit/blob/v0.34.0/README.md) documents
Dockerfile builds, target selection, build arguments, and OCI output.
Prove the complete two-build path with this pinned version before extending
the command. Verify root filesystem ownership, modes, and symbolic links in
that proof. Revise the plan if the proof fails.

Provisioning owns the `dev` account policy, required packages, SSH policy,
bootstrap service, and init setup. Custom files and enabled application
services can remain when they satisfy validation. Do not remove arbitrary
application data during cleanup. Document the exact cleanup paths.

Verify the final `/etc/os-release` against `--distribution` and
`--guest-version`. Refuse to publish a bundle with incorrect guest metadata.
The provisioning script checks `ID` and the exact `VERSION_ID` before it
installs packages.

## 8. Implementation locations

| Path | Implementation |
| --- | --- |
| `cmd/sandboxctl/image/build/` | Parses both input modes and builds a typed request. |
| `internal/imagebuild/buildcontext/` | Creates a bounded, private context snapshot with ignore rules, safe file checks, archive output, and input digests. |
| `internal/imagebuild/buildkit/` | Renders the fixed provisioning build and pins BuildKit source policy. |
| `internal/imagebuild/builder/` | Uploads separate user and control inputs, runs the fixed guest service, and cleans up the builder VM. |
| `internal/imagebuild/guest/` | Validates uploaded settings, extracts context safely, runs the two BuildKit stages, and finalizes the guest filesystem. |
| `internal/imagebuild/finalize/` | Verifies builder artifacts and publishes the output bundle with input provenance. |
| `internal/imagebuild/manifest/` | Keeps the existing image manifest schema and digest calculation. |
| `scripts/manual-dockerfile-build.sh` | Builds, installs, boots, verifies, restarts, and deletes a test guest through the workspace CLI. |
| `scripts/manual-dockerfile-rejections.sh` | Checks rejected builds and confirms that they publish no image manifest. |
| `Makefile` | Adds Dockerfile acceptance targets with the test builder name and digest. |
| `README.md` | Documents local Dockerfile builds and their limits. |

Use the standard library for archives, hashing, typed settings, and file
handling. Record each additional dependency and its version. No E2B code
extraction is required for this phase.

If safe extraction needs a compiled guest helper, define its entry point,
build target, and distribution path in the first milestone. Include the
helper in the tool distribution. Do not require the operator to have Go
installed for routine image builds.

No new sandboxd method or host command runner is planned. User Dockerfile
commands execute only inside the builder VM. If implementation requires a
daemon or packaged builder change, record why before changing that boundary.

## 9. Provenance and logging

Record the input mode, Dockerfile digest, context digest, selected stage,
build argument digest, pinned image sources, user result digest, provisioning
script digest, builder version, and BuildKit version. Retain the existing
E2B-derived source reference for provisioning.

Use the current manifest `provenance` field for a bounded, versioned record.
Do not add an unverified sidecar that the current image import cannot retain.
Include this record in the existing manifest version calculation. Do not store
raw context files or build argument values in the manifest.

Add proactive `slog` events for validation, context preparation, transfer,
user build, provisioning, validation, export, publication, and cleanup. Include
durations, byte counts, and build identifiers. Do not log file contents,
private keys, registry credentials, or build argument values.

Show bounded BuildKit progress while the command runs. Keep bounded failure
output that includes the final error. Do not leave a long build silent until
it exits. Build commands can print sensitive data, so keep saved logs private.

## 10. Implementation status and acceptance

The base-image and Dockerfile modes share the packaged builder VM, bounded
artifact transfer, provisioning, manifest validation, and atomic publication.
Dockerfile inputs use a private, deterministic context snapshot and Docker
ignore rules. BuildKit enforces digest-pinned image sources. The builder
exports the selected stage, applies fixed guest provisioning, and returns a
SquashFS image. Container startup settings do not replace guest PID 1.

Run the Dockerfile VM acceptance test with `make manual-dockerfile-build`.
The Makefile contains the installed builder name and digest for this test.
Edit those values when the installed test builder changes. The test uses the
workspace CLI with the installed sandboxd service. It does not require a
package reinstall when the package builder did not change.

The live test passed on 2026-10-11. It built multiple Dockerfile stages with
local `COPY`, an executable file, an explicit build argument, an ignored
file, a non-root final `USER`, `ENV`, `CMD`, and `ENTRYPOINT`. The guest booted
systemd as PID 1. The test confirmed fixed provisioning, public-key SSH,
context filtering, and state persistence after stop and restart. It then
stopped and deleted the test VM. The log is
`tmp/logs/dockerfile-build-test.log`.

The live rejection test passed on 2026-10-11. It rejected a failing `RUN`, an
unpinned source, a `scratch` result, a guest version mismatch, and an
oversized Dockerfile. It confirmed that each failed build published no image
manifest. Run it with `make manual-dockerfile-rejections`. The log is
`tmp/logs/dockerfile-rejection-test.log`.

The live base-image build passed again on 2026-10-11. It built Ubuntu 24.04
from the pinned base and validated the guest version before it published the
image. The image build log is `tmp/logs/image-build-test.log`.

Unit tests cover invalid options, duplicate build arguments, context paths,
ignore rules, unsafe links, file changes, archive limits, source-policy
selection, transfer errors, build errors, cancellation cleanup, and manifest
provenance. The live positive and rejection tests cover the base-image and
Dockerfile paths. The clean supported Arch host acceptance test remains open
from the runtime phase.

Use workspace command binaries against the installed daemon. Do not reinstall
the Arch package when only CLI or guest service source changes. A packaged
builder change requires a new verified bundle and installation through the
existing workflow.

## 11. Deferred work

Private registry authentication, secret and SSH mounts, remote build contexts,
persistent build caches, additional guest distributions, and application
startup convenience commands are separate phases. The clean Arch host
acceptance test remains open from the runtime phase.
