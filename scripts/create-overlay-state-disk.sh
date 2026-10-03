#!/usr/bin/env bash
set -Eeuo pipefail

repo_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
state_dir="$repo_dir/tmp/config/state"
disk="$state_dir/overlay-proof.ext4"
mkdir -p "$state_dir"
chmod 0700 "$state_dir"
if [[ -e $disk ]]; then
  printf 'Keeping existing state disk: %s\n' "$disk"
  exit 0
fi
truncate -s 1G "$disk"
if ! mkfs.ext4 -q -F "$disk"; then
  rm -f -- "$disk"
  exit 1
fi
chmod 0600 "$disk"
printf 'Created %s\n' "$disk"
