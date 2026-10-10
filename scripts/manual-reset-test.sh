#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

repo_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
log_dir="$repo_dir/tmp/logs"
mkdir -p "$log_dir"
chmod 0700 "$log_dir"
log_file="$log_dir/reset-test.log"
touch "$log_file"
chmod 0600 "$log_file"
exec > >(tee -a "$log_file") 2>&1
export PATH="$repo_dir/bin:$PATH"

for program in sandboxctl ssh timeout; do
	command -v "$program" >/dev/null 2>&1 || {
		printf 'Required command not found: %s\n' "$program" >&2
		exit 1
	}
done

instance_name="reset-test-$(date +%s)-$$"
ssh_alias="sandbox-$instance_name"
image_name=${RESET_TEST_IMAGE:-default}
ssh_output=$(mktemp "$log_dir/.reset-test-ssh.XXXXXX")
ssh_known_hosts=$(mktemp "$log_dir/.reset-test-known-hosts.XXXXXX")
ssh_options=(-o BatchMode=yes -o ConnectTimeout=3 -o StrictHostKeyChecking=accept-new -o "UserKnownHostsFile=$ssh_known_hosts")
trap 'rm -f -- "$ssh_output" "$ssh_known_hosts"' EXIT
trap 'result=$?; printf "Reset test failed. Sandbox %s remains for diagnosis.\n" "$instance_name" >&2; printf "Guest console log: /var/lib/sandboxctl/instances/%s/console.log\n" "$instance_name" >&2; exit "$result"' ERR

printf 'Create reset test sandbox %s from image %s.\n' "$instance_name" "$image_name"
"$repo_dir/bin/sandboxctl" create "$instance_name" --cpus 2 --memory 2048 --disk-size 1 --image "$image_name"
"$repo_dir/bin/sandboxctl" start "$instance_name"

wait_for_ssh() {
	local deadline=$((SECONDS + 120))
	while (( SECONDS < deadline )); do
		if timeout --foreground --kill-after=1s 5s ssh "${ssh_options[@]}" \
			"$ssh_alias" 'test -r /var/lib/sandboxctl/bootstrap-complete && test "$(id -un)" = dev' \
			>"$ssh_output" 2>&1; then
			return 0
		fi
		sleep 2
	done
	cat "$ssh_output" >&2
	printf 'Guest SSH did not become ready within 120 seconds.\n' >&2
	return 1
}

wait_for_ssh
ssh "${ssh_options[@]}" "$ssh_alias" \
	"printf retained > /home/dev/reset-test-marker"
printf 'Stop and reset sandbox %s.\n' "$instance_name"
"$repo_dir/bin/sandboxctl" stop "$instance_name"
"$repo_dir/bin/sandboxctl" reset "$instance_name"
: >"$ssh_known_hosts"
"$repo_dir/bin/sandboxctl" start "$instance_name"
wait_for_ssh
if ssh "${ssh_options[@]}" "$ssh_alias" \
	'test ! -e /home/dev/reset-test-marker'; then
	printf 'PASS reset removed writable guest state for %s.\n' "$instance_name"
else
	printf 'Reset marker still exists after reset.\n' >&2
	exit 1
fi

"$repo_dir/bin/sandboxctl" stop "$instance_name"
"$repo_dir/bin/sandboxctl" delete "$instance_name" --yes
printf 'Reset test log: %s\n' "$log_file"
