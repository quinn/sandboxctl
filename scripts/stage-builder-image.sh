#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

repo_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
source_dir="$repo_dir/tmp/config/images/default"
tools_dir="$repo_dir/tmp/config/builder/tools/v0.34.0"
data_home=${XDG_DATA_HOME:-"$HOME/.local/share"}
image_name=${IMAGE_NAME:-builder}
image_root="$data_home/sandboxctl/images/$image_name"
apt_snapshot=20261009T000000Z
ubuntu_index_digest=sha256:224a1869083a311ef3f13648a154ba79832fbef6364d31493642ca03082da254
buildkit_archive_sha256=14813b1fb2a40082114e6d386c6fc5e7dd5298176ce7ee1a18bc33e73e109ac3

for program in podman mksquashfs unsquashfs python3 sha256sum; do
  command -v "$program" >/dev/null 2>&1 || {
    printf 'Required command not found: %s\n' "$program" >&2
    exit 1
  }
done

if [[ $(uname -m) != x86_64 ]]; then
  printf 'Builder images support x86_64 hosts only.\n' >&2
  exit 1
fi

if [[ ! $image_name =~ ^[a-z][a-z0-9-]{0,31}$ ]]; then
  printf 'Invalid builder image name: %s\n' "$image_name" >&2
  exit 1
fi

for file in \
  "$source_dir/vmlinux" \
  "$source_dir/ubuntu.squashfs" \
  "$repo_dir/tmp/config/initramfs/overlay-initramfs.cpio.gz" \
  "$tools_dir/buildkit-v0.34.0.linux-amd64.tar.gz" \
  "$tools_dir/bin/buildctl" \
  "$tools_dir/bin/buildkitd" \
  "$tools_dir/bin/buildkit-runc"; do
  [[ -s $file && ! -L $file ]] || {
    printf 'Required builder image input is missing or invalid: %s\n' "$file" >&2
    exit 1
  }
done

printf '%s  %s\n' "$buildkit_archive_sha256" "$tools_dir/buildkit-v0.34.0.linux-amd64.tar.gz" | sha256sum --check --status || {
  printf 'BuildKit archive checksum does not match. Run make download-builder-tools.\n' >&2
  exit 1
}

install -d -m 0700 "$image_root"
chmod 0700 "$image_root"
staging_image_root=$image_root
stage=$(mktemp -d "$image_root/.builder-image.XXXXXX")
chmod 0700 "$stage"
podman_store=$(mktemp -d "${TMPDIR:-/tmp}/sandboxctl-builder-podman.XXXXXX")
podman_root="$podman_store/storage"
podman_runroot="$podman_store/run"
mkdir -m 0700 "$podman_root" "$podman_runroot"
context="$stage/context"
mkdir -m 0700 "$context" "$stage/bundle"
container_id=
image_tag="localhost/sandboxctl-builder-seed:${stage##*.builder-image.}"
cleanup() {
  if [[ -n $container_id ]]; then
    podman_cmd rm --force "$container_id" >/dev/null 2>&1 || true
  fi
  podman_cmd image rm "$image_tag" >/dev/null 2>&1 || true
  podman_cmd system reset --force >/dev/null 2>&1 || true
  rm -rf -- "$stage"
  rm -rf -- "$podman_store"
  if [[ $staging_image_root != "$image_root" ]]; then
    rmdir -- "$staging_image_root" 2>/dev/null || true
  fi
}
trap cleanup EXIT

podman_cmd() {
  podman --root "$podman_root" --runroot "$podman_runroot" "$@"
}

install -m 0600 "$repo_dir/scripts/builder-base.Dockerfile" "$context/Dockerfile"
install -d -m 0700 "$context/buildkit/bin"
for binary in buildctl buildkitd buildkit-runc; do
  install -m 0700 "$tools_dir/bin/$binary" "$context/buildkit/bin/$binary"
done

printf 'Building the pinned Ubuntu builder seed image.\n'
podman_cmd build \
  --pull=missing \
  --platform linux/amd64 \
  --build-arg "APT_SNAPSHOT=$apt_snapshot" \
  --file "$context/Dockerfile" \
  --tag "$image_tag" \
  "$context"
container_id=$(podman_cmd create "$image_tag")
bundle="$stage/bundle"
install -m 0600 "$source_dir/vmlinux" "$bundle/vmlinux"
install -m 0600 "$repo_dir/tmp/config/initramfs/overlay-initramfs.cpio.gz" "$bundle/initramfs.cpio.gz"
podman_cmd export "$container_id" | mksquashfs - "$bundle/root.squashfs" \
  -tar \
  -numeric-owner \
  -default-uid 0 \
  -default-gid 0 \
  -noappend \
  -comp xz \
  -b 1M \
  -no-xattrs \
  -no-progress \
  -all-time 0

unsquashfs -s "$bundle/root.squashfs" >/dev/null
for required_path in \
  usr/local/bin/buildctl \
  usr/local/bin/buildkitd \
  usr/local/bin/buildkit-runc \
  usr/bin/mksquashfs \
  usr/sbin/sshd \
  usr/bin/sudo \
  usr/lib/systemd/systemd; do
  listing=$(unsquashfs -ll "$bundle/root.squashfs" "$required_path" 2>/dev/null) || listing=
  if ! grep -Fq "squashfs-root/$required_path" <<<"$listing"; then
    printf 'Builder root filesystem is missing %s.\n' "$required_path" >&2
    exit 1
  fi
done

if [[ $image_name == auto ]]; then
  image_name=$(python3 - "$bundle" <<'PY'
import hashlib
import pathlib
import sys

directory = pathlib.Path(sys.argv[1])
hasher = hashlib.sha256()
for name in ("vmlinux", "initramfs.cpio.gz", "root.squashfs"):
    artifact_hasher = hashlib.sha256()
    with (directory / name).open("rb") as source:
        while chunk := source.read(1024 * 1024):
            artifact_hasher.update(chunk)
    hasher.update(name.encode())
    hasher.update(artifact_hasher.digest())
print("builder-" + hasher.hexdigest()[:16])
PY
  )
  image_root="$data_home/sandboxctl/images/$image_name"
  install -d -m 0700 "$image_root"
  chmod 0700 "$image_root"
fi

python3 - "$bundle" "$image_name" "$ubuntu_index_digest" "$apt_snapshot" "$buildkit_archive_sha256" <<'PY'
import hashlib
import json
import pathlib
import struct
import sys

directory = pathlib.Path(sys.argv[1])
image_name, ubuntu_digest, apt_snapshot, buildkit_digest = sys.argv[2:]
artifact_names = ("vmlinux", "initramfs.cpio.gz", "root.squashfs")
artifacts = {}
for name in artifact_names:
    path = directory / name
    hasher = hashlib.sha256()
    size = 0
    with path.open("rb") as source:
        while chunk := source.read(1024 * 1024):
            size += len(chunk)
            hasher.update(chunk)
    artifacts[name] = {"size": size, "sha256": hasher.hexdigest()}

image = {
    "schema_version": 1,
    "name": image_name,
    "architecture": "x86_64",
    "firecracker_compatibility": "1.17.x",
    "kernel_version": "6.1.155",
    "guest": {"distribution": "Ubuntu", "version": "24.04"},
    "artifacts": artifacts,
    "kernel_args": ["console=ttyS0", "panic=1", "reboot=k", "rdinit=/init"],
    "provenance": (
        f"Ubuntu OCI index {ubuntu_digest}; APT snapshot {apt_snapshot}; "
        f"ca-certificates 20240203; BuildKit v0.34.0 archive SHA-256 {buildkit_digest}; "
        "sandboxctl builder seed recipe v1"
    ),
}
hasher = hashlib.sha256()
def write(value):
    encoded = value.encode()
    hasher.update(struct.pack(">Q", len(encoded)))
    hasher.update(encoded)

write("sandboxctl-image-manifest-v1")
write(str(image["schema_version"]))
write(image["name"])
write(image["architecture"])
write(image["firecracker_compatibility"])
write(image["kernel_version"])
write(image["guest"]["distribution"])
write(image["guest"]["version"])
write(image["provenance"])
write(str(len(image["kernel_args"])))
for argument in image["kernel_args"]:
    write(argument)
for name in artifact_names:
    write(name)
    write(str(artifacts[name]["size"]))
    write(artifacts[name]["sha256"])
image["version"] = "sha256:" + hasher.hexdigest()
(directory / "manifest.json").write_text(json.dumps(image, indent=2) + "\n")
PY

chmod 0600 "$bundle"/*
version=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["version"])' "$bundle/manifest.json")
destination="$image_root/$version"
if [[ -e $destination ]]; then
  existing_version=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("version", ""))' "$destination/manifest.json" 2>/dev/null || true)
  if [[ $existing_version != "$version" ]]; then
    printf 'Builder image destination exists with different content: %s\n' "$destination" >&2
    exit 1
  fi
  for artifact in vmlinux initramfs.cpio.gz root.squashfs; do
    expected=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["artifacts"][sys.argv[2]]["sha256"])' "$destination/manifest.json" "$artifact")
    actual=$(sha256sum "$destination/$artifact" | cut -d ' ' -f 1)
    if [[ $actual != "$expected" ]]; then
      printf 'Existing builder image failed checksum validation: %s\n' "$destination/$artifact" >&2
      exit 1
    fi
  done
  rm -rf -- "$stage"
  printf 'Builder image already staged at %s\n' "$destination"
  printf 'Install it with: sandboxctl image install %s --version %s\n' "$image_name" "$version"
  exit 0
fi
mv -- "$bundle" "$destination"
chmod 0700 "$destination"
printf 'Staged pinned builder image at %s\n' "$destination"
printf 'Install it with: sandboxctl image install %s --version %s\n' "$image_name" "$version"
