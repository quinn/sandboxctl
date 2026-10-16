#!/usr/bin/env bash
# Upload verified package files to a GitHub draft release.
set -Eeuo pipefail

repo_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
if [[ $# -ne 3 || ! $1 =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ || ! $3 =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]; then
  printf 'Usage: %s vMAJOR.MINOR.PATCH ARTIFACT_DIRECTORY OWNER/REPO\n' "${0##*/}" >&2
  exit 2
fi
tag=$1
artifacts=$2
repository=$3
command -v gh >/dev/null 2>&1 || {
  printf 'Required command not found: gh\n' >&2
  exit 1
}
python3 "$repo_dir/scripts/release-artifacts.py" verify "$artifacts" "$tag"
commit=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["source_commit"])' "$artifacts/release.json")
# The existing remote tag must identify the commit that produced the package.
object=$(gh api "repos/$repository/git/ref/tags/$tag" --jq '[.object.type, .object.sha] | @tsv')
read -r object_type remote_commit <<< "$object"
# Resolve annotated tags to their commit. Use a bounded loop for nested tags.
for depth in {1..16}; do
  [[ $object_type == tag ]] || break
  if [[ ! $remote_commit =~ ^([0-9a-f]{40}|[0-9a-f]{64})$ ]]; then
    printf 'The GitHub tag object is invalid.\n' >&2
    exit 1
  fi
  object=$(gh api "repos/$repository/git/tags/$remote_commit" --jq '[.object.type, .object.sha] | @tsv')
  read -r object_type remote_commit <<< "$object"
done
if [[ $object_type != commit || $remote_commit != "$commit" ]]; then
  printf 'The GitHub tag does not match the release source commit.\n' >&2
  exit 1
fi
packages=("$artifacts/sandboxctl-x86_64.pkg.tar.zst" "$artifacts/sandboxctl-builder-x86_64.pkg.tar.zst")
if is_draft=$(gh release view "$tag" --repo "$repository" --json isDraft --jq '.isDraft' 2>/dev/null); then
  if [[ $is_draft != true ]]; then
    printf 'The release is already published. Its files cannot be replaced.\n' >&2
    exit 1
  fi
  gh release upload "$tag" "${packages[@]}" "$artifacts/SHA256SUMS" "$artifacts/release.json" --repo "$repository" --clobber
else
  gh release create "$tag" "${packages[@]}" "$artifacts/SHA256SUMS" "$artifacts/release.json" \
    --repo "$repository" --verify-tag --draft --title "$tag" --generate-notes
fi
