# E2B-derived source record

Repository: https://github.com/e2b-dev/runtime
Commit: 47096f195aca4a545a9079b706efb3a18f51cf3f
License: Apache-2.0

The retained comparison checkout is `tmp/e2b-runtime`. Git ignores that path.
The checkout is not part of the sandboxctl source tree or release artifacts.
The license text is in `third_party/licenses/e2b-apache-2.0.txt`.

## Extracted source

### Distribution profiles

- Original E2B paths:
  - `packages/orchestrator/pkg/template/build/phases/base/distro/distro.go`
  - `packages/orchestrator/pkg/template/build/phases/base/distro/init.go`
  - `packages/orchestrator/pkg/template/build/phases/base/distro/distro_test.go`
  - `packages/orchestrator/pkg/template/build/phases/base/distro/parity_test.go`
- Destination paths:
  - `internal/imagebuild/distro/profile.go`
  - `internal/imagebuild/distro/init.go`
  - `internal/imagebuild/distro/profile_test.go`
- Functions and concepts retained: declared `/etc/os-release` identity selection, explicit Debian and Ubuntu package profiles, `dpkg-query` package detection, noninteractive `apt-get` package installation, init-system paths, SSH service names, administrator groups, CA bundle refresh, serial console and first-boot service handling.
- Omitted: Fedora, Arch, Alpine, and NixOS profiles; `ID_LIKE` fallback; time-sync and E2B service setup; snapshot behavior; mutable global profile registry.
- Material modifications: support Debian and Ubuntu only; use sandboxctl identifiers; reduce packages to the sandbox bootstrap requirements; set secure OpenSSH policy; create `dev` with a random unusable password hash; configure the documented guest sudo policy; enable SSH; remove machine identity and SSH host keys for bootstrap regeneration.
- The sandboxctl tests are newly authored from the required behavior. E2B test code was inspected but was not copied.
- Extraction date: 2026-10-07.

### Provisioning renderer and script

- Original E2B paths:
  - `packages/orchestrator/pkg/template/build/phases/base/provision.go`
  - `packages/orchestrator/pkg/template/build/phases/base/provision.sh`
  - `packages/orchestrator/pkg/template/build/phases/base/provision_test.go`
- Destination paths:
  - `internal/imagebuild/provision/render.go`
  - `internal/imagebuild/provision/provision.sh`
  - `internal/imagebuild/provision/provision_test.go`
- Functions and concepts retained: render a deterministic POSIX shell provisioning script and select package setup from the declared distribution profile.
- Omitted: E2B telemetry, logging, template metadata, rootfs mounts, environment daemon setup, time synchronization, proxy setup, snapshot settings, and E2B-specific cleanup.
- Material modifications: embed a sandboxctl-owned script; add secure SSH policy; create the `dev` account and sudo policy; leave `authorized_keys` for guest bootstrap; remove machine ID and SSH host keys; enable SSH through the selected init system.
- The guest bootstrap logic follows the sandboxctl metadata contract. It is new code, not copied from E2B.
- Extraction date: 2026-10-07.

## Inspected but not extracted

- `packages/orchestrator/pkg/template/build/core/oci/oci.go`
- `packages/orchestrator/pkg/template/build/core/oci/oci_test.go`
- `packages/orchestrator/pkg/template/build/core/oci/auth/`

BuildKit will pull and expand OCI images inside the isolated builder VM. The host-side OCI-to-ext4 extraction path is excluded. Revisit reference and digest validation only if the sandboxctl CLI needs to validate OCI references before sending a build request.

Other reviewed paths, including E2B step execution, Firecracker process management, filesystem mounting, and snapshot finalization, remain reference-only. Do not copy them into sandboxctl.
