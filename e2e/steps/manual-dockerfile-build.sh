#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

# shellcheck source=../lib.sh
source "$(dirname -- "${BASH_SOURCE[0]}")/../lib.sh"
data_home="$E2E_WORK_DIR/data"
export XDG_DATA_HOME="$data_home"
config_home=$XDG_CONFIG_HOME
ssh_config="$config_home/sandboxctl/ssh_config"
log_dir="$E2E_WORK_DIR/logs"
mkdir -p "$log_dir"
chmod 0700 "$log_dir"
log_file="$log_dir/dockerfile-build-test.log"
touch "$log_file"
chmod 0600 "$log_file"
exec > >(tee -a "$log_file") 2>&1

for program in python3 timeout ssh; do
  command -v "$program" >/dev/null 2>&1 || {
    printf 'Required command not found: %s\n' "$program" >&2
    exit 1
  }
done
[[ -r $ssh_config ]] || {
  printf 'The sandboxctl SSH configuration is missing. Run sandboxctl init first.\n' >&2
  exit 1
}

timestamp=$(date +%s)
suffix="$timestamp-$$"
image_name="df-$suffix"
sandbox_name="dfvm-$suffix"
alias="sandbox-$sandbox_name"
build_marker="dockerfile-build-$suffix"
context_parent="${E2E_WORK_DIR:-$repo_dir/tmp}"
mkdir -p "$context_parent"
context_dir=$(mktemp -d "$context_parent/dockerfile-context.XXXXXX")
chmod 0700 "$context_dir"
cleanup_context() {
  rm -rf -- "$context_dir"
}
trap cleanup_context EXIT
mkdir -m 0700 "$context_dir/payload"
cat >"$context_dir/Dockerfile" <<'DOCKERFILE'
FROM ubuntu:24.04@sha256:224a1869083a311ef3f13648a154ba79832fbef6364d31493642ca03082da254 AS build
ARG BUILD_MARKER
COPY . /opt/docker-context
COPY payload/probe /usr/local/bin/sandboxctl-context-probe
RUN printf '%s\n' "$BUILD_MARKER" > /etc/sandboxctl-build-argument

FROM build AS runtime
USER 65534:65534
WORKDIR /tmp
ENV IMAGE_BUILD_ENV=container-only
CMD ["/usr/bin/false"]
ENTRYPOINT ["/usr/bin/false"]
DOCKERFILE
cat >"$context_dir/.dockerignore" <<'IGNORE'
ignored.txt
IGNORE
cat >"$context_dir/payload/probe" <<'PROBE'
#!/bin/sh
printf 'context executable passed\n'
PROBE
chmod 0755 "$context_dir/payload/probe"
printf 'must stay outside the guest context\n' >"$context_dir/ignored.txt"
chmod 0600 "$context_dir/ignored.txt"

printf 'Dockerfile image build test log: %s\n' "$log_file"
printf 'Building image %s from a local Dockerfile.\n' "$image_name"
e2e_sandboxctl image build "$image_name" \
  --dockerfile "$context_dir/Dockerfile" \
  --context "$context_dir" \
  --target runtime \
  --build-arg "BUILD_MARKER=$build_marker" \
  --guest-version 24.04

manifest_files=("$data_home/sandboxctl/images/$image_name"/*/manifest.json)
[[ ${#manifest_files[@]} -eq 1 && -f ${manifest_files[0]} ]] || {
  printf 'Built image manifest is missing.\n' >&2
  exit 1
}
manifest=${manifest_files[0]}
version=$(python3 - "$manifest" <<'PY'
import json
import pathlib
import sys

image = json.loads(pathlib.Path(sys.argv[1]).read_text())
version = image.get("version", "")
if not version.startswith("sha256:") or len(version) != 71:
    raise SystemExit("The built image manifest has an invalid version.")
print(version)
PY
)

printf 'Installing built image %s at %s.\n' "$image_name" "$version"
e2e_sandboxctl image install "$image_name" --version "$version"
e2e_sandboxctl create "$sandbox_name" --cpus 2 --memory 2048 --disk-size 1 --image "$image_name"
e2e_sandboxctl start "$sandbox_name"

ssh_output=$(mktemp "$log_dir/.dockerfile-build-ssh.XXXXXX")
chmod 0600 "$ssh_output"
ssh_known_hosts=$(mktemp "$log_dir/.dockerfile-build-known-hosts.XXXXXX")
chmod 0600 "$ssh_known_hosts"
cleanup_ssh_output() {
  rm -f -- "$ssh_output"
  rm -f -- "$ssh_known_hosts"
}
trap 'cleanup_ssh_output; cleanup_context' EXIT
deadline=$((SECONDS + 120))
while (( SECONDS < deadline )); do
  if timeout --foreground --kill-after=1s 5s ssh -F "$ssh_config" -o BatchMode=yes -o ConnectTimeout=3 -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile="$ssh_known_hosts" -o GlobalKnownHostsFile=/dev/null "$alias" \
    'test "$(ps -p 1 -o comm= | xargs)" = systemd && test "$(cat /etc/sandboxctl-build-argument)" = '"$build_marker"' && test -x /usr/local/bin/sandboxctl-context-probe && test "$(/usr/local/bin/sandboxctl-context-probe)" = "context executable passed" && test ! -e /opt/docker-context/ignored.txt && test -z "${IMAGE_BUILD_ENV:-}" && test "$(id -un)" = dev && test -r /var/lib/sandboxctl/bootstrap-complete' \
    >"$ssh_output" 2>&1; then
    printf 'PASS Dockerfile image, local COPY, ignore rules, build arguments, VM PID 1, and public-key SSH.\n'
    break
  fi
  sleep 2
done
if (( SECONDS >= deadline )); then
  printf 'Dockerfile guest checks did not pass within 120 seconds.\n' >&2
  cat "$ssh_output" >&2
  printf 'Sandbox %s remains available for diagnosis.\n' "$sandbox_name" >&2
  printf 'Guest console log: /var/lib/sandboxctl/instances/%s/console.log\n' "$sandbox_name" >&2
  exit 1
fi

e2e_sandboxctl stop "$sandbox_name"
e2e_sandboxctl start "$sandbox_name"
restart_deadline=$((SECONDS + 120))
restart_verified=0
while (( SECONDS < restart_deadline )); do
  if timeout --foreground --kill-after=1s 5s ssh -F "$ssh_config" -o BatchMode=yes -o ConnectTimeout=3 -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile="$ssh_known_hosts" -o GlobalKnownHostsFile=/dev/null "$alias" \
    'test "$(cat /etc/sandboxctl-build-argument)" = '"$build_marker"' && test -r /var/lib/sandboxctl/bootstrap-complete'; then
    restart_verified=1
    break
  fi
  sleep 2
done
if (( restart_verified == 0 )); then
  printf 'Dockerfile guest state did not persist after restart.\n' >&2
  exit 1
fi
e2e_sandboxctl stop "$sandbox_name"
e2e_sandboxctl delete "$sandbox_name" --yes
e2e_sandboxctl image delete "$image_name" --yes
printf 'PASS Dockerfile image persistence after restart.\n'
printf 'Test image %s was removed after the VM checks.\n' "$image_name"
printf 'Test log: %s\n' "$log_file"
