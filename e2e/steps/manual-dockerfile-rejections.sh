#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

# shellcheck source=../lib.sh
source "$(dirname -- "${BASH_SOURCE[0]}")/../lib.sh"
data_home="${XDG_DATA_HOME:-$repo_dir/tmp/dockerfile-rejection-data}"
export XDG_DATA_HOME="$data_home"
export XDG_CONFIG_HOME="${XDG_CONFIG_HOME:-$HOME/.config}"
export XDG_STATE_HOME="${XDG_STATE_HOME:-$repo_dir/tmp/dockerfile-rejection-state}"
log_dir="$repo_dir/tmp/logs"
mkdir -p "$log_dir"
chmod 0700 "$log_dir"
log_file="$log_dir/dockerfile-rejection-test.log"
touch "$log_file"
chmod 0600 "$log_file"
exec > >(tee -a "$log_file") 2>&1

for program in python3 timeout; do
  command -v "$program" >/dev/null 2>&1 || {
    printf 'Required command not found: %s\n' "$program" >&2
    exit 1
  }
done
timestamp=$(date +%s)
context_parent="${E2E_WORK_DIR:-$repo_dir/tmp}"
mkdir -p "$context_parent"
context_dir=$(mktemp -d "$context_parent/dockerfile-rejections.XXXXXX")
chmod 0700 "$context_dir"
mkdir -p "$data_home" "$XDG_STATE_HOME"
chmod 0700 "$data_home" "$XDG_STATE_HOME"
cleanup() {
  rm -rf -- "$context_dir"
}
trap cleanup EXIT

base_image='ubuntu:24.04@sha256:224a1869083a311ef3f13648a154ba79832fbef6364d31493642ca03082da254'
run_rejection() {
  local case_name=$1 expected=$2 guest_version=$3 image_name="reject-$timestamp-$1"
  local output="$log_dir/.dockerfile-rejection-$case_name.out"
  rm -f -- "$output"
  if timeout --foreground --kill-after=5s 240s "$SANDBOXCTL_BIN" image build "$image_name" \
    --dockerfile "$context_dir/Dockerfile" \
    --context "$context_dir" \
    --guest-version "$guest_version" >"$output" 2>&1; then
    cat "$output"
    printf 'FAIL %s: invalid image build succeeded.\n' "$case_name" >&2
    return 1
  fi
  if ! grep -Fq -- "$expected" "$output"; then
    cat "$output" >&2
    printf 'FAIL %s: expected error text was missing: %s\n' "$case_name" "$expected" >&2
    return 1
  fi
  if compgen -G "$data_home/sandboxctl/images/$image_name/*/manifest.json" >/dev/null; then
    printf 'FAIL %s: rejected image has a published manifest.\n' "$case_name" >&2
    return 1
  fi
  printf 'PASS %s was rejected and no image was published.\n' "$case_name"
  rm -f -- "$output"
}

printf 'Dockerfile rejection test log: %s\n' "$log_file"
cat >"$context_dir/Dockerfile" <<DOCKERFILE
FROM $base_image
RUN exit 47
DOCKERFILE
run_rejection failed-command 'BuildKit user image build failed' 24.04

cat >"$context_dir/Dockerfile" <<'DOCKERFILE'
FROM ubuntu:24.04
DOCKERFILE
run_rejection unpinned 'BuildKit user image build failed' 24.04

cat >"$context_dir/Dockerfile" <<'DOCKERFILE'
FROM scratch
DOCKERFILE
run_rejection unsupported 'guest provisioning build failed' 24.04

cat >"$context_dir/Dockerfile" <<DOCKERFILE
FROM $base_image
DOCKERFILE
run_rejection version 'expected guest version 22.04, found 24.04' 22.04

python3 - "$context_dir/Dockerfile" <<'PY'
import pathlib
import sys

pathlib.Path(sys.argv[1]).write_bytes(b"#" + b"x" * (1024 * 1024) + b"\n")
PY
run_rejection oversized 'file is not regular or exceeds the size limit' 24.04

printf 'PASS Dockerfile rejection checks.\n'
printf 'Test log: %s\n' "$log_file"
