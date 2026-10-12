#!/usr/bin/env bash
set -euo pipefail

# shellcheck source=../lib.sh
source "$(dirname -- "${BASH_SOURCE[0]}")/../lib.sh"
repository_root=$repo_dir
log_path="$repository_root/tmp/logs/delete-test.log"
mkdir -p "$(dirname -- "$log_path")"
umask 077
chmod 0700 "$(dirname -- "$log_path")"
exec > >(tee "$log_path") 2>&1

instance_name="delete-test-$(date +%s)-$$"
image_name=${E2E_GUEST_IMAGE:-default}

printf 'Create stopped test sandbox %s.\n' "$instance_name"
e2e_sandboxctl create "$instance_name" \
	--cpus 1 --memory 512 --disk-size 1 --image "$image_name"

printf 'Delete stopped test sandbox %s.\n' "$instance_name"
e2e_sandboxctl delete "$instance_name" --yes

if e2e_sandboxctl list | awk -F '\t' -v name="$instance_name" 'NR > 1 && $1 == name { found = 1 } END { exit !found }'; then
	printf 'Deleted sandbox %s still appears in the instance list.\n' "$instance_name" >&2
	exit 1
fi

printf 'PASS stopped sandbox deletion for %s.\n' "$instance_name"
printf 'Test log: %s\n' "$log_path"
