#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

repo_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
log_dir="$repo_dir/tmp/logs"
mkdir -p "$log_dir"
chmod 0700 "$log_dir"
log_file="$log_dir/guest-bootstrap-test.log"
touch "$log_file"
chmod 0600 "$log_file"
exec > >(tee -a "$log_file") 2>&1

for program in sandboxctl ssh python3; do
  command -v "$program" >/dev/null 2>&1 || {
    printf 'Required command not found: %s\n' "$program" >&2
    exit 1
  }
done

name="bootstrap-$(date +%s)-$$"
alias="sandbox-$name"
XDG_DATA_HOME="$repo_dir/tmp/test-data" IMAGE_NAME="$name" make -C "$repo_dir" stage-overlay-image
image_root="$repo_dir/tmp/test-data/sandboxctl/images/$name"

[[ -r /dev/kvm && -w /dev/kvm ]] || {
  printf 'Firecracker needs read and write access to /dev/kvm.\n' >&2
  exit 1
}
manifest=$(find "$image_root" -mindepth 2 -maxdepth 2 -type f -name manifest.json -printf '%T@ %p\n' 2>/dev/null | sort -nr | sed -n '1s/^[^ ]* //p')
[[ -n $manifest ]] || {
  printf 'The staged guest image is missing. Run make manual-bootstrap-test.\n' >&2
  exit 1
}
image_dir=${manifest%/manifest.json}

version=$(python3 - "$image_dir/manifest.json" <<'PY'
import json
import pathlib
import sys

manifest = json.loads(pathlib.Path(sys.argv[1]).read_text())
version = manifest.get("version", "")
if not version.startswith("sha256:") or len(version) != 71:
    raise SystemExit("The staged image manifest has an invalid version.")
print(version)
PY
)

printf 'Guest bootstrap test log: %s\n' "$log_file"
printf 'Installing image version %s.\n' "$version"
XDG_DATA_HOME="$repo_dir/tmp/test-data" sandboxctl image install "$name" --version "$version"
sandboxctl create "$name" --cpus 2 --memory 2048 --disk-size 1 --image "$name"
sandboxctl start "$name"

ssh_output=$(mktemp "$repo_dir/tmp/logs/.guest-bootstrap-ssh.XXXXXX")
trap 'rm -f -- "$ssh_output"' EXIT
ready=false
for attempt in $(seq 1 60); do
  if ssh -o BatchMode=yes -o ConnectTimeout=3 -o StrictHostKeyChecking=accept-new "$alias" \
    "test -r /var/lib/sandboxctl/bootstrap-complete && grep -qx 'hostname=$name' /var/lib/sandboxctl/bootstrap-complete && test \"\$(hostname)\" = '$name' && test \"\$(id -un)\" = dev" \
    >"$ssh_output" 2>&1; then
    ready=true
    break
  fi
  sleep 2
done

if [[ $ready != true ]]; then
  cat "$ssh_output"
  printf 'Guest bootstrap did not complete within 120 seconds.\n' >&2
  printf 'Sandbox %s remains available for diagnosis.\n' "$name" >&2
  exit 1
fi

ssh -o BatchMode=yes -o ConnectTimeout=3 -o StrictHostKeyChecking=accept-new "$alias" \
  'cat /var/lib/sandboxctl/bootstrap-complete; hostname; id -un'
printf 'PASS guest bootstrap and public-key SSH for %s.\n' "$name"
printf 'The sandbox remains running. Stop it with: sandboxctl stop %s\n' "$name"
