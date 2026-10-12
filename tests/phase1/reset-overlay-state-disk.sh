#!/usr/bin/env bash
set -Eeuo pipefail

repo_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
state_dir="$repo_dir/tmp/config/state"
disk="$state_dir/overlay-proof.ext4"
lock_path="$disk.lock"
mkdir -p "$state_dir"
chmod 0700 "$state_dir"
exec 9>"$lock_path"
chmod 0600 "$lock_path"
if ! flock -n 9; then
  printf 'The overlay proof VM may still use the state disk. Stop it before reset.\n' >&2
  exit 1
fi
if [[ ! -f $disk ]]; then
  printf 'State disk does not exist: %s\n' "$disk" >&2
  exit 1
fi
printf 'This will erase the overlay proof state disk: %s\n' "$disk"
read -r -p 'Type RESET to continue: ' answer
if [[ $answer != RESET ]]; then
  printf 'Reset canceled.\n'
  exit 1
fi
rm -- "$disk"
truncate -s 1G "$disk"
if ! mkfs.ext4 -q -F "$disk"; then
  rm -f -- "$disk"
  exit 1
fi
chmod 0600 "$disk"
printf 'Created a new state disk: %s\n' "$disk"
