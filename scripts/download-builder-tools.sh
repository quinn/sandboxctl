#!/usr/bin/env bash
# Download and verify the pinned BuildKit tools used inside builder VMs.
set -euo pipefail

repo_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
asset_root="$repo_dir/tmp/config/builder/tools"
version='v0.34.0'
archive_name="buildkit-${version}.linux-amd64.tar.gz"
archive_url="https://github.com/moby/buildkit/releases/download/${version}/${archive_name}"
archive_sha256='14813b1fb2a40082114e6d386c6fc5e7dd5298176ce7ee1a18bc33e73e109ac3'
destination="$asset_root/$version"

for program in curl sha256sum tar awk; do
  command -v "$program" >/dev/null 2>&1 || {
    printf 'Required command not found: %s\n' "$program" >&2
    exit 1
  }
done

if [[ $(uname -m) != x86_64 ]]; then
  printf 'Builder tools support x86_64 hosts only.\n' >&2
  exit 1
fi

if [[ -e $destination ]]; then
  if [[ ! -d $destination || -L $destination || ! -f $destination/$archive_name || -L $destination/$archive_name || ! -f $destination/BUILDINFO || -L $destination/BUILDINFO || ! -f $destination/SHA256SUMS || -L $destination/SHA256SUMS || ! -d $destination/bin || -L $destination/bin ]]; then
    printf 'Builder tool directory is incomplete or unsafe: %s\n' "$destination" >&2
    exit 1
  fi
  for binary in buildctl buildkitd buildkit-runc; do
    if [[ ! -f $destination/bin/$binary || -L $destination/bin/$binary ]]; then
      printf 'Builder tool directory is missing bin/%s: %s\n' "$binary" "$destination" >&2
      exit 1
    fi
  done
  for binary_path in "$destination"/bin/*; do
    if [[ ! -f $binary_path || -L $binary_path ]]; then
      printf 'Builder tool directory contains an invalid binary path: %s\n' "$binary_path" >&2
      exit 1
    fi
    chmod 0700 "$binary_path"
  done
  printf '%s  %s\n' "$archive_sha256" "$destination/$archive_name" | sha256sum --check --status || {
    printf 'Builder tool archive checksum does not match: %s\n' "$destination/$archive_name" >&2
    exit 1
  }
  printf 'Keeping verified BuildKit %s tools in %s\n' "$version" "$destination"
  exit 0
fi

install -d -m 0700 "$asset_root"
stage=$(mktemp -d "$asset_root/.download.XXXXXX")
trap 'rm -rf -- "$stage"' EXIT
chmod 0700 "$stage"

curl --fail --location --retry 3 --proto '=https' --tlsv1.2 "$archive_url" -o "$stage/$archive_name"
printf '%s  %s\n' "$archive_sha256" "$stage/$archive_name" | sha256sum --check --status || {
  printf 'Downloaded BuildKit archive checksum does not match.\n' >&2
  exit 1
}

tar -tzf "$stage/$archive_name" | awk '
  /^\// { exit 1 }
  {
    count = split($0, parts, "/")
    for (part_index = 1; part_index <= count; part_index++) {
      if (parts[part_index] == "..") exit 1
    }
  }
  END { if (NR == 0) exit 1 }
' || {
  printf 'BuildKit archive contains an invalid path.\n' >&2
  exit 1
}

tar --no-same-owner --no-same-permissions -xzf "$stage/$archive_name" -C "$stage"
for binary in buildctl buildkitd buildkit-runc; do
  [[ -f $stage/bin/$binary && ! -L $stage/bin/$binary ]] || {
    printf 'BuildKit archive is missing bin/%s.\n' "$binary" >&2
    exit 1
  }
done
for binary_path in "$stage"/bin/*; do
  [[ -f $binary_path && ! -L $binary_path ]] || {
    printf 'BuildKit archive contains an invalid binary path: %s\n' "$binary_path" >&2
    exit 1
  }
  chmod 0700 "$binary_path"
done

cat > "$stage/BUILDINFO" <<EOF
BuildKit version: $version
Release URL: $archive_url
Archive SHA-256: $archive_sha256
Architecture: x86_64 Linux
License: Apache-2.0
License URL: https://github.com/moby/buildkit/blob/${version}/LICENSE
EOF
printf '%s  %s\n' "$archive_sha256" "$archive_name" > "$stage/SHA256SUMS"
chmod 0600 "$stage/$archive_name" "$stage/BUILDINFO" "$stage/SHA256SUMS"
chmod 0700 "$stage" "$stage/bin"
mv -- "$stage" "$destination"
trap - EXIT

printf 'Downloaded and verified BuildKit %s in %s\n' "$version" "$destination"
