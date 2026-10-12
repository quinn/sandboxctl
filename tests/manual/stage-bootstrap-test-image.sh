#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
data_home=${XDG_DATA_HOME:-"$HOME/.local/share"}
image_name=${IMAGE_NAME:-default}
source_dir="$repo_dir/tmp/config/images/default"
initramfs="$repo_dir/tmp/config/initramfs/overlay-initramfs.cpio.gz"
[[ $image_name =~ ^[a-z][a-z0-9-]{0,31}$ ]] || {
  printf 'Invalid image name: %s\n' "$image_name" >&2
  exit 1
}
image_root="$data_home/sandboxctl/images/$image_name"

for file in "$source_dir/vmlinux" "$source_dir/ubuntu.squashfs" "$initramfs"; do
  [[ -f $file && -s $file ]] || {
    printf 'Required image file is missing or empty: %s\n' "$file" >&2
    exit 1
  }
done

command -v python3 >/dev/null 2>&1 || {
  printf 'Required command not found: python3\n' >&2
  exit 1
}

install -d -m 0700 "$image_root"
chmod 0700 "$image_root"
stage=$(mktemp -d "$image_root/.overlay-image.XXXXXX")
trap 'rm -rf -- "$stage"' EXIT
chmod 0700 "$stage"
install -m 0600 "$source_dir/vmlinux" "$stage/vmlinux"
install -m 0600 "$initramfs" "$stage/initramfs.cpio.gz"
install -m 0600 "$source_dir/ubuntu.squashfs" "$stage/root.squashfs"

version=$(python3 - "$stage" "$image_name" <<'PY'
import hashlib
import json
import pathlib
import struct
import sys

directory = pathlib.Path(sys.argv[1])
image_name = sys.argv[2]
artifacts = {}
for name in ("vmlinux", "initramfs.cpio.gz", "root.squashfs"):
    path = directory / name
    hasher = hashlib.sha256()
    size = 0
    with path.open("rb") as source:
        while chunk := source.read(1024 * 1024):
            size += len(chunk)
            hasher.update(chunk)
    digest = hasher.hexdigest()
    artifacts[name] = {"size": size, "sha256": digest}
manifest = {
    "schema_version": 1,
    "name": image_name,
    "architecture": "x86_64",
    "firecracker_compatibility": "1.17.x",
    "kernel_version": "6.1.155",
    "guest": {"distribution": "Ubuntu", "version": "24.04"},
    "artifacts": artifacts,
    "kernel_args": ["console=ttyS0", "panic=1", "reboot=k", "rdinit=/guest-overlay-init"],
    "provenance": "Firecracker CI v1.15 kernel and Ubuntu 24.04 SquashFS; sandboxctl overlay initramfs",
}
hasher = hashlib.sha256()
def write(value):
    encoded = value.encode()
    hasher.update(struct.pack(">Q", len(encoded)))
    hasher.update(encoded)

write("sandboxctl-image-manifest-v1")
write(str(manifest["schema_version"]))
write(manifest["name"])
write(manifest["architecture"])
write(manifest["firecracker_compatibility"])
write(manifest["kernel_version"])
write(manifest["guest"]["distribution"])
write(manifest["guest"]["version"])
write(manifest["provenance"])
write(str(len(manifest["kernel_args"])))
for argument in manifest["kernel_args"]:
    write(argument)
for name in ("vmlinux", "initramfs.cpio.gz", "root.squashfs"):
    write(name)
    write(str(artifacts[name]["size"]))
    write(artifacts[name]["sha256"])
version = "sha256:" + hasher.hexdigest()
manifest["version"] = version
(directory / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
print(version)
PY
)
chmod 0600 "$stage/manifest.json"

destination="$image_root/$version"
if [[ -e $destination ]]; then
  if [[ -d $destination && ! -L $destination ]] && cmp -s "$stage/manifest.json" "$destination/manifest.json" && cmp -s "$stage/vmlinux" "$destination/vmlinux" && cmp -s "$stage/initramfs.cpio.gz" "$destination/initramfs.cpio.gz" && cmp -s "$stage/root.squashfs" "$destination/root.squashfs"; then
    printf 'Image bundle is already staged at %s\n' "$destination"
    printf 'Install it with: sandboxctl image install %s --version %s\n' "$image_name" "$version"
    exit 0
  fi
  printf 'An image bundle already exists with different files: %s\n' "$destination" >&2
  exit 1
fi
mv -- "$stage" "$destination"
trap - EXIT
chmod 0700 "$destination"
printf 'Staged image bundle at %s\n' "$destination"
printf 'Install it with: sandboxctl image install %s --version %s\n' "$image_name" "$version"
