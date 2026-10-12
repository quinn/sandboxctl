#!/usr/bin/env bash
# Build the initramfs used to boot sandboxctl guest images.
#
# Usage: make guest-initramfs, or run this script from any directory.
# Build inputs are cmd/guest/overlay-init and cmd/guest/bootstrap. The script
# compiles guest-overlay-init and guest-bootstrap as static Linux binaries and
# stores them under those names in the archive. The kernel and guest init code
# use the same names. It writes tmp/config/initramfs/overlay-initramfs.cpio.gz.
# make stage-builder-image and packaging/local/PKGBUILD use this output.
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
CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" go build -trimpath -ldflags='-s -w' -o "$stage/guest-overlay-init" ./cmd/guest/overlay-init
CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" go build -trimpath -ldflags='-s -w' -o "$stage/guest-bootstrap" ./cmd/guest/bootstrap
chmod 0755 "$stage/guest-overlay-init" "$stage/guest-bootstrap"
mkdir -p "$output_dir"
chmod 0700 "$output_dir"
archive="$output_dir/.overlay-initramfs.cpio.gz.$$"
trap 'rm -rf -- "$stage"; rm -f -- "$archive"' EXIT
(cd "$stage" && printf '.\n./guest-bootstrap\n./guest-overlay-init\n' | cpio --reproducible -o -H newc --quiet | gzip -n) > "$archive"
chmod 0600 "$archive"
mv -f -- "$archive" "$output_dir/overlay-initramfs.cpio.gz"
printf 'Built %s\n' "$output_dir/overlay-initramfs.cpio.gz"
