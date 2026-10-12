#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

# shellcheck source=../lib.sh
source "$(dirname -- "${BASH_SOURCE[0]}")/../lib.sh"
log_dir="$repo_dir/tmp/logs"
mkdir -p "$log_dir"
chmod 0700 "$log_dir"
log_file="$log_dir/builder-test.log"
touch "$log_file"
chmod 0600 "$log_file"
exec > >(tee -a "$log_file") 2>&1

for program in ssh timeout python3 make; do
  command -v "$program" >/dev/null 2>&1 || {
    printf 'Required command not found: %s\n' "$program" >&2
    exit 1
  }
done

name="builder-$(date +%s)-$$"
alias="sandbox-$name"
data_home="${XDG_DATA_HOME:-$repo_dir/tmp/test-data}"
XDG_DATA_HOME="$data_home" IMAGE_NAME="$name" make -C "$repo_dir" stage-builder-image
image_root="$data_home/sandboxctl/images/$name"
manifest=$(find "$image_root" -mindepth 2 -maxdepth 2 -type f -name manifest.json -printf '%T@ %p\n' | sort -nr | sed -n '1s/^[^ ]* //p')
if [[ -z $manifest ]]; then
  printf 'The staged builder image is missing. Run make stage-builder-image.\n' >&2
  exit 1
fi
version=$(python3 - "$manifest" <<'PY'
import json
import pathlib
import sys

manifest = json.loads(pathlib.Path(sys.argv[1]).read_text())
version = manifest.get("version", "")
if not version.startswith("sha256:") or len(version) != 71:
    raise SystemExit("The builder image manifest has an invalid version.")
print(version)
PY
)

printf 'Builder VM test log: %s\n' "$log_file"
printf 'Installing builder image %s.\n' "$version"
XDG_DATA_HOME="$data_home" e2e_sandboxctl image install "$name" --version "$version"
e2e_sandboxctl create "$name" --cpus 2 --memory 2048 --disk-size 1 --image "$name"
e2e_sandboxctl start "$name"

ssh_output=$(mktemp "$log_dir/.builder-ssh.XXXXXX")
trap 'rm -f -- "$ssh_output"' EXIT
deadline=$((SECONDS + 120))
while (( SECONDS < deadline )); do
  if timeout --foreground --kill-after=1s 8s ssh \
    -F "$XDG_CONFIG_HOME/sandboxctl/ssh_config" \
    -o BatchMode=yes \
    -o ConnectTimeout=3 \
    -o StrictHostKeyChecking=accept-new \
    "$alias" \
    'set -eu; test -r /var/lib/sandboxctl/bootstrap-complete; buildctl --version; buildkitd --version; mksquashfs -version' \
    >"$ssh_output" 2>&1; then
    if ! grep -Fq 'v0.34.0' "$ssh_output" || ! grep -Eq 'mksquashfs version 4\.' "$ssh_output"; then
      printf 'Builder VM started, but a required tool has an unexpected version.\n' >&2
      cat "$ssh_output"
      exit 1
    fi
    cat "$ssh_output"
    printf 'PASS builder bootstrap and pinned tools for %s.\n' "$name"
    if [[ ${E2E_CLEANUP:-0} == 1 ]]; then
      e2e_sandboxctl stop "$name"
      e2e_sandboxctl delete "$name" --yes
      e2e_sandboxctl image delete "$name" --yes
    else
      printf 'The sandbox remains running. Stop it with: sandboxctl stop %s\n' "$name"
    fi
    exit 0
  fi
  sleep 2
done

printf 'The builder VM did not become ready within 120 seconds.\n' >&2
cat "$ssh_output" >&2
printf 'Sandbox %s remains available for diagnosis.\n' "$name" >&2
printf 'Guest console log: /var/lib/sandboxctl/instances/%s/console.log\n' "$name" >&2
exit 1
