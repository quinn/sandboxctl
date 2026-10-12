#!/usr/bin/env bash
set -euo pipefail

repository_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
log_path="$repository_root/tmp/logs/delete-test.log"
mkdir -p "$(dirname -- "$log_path")"
umask 077
exec > >(tee "$log_path") 2>&1
export PATH="$repository_root/bin:$PATH"

instance_name="delete-test-$(date +%s)-$$"

printf 'Create stopped test sandbox %s.\n' "$instance_name"
"$repository_root/bin/sandboxctl" create "$instance_name" \
	--cpus 1 --memory 512 --disk-size 1 --image default

printf 'Delete stopped test sandbox %s.\n' "$instance_name"
"$repository_root/bin/sandboxctl" delete "$instance_name" --yes

if "$repository_root/bin/sandboxctl" list | awk -F '\t' -v name="$instance_name" 'NR > 1 && $1 == name { found = 1 } END { exit !found }'; then
	printf 'Deleted sandbox %s still appears in the instance list.\n' "$instance_name" >&2
	exit 1
fi

printf 'PASS stopped sandbox deletion for %s.\n' "$instance_name"
printf 'Test log: %s\n' "$log_path"
