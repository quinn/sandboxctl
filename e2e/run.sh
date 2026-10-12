#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

e2e_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=lib.sh
source "$e2e_root/lib.sh"

if (( EUID == 0 )); then
  printf 'Run the E2E suite as the configured operator, not as root.\n' >&2
  exit 1
fi

installed_bin=$(command -v sandboxctl || true)
if [[ -z $installed_bin ]]; then
  printf 'The installed sandboxctl package is required. Run make install first.\n' >&2
  exit 1
fi
installed_bin=$(readlink -f -- "$installed_bin")
repo_bin=$(readlink -f -- "$repo_dir/bin/sandboxctl" 2>/dev/null || true)
if [[ -n $repo_bin && $installed_bin == "$repo_bin" ]]; then
  printf 'The E2E suite must use the installed sandboxctl. Remove the workspace bin directory from PATH.\n' >&2
  exit 1
fi
export SANDBOXCTL_BIN=$installed_bin

run_id="$(date +%s)-$$"
export E2E_RUN_ID=$run_id
e2e_state="$repo_dir/tmp/e2e/$run_id"
install -d -m 0700 "$e2e_state/data" "$e2e_state/config" "$e2e_state/state"
export E2E_WORK_DIR="$e2e_state"
export XDG_DATA_HOME="$e2e_state/data"
export XDG_CONFIG_HOME="$e2e_state/config"
export XDG_STATE_HOME="$e2e_state/state"
export E2E_CLEANUP=1
export E2E_GUEST_IMAGE="guest-e2e-$run_id"

printf 'E2E run %s\n' "$run_id"
printf 'Installed CLI: %s\n' "$SANDBOXCTL_BIN"
printf 'Private test data: %s\n' "$e2e_state"

e2e_sandboxctl init
e2e_sandboxctl doctor

steps=(
  manual-bootstrap-test.sh
  manual-builder-test.sh
  manual-image-build.sh
  manual-dockerfile-build.sh
  manual-dockerfile-rejections.sh
  manual-delete-test.sh
  manual-reset-test.sh
)

for step in "${steps[@]}"; do
  printf '\n===== %s =====\n' "$step"
  "$e2e_root/steps/$step"
done

printf '\nPASS full sandboxctl E2E suite.\n'
printf 'Logs and isolated user data remain at %s for review.\n' "$e2e_state"
