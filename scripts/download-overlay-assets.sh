#!/usr/bin/env bash
set -euo pipefail

readonly asset_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/tmp/config/images/default"
readonly base_url='https://s3.amazonaws.com/spec.ccfc.min/firecracker-ci/v1.15/x86_64'
readonly kernel_name='vmlinux'
readonly squashfs_name='ubuntu.squashfs'
readonly kernel_url="$base_url/vmlinux-6.1.155"
readonly squashfs_url="$base_url/ubuntu-24.04.squashfs"
readonly checksum_name='OVERLAY-SHA256SUMS'

for program in curl sha256sum; do
  command -v "$program" >/dev/null 2>&1 || {
    printf 'Required command not found: %s\n' "$program" >&2
    exit 1
  }
done

if [[ $(uname -m) != x86_64 ]]; then
  printf 'This proof supports x86_64 hosts only.\n' >&2
  exit 1
fi

install -d -m 0700 "$asset_dir"
chmod 0700 "$asset_dir"

if [[ -f $asset_dir/$kernel_name && -f $asset_dir/$squashfs_name && -f $asset_dir/$checksum_name ]]; then
  (cd "$asset_dir" && sha256sum --check --strict "$checksum_name")
  printf 'Keeping verified overlay proof assets in %s\n' "$asset_dir"
  exit 0
fi

if [[ -f $asset_dir/$kernel_name && -f $asset_dir/$squashfs_name && -f $asset_dir/SHA256SUMS ]]; then
  stage=$(mktemp -d "$asset_dir/.download.XXXXXX")
  trap 'rm -rf -- "$stage"' EXIT
  awk -v kernel="$kernel_name" -v squashfs="$squashfs_name" '$2 == kernel || $2 == squashfs { print; count++ } END { if (count != 2) exit 1 }' "$asset_dir/SHA256SUMS" > "$stage/$checksum_name" || {
    printf 'The existing checksum file does not pin both overlay assets.\n' >&2
    exit 1
  }
  (cd "$asset_dir" && sha256sum --check --strict "$stage/$checksum_name")
  chmod 0600 "$stage/$checksum_name"
  mv -- "$stage/$checksum_name" "$asset_dir/$checksum_name"
  printf 'Recorded overlay checksums separately in %s/%s\n' "$asset_dir" "$checksum_name"
  exit 0
fi

if [[ -e $asset_dir/$kernel_name || -e $asset_dir/$squashfs_name || -e $asset_dir/$checksum_name ]]; then
  printf 'Found incomplete or unpinned assets in %s. Move them before retrying.\n' "$asset_dir" >&2
  exit 1
fi

stage=$(mktemp -d "$asset_dir/.download.XXXXXX")
trap 'rm -rf -- "$stage"' EXIT
chmod 0700 "$stage"

curl --fail --location --retry 3 --proto '=https' --tlsv1.2 "$kernel_url" -o "$stage/$kernel_name"
curl --fail --location --retry 3 --proto '=https' --tlsv1.2 "$squashfs_url" -o "$stage/$squashfs_name"

[[ -s $stage/$kernel_name && -s $stage/$squashfs_name ]] || {
  printf 'A downloaded overlay proof asset is empty.\n' >&2
  exit 1
}

(cd "$stage" && sha256sum "$kernel_name" "$squashfs_name" > "$checksum_name")
cat > "$stage/manifest.txt" <<EOF
Firecracker CI artifact set: v1.15
Host architecture: x86_64
Kernel source: $kernel_url
Root filesystem source: $squashfs_url
SHA-256 values: $checksum_name
The SHA-256 values pin the HTTPS downloads used for this local Phase 1 proof.
EOF

chmod 0600 "$stage/$kernel_name" "$stage/$squashfs_name" "$stage/$checksum_name" "$stage/manifest.txt"
mv -- "$stage/$kernel_name" "$asset_dir/$kernel_name"
mv -- "$stage/$squashfs_name" "$asset_dir/$squashfs_name"
mv -- "$stage/$checksum_name" "$asset_dir/$checksum_name"
mv -- "$stage/manifest.txt" "$asset_dir/manifest.txt"

printf 'Downloaded and pinned overlay proof assets in %s\n' "$asset_dir"
printf 'Kernel: Firecracker CI v1.15, Linux 6.1.155\n'
printf 'Root filesystem: Ubuntu 24.04 SquashFS\n'
