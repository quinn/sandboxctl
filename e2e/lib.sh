#!/usr/bin/env bash

e2e_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_dir=$(cd -- "$e2e_root/.." && pwd)

e2e_run_id=${E2E_RUN_ID:-manual}
if [[ ! $e2e_run_id =~ ^[a-zA-Z0-9_-]+$ ]]; then
  printf 'Invalid E2E run ID.\n' >&2
  return 1 2>/dev/null || exit 1
fi

E2E_WORK_DIR="$repo_dir/tmp/e2e/$e2e_run_id"
export E2E_WORK_DIR
export XDG_CONFIG_HOME="$E2E_WORK_DIR/config"
export XDG_DATA_HOME="$E2E_WORK_DIR/data"
export XDG_STATE_HOME="$E2E_WORK_DIR/state"
install -d -m 0700 "$XDG_CONFIG_HOME" "$XDG_DATA_HOME" "$XDG_STATE_HOME"

if [[ -n ${SANDBOXCTL_BIN:-} ]]; then
  sandboxctl_bin=$SANDBOXCTL_BIN
elif [[ -x $repo_dir/bin/sandboxctl ]]; then
  sandboxctl_bin="$repo_dir/bin/sandboxctl"
else
  sandboxctl_bin=$(command -v sandboxctl || true)
fi

if [[ -z $sandboxctl_bin || ! -x $sandboxctl_bin ]]; then
  printf 'sandboxctl was not found. Install the package or set SANDBOXCTL_BIN.\n' >&2
  return 1 2>/dev/null || exit 1
fi

export SANDBOXCTL_BIN=$sandboxctl_bin

e2e_sandboxctl() {
	# Lifecycle checks do not attach the source checkout as a Git task.
	if [[ ${1:-} == create || ${1:-} == start ]]; then
		(cd / && "$SANDBOXCTL_BIN" "$@")
	else
		"$SANDBOXCTL_BIN" "$@"
	fi
}
