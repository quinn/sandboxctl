#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

repo_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
export PATH="$repo_dir/bin:$PATH"
log_dir="$repo_dir/tmp/logs"
data_home="$repo_dir/tmp/image-build-data"
export XDG_DATA_HOME="$data_home"
export XDG_CONFIG_HOME="$repo_dir/tmp/image-build-config"
export XDG_STATE_HOME="$repo_dir/tmp/image-build-state"
mkdir -p "$log_dir"
chmod 0700 "$log_dir"
log_file="$log_dir/image-build-test.log"
touch "$log_file"
chmod 0600 "$log_file"
exec > >(tee -a "$log_file") 2>&1

for program in make python3; do
  command -v "$program" >/dev/null 2>&1 || {
    printf 'Required command not found: %s\n' "$program" >&2
    exit 1
  }
done

if [[ ! -x $repo_dir/bin/sandboxctl ]]; then
  printf 'Build the workspace CLI first with make build.\n' >&2
  exit 1
fi

timestamp=$(date +%s)
builder_name="builder-$timestamp-$$"
guest_name="guest-$timestamp-$$"
base_image='ubuntu:24.04@sha256:224a1869083a311ef3f13648a154ba79832fbef6364d31493642ca03082da254'

printf 'Image build test log: %s\n' "$log_file"
printf 'Staging a private builder image as %s.\n' "$builder_name"
IMAGE_NAME="$builder_name" make -C "$repo_dir" stage-builder-image

builder_manifest_root="$data_home/sandboxctl/images/$builder_name"
builder_manifest=$(find "$builder_manifest_root" -mindepth 2 -maxdepth 2 -type f -name manifest.json -print -quit)
if [[ -z $builder_manifest ]]; then
  printf 'The staged builder manifest is missing.\n' >&2
  exit 1
fi
builder_version=$(python3 - "$builder_manifest" <<'PY'
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

printf 'Installing builder image %s at %s.\n' "$builder_name" "$builder_version"
"$repo_dir/bin/sandboxctl" image install "$builder_name" --version "$builder_version"

printf 'Building guest image %s from the pinned Ubuntu 24.04 base.\n' "$guest_name"
"$repo_dir/bin/sandboxctl" image build "$guest_name" \
  --base-image "$base_image" \
  --builder-image "$builder_name" \
  --builder-version "$builder_version" \
  --distribution ubuntu \
  --guest-version 24.04
