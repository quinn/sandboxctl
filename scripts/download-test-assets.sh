#!/usr/bin/env bash
set -Eeuo pipefail

repo_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
output_dir="$repo_dir/tmp/config/images/default"
stage_parent="$repo_dir/tmp/config"
refresh=${REFRESH:-0}
prefix=${FIRECRACKER_CI_PREFIX:-}
s3=https://s3.amazonaws.com/spec.ccfc.min

for program in curl python3 sort tail unsquashfs truncate mkfs.ext4 sha256sum sudo id; do
  command -v "$program" >/dev/null || { printf 'Required command not found: %s\n' "$program" >&2; exit 1; }
done

arch=$(uname -m)
case "$arch" in
  x86_64|aarch64) ;;
  *) printf 'Unsupported host architecture: %s\n' "$arch" >&2; exit 1 ;;
esac

if [[ -d $output_dir && $refresh != 1 ]]; then
  if [[ -f $output_dir/SHA256SUMS ]] && (cd "$output_dir" && sha256sum --check --status SHA256SUMS); then
    if [[ ! -f $output_dir/root.ext4 ]]; then
      printf 'Kernel and SquashFS assets match their checksums. Building the missing ext4 root drive from the existing SquashFS.\n'
      "$repo_dir/scripts/create-test-root-drive.sh"
    fi
    printf 'Test assets exist and match their checksums: %s\n' "$output_dir"
    printf 'Set REFRESH=1 to fetch new upstream artifacts.\n'
    exit 0
  fi
  printf 'Test assets exist but do not match their checksums. Review or move %s, or run with REFRESH=1.\n' "$output_dir" >&2
  exit 1
fi

if [[ -z $prefix ]]; then
  prefix_listing=$(curl -fsSLG "$s3/" \
    --data-urlencode 'list-type=2' \
    --data-urlencode 'prefix=firecracker-ci/' \
    --data-urlencode 'delimiter=/')
  dated_prefixes=$(printf '%s' "$prefix_listing" \
    | python3 -c 'import sys, xml.etree.ElementTree as E; r=E.parse(sys.stdin).getroot(); print("\n".join(x.text or "" for x in r.iter() if x.tag.endswith("}Prefix") and x.text))' \
    | grep -E '^firecracker-ci/[0-9]{8}-[^/]+/$' || true)
  if [[ -z $dated_prefixes ]]; then
    printf 'No dated Firecracker CI release artifacts were listed. Check the upstream artifact listing.\n' >&2
    exit 1
  fi
  prefix=$(printf '%s\n' "$dated_prefixes" | sort | tail -n 1)
fi
if [[ ! $prefix =~ ^firecracker-ci/[0-9]{8}-[^/]+/$ ]]; then
  printf 'Invalid Firecracker CI artifact prefix: %s\n' "$prefix" >&2
  exit 1
fi

list_keys() {
  curl -fsSLG "$s3/" --data-urlencode 'list-type=2' --data-urlencode "prefix=$1" \
    | python3 -c 'import sys, xml.etree.ElementTree as E; r=E.parse(sys.stdin).getroot(); print("\n".join(x.text or "" for x in r.iter() if x.tag.endswith("}Key") and x.text))'
}

kernel_key=$(list_keys "${prefix}${arch}/vmlinux-" \
  | grep -E "^${prefix}${arch}/vmlinux-[0-9]+\.[0-9]+\.[0-9]+$" \
  | sort -V | tail -n 1)
ubuntu_key=$(list_keys "${prefix}${arch}/ubuntu-" \
  | grep -E "^${prefix}${arch}/ubuntu-[0-9]+\.[0-9]+\.squashfs$" \
  | sort -V | tail -n 1)
if [[ -z $kernel_key || -z $ubuntu_key ]]; then
  printf 'Could not find a kernel and Ubuntu filesystem under %s for %s.\n' "$prefix" "$arch" >&2
  exit 1
fi

mkdir -p "$stage_parent"
stage=$(mktemp -d "$stage_parent/.test-assets.XXXXXX")
root_owned=0
cleanup() {
  status=$?
  trap - EXIT
  if [[ -e $stage ]]; then
    if (( root_owned )); then sudo rm -rf -- "$stage"; else rm -rf -- "$stage"; fi
  fi
  exit "$status"
}
trap cleanup EXIT

curl -fL --retry 3 "$s3/$kernel_key" -o "$stage/vmlinux"
curl -fL --retry 3 "$s3/$ubuntu_key" -o "$stage/ubuntu.squashfs"
root_owned=1
sudo unsquashfs -no-progress -d "$stage/rootfs" "$stage/ubuntu.squashfs"
truncate -s 1G "$stage/root.ext4"
sudo mkfs.ext4 -q -F -d "$stage/rootfs" "$stage/root.ext4"
sudo chown "$(id -u):$(id -g)" "$stage/root.ext4"
chmod 0600 "$stage/root.ext4"
sudo rm -rf -- "$stage/rootfs"
root_owned=0

cat > "$stage/manifest.txt" <<EOF
Firecracker CI artifact prefix: $prefix
Architecture: $arch
Kernel source: $s3/$kernel_key
Ubuntu filesystem source: $s3/$ubuntu_key
The Ubuntu SquashFS filesystem was converted to a 1 GiB ext4 image.
EOF
(cd "$stage" && sha256sum vmlinux ubuntu.squashfs root.ext4 > SHA256SUMS)
chmod 0600 "$stage/vmlinux" "$stage/ubuntu.squashfs" "$stage/root.ext4" "$stage/manifest.txt" "$stage/SHA256SUMS"

backup="$stage_parent/.default-backup.$$"
if [[ -e $output_dir ]]; then mv -- "$output_dir" "$backup"; fi
if ! mv -- "$stage" "$output_dir"; then
  [[ ! -e $backup ]] || mv -- "$backup" "$output_dir"
  exit 1
fi
if [[ -e $backup ]]; then sudo rm -rf -- "$backup"; fi
printf 'Downloaded test assets to %s\n' "$output_dir"
printf 'Check local file integrity with: (cd %s && sha256sum --check SHA256SUMS)\n' "$output_dir"
printf 'See manifest.txt for the upstream artifact URLs.\n'
