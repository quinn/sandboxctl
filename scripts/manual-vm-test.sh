#!/usr/bin/env bash
set -Eeuo pipefail

if [[ $# -gt 1 ]]; then
  printf 'Usage: %s [SANDBOX_NAME]\n' "$0" >&2
  exit 2
fi

repo_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
export PATH="$repo_dir/bin:$PATH"
umask 077
log_dir="$repo_dir/tmp/logs"
install -d -m 0700 "$log_dir"
log_file="$log_dir/manual-test.log"
install -m 0600 /dev/null "$log_file"
exec > >(tee "$log_file") 2>&1
printf 'Full test log: %s\n' "$log_file"
printf 'Manual VM test started at %s\n' "$(date --iso-8601=seconds)"
asset_dir="$repo_dir/tmp/config/images/default"
kernel="$asset_dir/vmlinux"
root_drive="$asset_dir/root.ext4"
name=${1:-manualtest}
binary="$repo_dir/bin/sandboxctl"
guest_user=ubuntu
guest_ip=172.30.0.10
tap="fc-${name:0:12}"
alias="sandboxctl-${name}"

if [[ ! $name =~ ^[a-z][a-z0-9-]{0,31}$ ]]; then
  printf 'Sandbox name must start with a lower-case letter and use only lower-case letters, digits, or hyphens.\n' >&2
  exit 2
fi
if [[ ! $guest_ip =~ ^172\.30\.0\.([2-9]|[1-9][0-9]|1[0-9]{2}|2[0-4][0-9]|25[0-4])$ ]]; then
  printf 'GUEST_IP must be a usable address in 172.30.0.0/24.\n' >&2
  exit 2
fi

created_tap=0
started=0
cleanup() {
  status=$?
  trap - EXIT INT TERM
  if (( started )); then
    if "$binary" stop "$name"; then
      started=0
    else
      printf 'The sandbox may still run. Keeping TAP device %s.\n' "$tap" >&2
    fi
  fi
  if (( created_tap && ! started )); then
    sudo ip tuntap del dev "$tap" mode tap || true
  fi
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

for program in ip ssh sudo id; do
  command -v "$program" >/dev/null || { printf 'Required command not found: %s\n' "$program" >&2; exit 1; }
done
[[ -x $binary ]] || { printf 'Build sandboxctl first: %s is missing.\n' "$binary" >&2; exit 1; }
[[ -f $kernel && -r $kernel ]] || { printf 'Kernel not found. Run `make download-test-assets` first.\n' >&2; exit 1; }
[[ -f $root_drive && -r $root_drive ]] || { printf 'Root drive not found. Run `make download-test-assets` first.\n' >&2; exit 1; }
command -v firecracker >/dev/null || { printf 'Firecracker is not in PATH.\n' >&2; exit 1; }
[[ -r /dev/kvm && -w /dev/kvm ]] || { printf 'This user needs read and write access to /dev/kvm.\n' >&2; exit 1; }

"$binary" init
if ip link show dev "$tap" >/dev/null 2>&1; then
  printf 'TAP device already exists: %s. Stop any sandbox that uses it before retrying.\n' "$tap" >&2
  exit 1
fi
sudo ip tuntap add dev "$tap" mode tap user "$(id -un)"
created_tap=1
sudo ip address add 172.30.0.1/24 dev "$tap"
sudo ip link set dev "$tap" up

if "$binary" list | awk -v name="$name" '$1 == name { found=1 } END { exit !found }'; then
  printf 'Using existing sandbox %s. Its private disk is unchanged if you prepared the base image again.\n' "$name"
else
  "$binary" create "$name" --guest-ip "$guest_ip" --ssh-user "$guest_user" --kernel "$kernel" --root-drive "$root_drive"
fi

"$repo_dir/scripts/prepare-manual-test-guest.sh" "$name"

effective_host=$(ssh -G "$alias" 2>/dev/null | awk '$1 == "hostname" { print $2; exit }')
if [[ $effective_host != "$guest_ip" ]]; then
  printf 'Add this block to your main SSH config:\n\n%s\n' "$("$binary" ssh-config)"
  read -r -p 'Press Enter after you add the block. '
  effective_host=$(ssh -G "$alias" 2>/dev/null | awk '$1 == "hostname" { print $2; exit }')
  if [[ $effective_host != "$guest_ip" ]]; then
    printf 'SSH alias %s still does not resolve to %s.\n' "$alias" "$guest_ip" >&2
    exit 1
  fi
fi
effective_user=$(ssh -G "$alias" 2>/dev/null | awk '$1 == "user" { print $2; exit }')
if [[ $effective_user != $guest_user ]]; then
  printf 'SSH alias %s uses user %s. This test needs %s. Use a new sandbox name or prepare its saved instance for %s.\n' \
    "$alias" "${effective_user:-unknown}" "$guest_user" "$guest_user" >&2
  exit 1
fi

"$binary" start "$name" --dry-run
"$binary" start "$name"
started=1

marker="/home/ubuntu/.sandboxctl-persistence-${name}-$$"
token="sandboxctl-${name}-$$"
deadline=$((SECONDS + 180))
printf 'Waiting for SSH at %s. Accept the guest host key when SSH asks.\n' "$alias"
until ssh -o ConnectTimeout=3 \
  -o PreferredAuthentications=publickey \
  -o PasswordAuthentication=no \
  -o KbdInteractiveAuthentication=no \
  "$alias" "printf '%s\\n' '$token' > '$marker' && sync"; do
  if (( SECONDS >= deadline )); then
    printf 'SSH did not become ready within 180 seconds. Check the sandbox log.\n' >&2
    exit 1
  fi
  sleep 3
done

"$binary" stop "$name"
started=0
"$binary" start "$name"
started=1
deadline=$((SECONDS + 180))
until ssh -o ConnectTimeout=3 \
  -o PreferredAuthentications=publickey \
  -o PasswordAuthentication=no \
  -o KbdInteractiveAuthentication=no \
  "$alias" true; do
  if (( SECONDS >= deadline )); then
    printf 'SSH did not become ready after restart within 180 seconds. Check the sandbox log.\n' >&2
    exit 1
  fi
  sleep 3
done
actual=$(ssh -o ConnectTimeout=3 \
  -o PreferredAuthentications=publickey \
  -o PasswordAuthentication=no \
  -o KbdInteractiveAuthentication=no \
  "$alias" "cat '$marker'")
if [[ $actual != "$token" ]]; then
  printf 'Persistence check failed. Expected %s, received %s.\n' "$token" "$actual" >&2
  exit 1
fi
ssh "$alias" "rm -f '$marker' && sync"
"$binary" stop "$name"
started=0

printf 'Manual VM test passed for %s. The private sandbox disk remains.\n' "$name"
