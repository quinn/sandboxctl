#!/usr/bin/env bash

e2e_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_dir=$(cd -- "$e2e_root/.." && pwd)
: "${XDG_CONFIG_HOME:=$HOME/.config}"
export XDG_CONFIG_HOME

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
  "$SANDBOXCTL_BIN" "$@"
}
