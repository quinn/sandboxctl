#!/usr/bin/env bash
# Build GitHub release packages from the checked-out version tag.
set -Eeuo pipefail
umask 077

repo_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
if [[ $# -lt 1 || $# -gt 2 || ! $1 =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  printf 'Usage: %s vMAJOR.MINOR.PATCH [OUTPUT_DIRECTORY]\n' "${0##*/}" >&2
  exit 2
fi
tag=$1
version=${tag#v}
if [[ $EUID == 0 ]]; then
  printf 'Build release packages as an unprivileged user.\n' >&2
  exit 1
fi
for program in git make makepkg python3 tar zstd; do
  command -v "$program" >/dev/null 2>&1 || {
    printf 'Required command not found: %s\n' "$program" >&2
    exit 1
  }
done
commit=$(git -C "$repo_dir" rev-parse --verify "refs/tags/$tag^{commit}")
if [[ $commit != "$(git -C "$repo_dir" rev-parse HEAD)" ]]; then
  printf 'Check out release tag %s before building.\n' "$tag" >&2
  exit 1
fi
require_clean() {
  if [[ -n $(git -C "$repo_dir" status --porcelain --untracked-files=all) ]]; then
    printf 'Commit or remove checkout changes before building a release.\n' >&2
    exit 1
  fi
}
require_clean
make -C "$repo_dir" check
require_clean

output=${2:-"$repo_dir/dist/$tag"}
# Resolve the directory before makepkg changes its working directory.
output=$(python3 -c 'import os,sys; print(os.path.abspath(sys.argv[1]))' "$output")
if [[ -e $output || -L $output ]]; then
  printf 'Release output already exists: %s\n' "$output" >&2
  exit 1
fi
mkdir -p -- "$(dirname -- "$output")"
mkdir -m 0700 -- "$output"
stage=$(mktemp -d "${TMPDIR:-/tmp}/sandboxctl-release.XXXXXX")
trap 'rm -rf -- "$stage"' EXIT
install -m 0600 "$repo_dir/packaging/local/PKGBUILD" "$stage/PKGBUILD"
cp /etc/makepkg.conf "$stage/makepkg.conf"
{
  printf '\nPKGEXT=%q\n' '.pkg.tar.zst'
  printf 'PKGDEST=%q\n' "$output"
} >> "$stage/makepkg.conf"
export SANDBOXCTL_PACKAGE_SOURCE="$repo_dir"
export SANDBOXCTL_RELEASE_VERSION="$version"
export SOURCE_DATE_EPOCH
SOURCE_DATE_EPOCH=$(git -C "$repo_dir" show -s --format=%ct "$commit")
(
  cd -- "$stage"
  makepkg --config "$stage/makepkg.conf" --syncdeps --cleanbuild
)
python3 "$repo_dir/scripts/release-artifacts.py" create "$output" "$tag" "$commit"
printf 'Release files: %s\n' "$output"
