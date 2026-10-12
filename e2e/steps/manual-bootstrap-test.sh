#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

# shellcheck source=../lib.sh
source "$(dirname -- "${BASH_SOURCE[0]}")/../lib.sh"
log_dir="$repo_dir/tmp/logs"
mkdir -p "$log_dir"
chmod 0700 "$log_dir"
log_file="$log_dir/guest-bootstrap-test.log"
touch "$log_file"
chmod 0600 "$log_file"
exec > >(tee -a "$log_file") 2>&1

for program in ssh python3 pacman timeout; do
  command -v "$program" >/dev/null 2>&1 || {
    printf 'Required command not found: %s\n' "$program" >&2
    exit 1
  }
done

name="bootstrap-$(date +%s)-$$"
alias="sandbox-$name"
test_data_home="${XDG_DATA_HOME:-$repo_dir/tmp/test-data}"
XDG_DATA_HOME="$test_data_home" IMAGE_NAME="$name" make -C "$repo_dir" stage-bootstrap-test-image
image_root="$test_data_home/sandboxctl/images/$name"

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
XDG_DATA_HOME="$test_data_home" e2e_sandboxctl image install "$name" --version "$version"
e2e_sandboxctl create "$name" --cpus 2 --memory 2048 --disk-size 1 --image "$name"
e2e_sandboxctl start "$name"

ssh_output=$(mktemp "$repo_dir/tmp/logs/.guest-bootstrap-ssh.XXXXXX")
trap 'rm -f -- "$ssh_output"' EXIT
wait_for_guest_bootstrap() {
  local boot=$1
  local deadline=$((SECONDS + 120))
  while (( SECONDS < deadline )); do
    if timeout --foreground --kill-after=1s 5s ssh -F "$XDG_CONFIG_HOME/sandboxctl/ssh_config" -o BatchMode=yes -o ConnectTimeout=3 -o StrictHostKeyChecking=accept-new "$alias" \
      "test -r /var/lib/sandboxctl/bootstrap-complete && grep -qx 'hostname=$name' /var/lib/sandboxctl/bootstrap-complete && test \"\$(hostname)\" = '$name' && test \"\$(id -un)\" = dev" \
      >"$ssh_output" 2>&1; then
      ssh -F "$XDG_CONFIG_HOME/sandboxctl/ssh_config" -o BatchMode=yes -o ConnectTimeout=3 -o StrictHostKeyChecking=accept-new "$alias" \
        'cat /var/lib/sandboxctl/bootstrap-complete; hostname; id -un'
      printf 'PASS guest bootstrap and public-key SSH after %s for %s.\n' "$boot" "$name"
      return 0
    fi
    sleep 2
  done

  printf 'The last SSH check failed or timed out after %s.\n' "$boot" >&2
  cat "$ssh_output"
  printf 'Guest bootstrap did not complete within 120 seconds.\n' >&2
  printf 'Sandbox %s remains available for diagnosis.\n' "$name" >&2
  printf 'Guest console log: /var/lib/sandboxctl/instances/%s/console.log\n' "$name" >&2
  return 1
}

wait_for_guest_bootstrap 'first boot'
e2e_sandboxctl stop "$name"
e2e_sandboxctl start "$name"
wait_for_guest_bootstrap 'restart'
if [[ ${E2E_CLEANUP:-0} == 1 ]]; then
  e2e_sandboxctl stop "$name"
  e2e_sandboxctl delete "$name" --yes
  e2e_sandboxctl image delete "$name" --yes
else
  printf 'The sandbox remains running. Stop it with: sandboxctl stop %s\n' "$name"
fi
