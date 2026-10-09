#!/usr/bin/env bash
set -Eeuo pipefail

repo_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
output_dir="$repo_dir/tmp/config/initramfs"
mkdir -p "$repo_dir/tmp/config"
stage=$(mktemp -d "$repo_dir/tmp/config/.overlay-init.XXXXXX")
trap 'rm -rf -- "$stage"' EXIT

case "$(uname -m)" in
  x86_64) goarch=amd64 ;;
  aarch64) goarch=arm64 ;;
  *) printf 'Unsupported host architecture: %s\n' "$(uname -m)" >&2; exit 1 ;;
esac

cd "$repo_dir"
CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" go build -trimpath -ldflags='-s -w' -o "$stage/init" ./cmd/overlay-init
CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" go build -trimpath -ldflags='-s -w' -o "$stage/bootstrap" ./cmd/guest-bootstrap
chmod 0755 "$stage/init"
chmod 0755 "$stage/bootstrap"
mkdir -p "$output_dir"
chmod 0700 "$output_dir"
archive="$output_dir/.overlay-initramfs.cpio.gz.$$"
trap 'rm -rf -- "$stage"; rm -f -- "$archive"' EXIT
(cd "$stage" && printf '.\n./bootstrap\n./init\n' | cpio --reproducible -o -H newc --quiet | gzip -n) > "$archive"
chmod 0600 "$archive"
mv -f -- "$archive" "$output_dir/overlay-initramfs.cpio.gz"
printf 'Built %s\n' "$output_dir/overlay-initramfs.cpio.gz"
