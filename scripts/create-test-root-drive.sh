#!/usr/bin/env bash
set -Eeuo pipefail

repo_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
asset_dir="$repo_dir/tmp/config/images/default"

for program in sha256sum unsquashfs truncate mkfs.ext4 sudo id; do
  command -v "$program" >/dev/null || { printf 'Required command not found: %s\n' "$program" >&2; exit 1; }
done

[[ -f $asset_dir/vmlinux && -f $asset_dir/ubuntu.squashfs ]] || {
  printf 'The kernel and Ubuntu SquashFS assets are required in %s.\n' "$asset_dir" >&2
  exit 1
}
[[ -f $asset_dir/SHA256SUMS ]] || {
  printf 'Missing asset checksum file: %s/SHA256SUMS\n' "$asset_dir" >&2
  exit 1
}
(cd "$asset_dir" && sha256sum --check --status SHA256SUMS) || {
  printf 'Source asset checksums do not match. Keep the files and review %s/SHA256SUMS.\n' "$asset_dir" >&2
  exit 1
}
if [[ -e $asset_dir/root.ext4 ]]; then
  printf 'Keeping existing root drive: %s/root.ext4\n' "$asset_dir"
  exit 0
fi

stage=$(mktemp -d "$repo_dir/tmp/config/.manual-root.XXXXXX")
rootfs_extracted=0
root_drive_installed=0
cleanup() {
  status=$?
  trap - EXIT
  if (( root_drive_installed )); then
    rm -f -- "$asset_dir/root.ext4"
  fi
  if (( rootfs_extracted )); then
    sudo rm -rf -- "$stage/rootfs"
  fi
  rm -rf -- "$stage"
  exit "$status"
}
trap cleanup EXIT

rootfs_extracted=1
sudo unsquashfs -no-progress -d "$stage/rootfs" "$asset_dir/ubuntu.squashfs"
truncate -s 1G "$stage/root.ext4"
sudo mkfs.ext4 -q -F -d "$stage/rootfs" "$stage/root.ext4"
sudo chown "$(id -u):$(id -g)" "$stage/root.ext4"
chmod 0600 "$stage/root.ext4"
sudo rm -rf -- "$stage/rootfs"
rootfs_extracted=0

ln -- "$stage/root.ext4" "$asset_dir/root.ext4"
root_drive_installed=1
{
  cat "$asset_dir/manifest.txt" 2>/dev/null || true
  printf '\nManual test root drive: generated as a 1 GiB ext4 image from ubuntu.squashfs.\n'
} > "$stage/manifest.txt"
chmod 0600 "$stage/manifest.txt"
mv -- "$stage/manifest.txt" "$asset_dir/manifest.txt"
(cd "$asset_dir" && sha256sum vmlinux ubuntu.squashfs root.ext4) > "$stage/SHA256SUMS"
chmod 0600 "$stage/SHA256SUMS"
mv -- "$stage/SHA256SUMS" "$asset_dir/SHA256SUMS"

root_drive_installed=0
printf 'Created private ext4 root drive: %s/root.ext4\n' "$asset_dir"
