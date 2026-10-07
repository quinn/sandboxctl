#!/usr/bin/env bash
set -Eeuo pipefail

if [[ $# -ne 1 || ! $1 =~ ^[a-z][a-z0-9-]{0,31}$ ]]; then
  printf 'Usage: %s SANDBOX_NAME\n' "$0" >&2
  exit 2
fi

name=$1
repo_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
export PATH="$repo_dir/bin:$PATH"
binary="$repo_dir/bin/sandboxctl"
[[ -x $binary ]] || { printf 'Build sandboxctl first with `make build`.\n' >&2; exit 1; }

"$binary" init >/dev/null
if ! "$binary" list | awk -v name="$name" '$1 == name && $2 == "stopped" { found=1 } END { exit !found }'; then
  printf 'Sandbox %s must exist and be stopped. Check `sandboxctl list`.\n' "$name" >&2
  exit 1
fi

data_dir=${XDG_DATA_HOME:-${HOME:?HOME is not set}/.local/share}/sandboxctl
disk="$data_dir/instances/$name/root.ext4"
public_key="$data_dir/keys/client_ed25519.pub"
[[ -f $disk ]] || { printf 'Private root drive not found: %s\n' "$disk" >&2; exit 1; }
[[ -r $public_key ]] || { printf 'SSH public key not found: %s\n' "$public_key" >&2; exit 1; }
for program in sudo mount umount chroot openssl; do
  command -v "$program" >/dev/null || { printf 'Required command not found: %s\n' "$program" >&2; exit 1; }
done

mount_dir=$(mktemp -d /tmp/sandboxctl-guest.XXXXXX)
image_mounted=0
dev_mounted=0
cleanup() {
  status=$?
  trap - EXIT
  if (( dev_mounted )); then sudo umount "$mount_dir/dev" || status=1; fi
  if (( image_mounted )); then sudo umount "$mount_dir" || status=1; fi
  rmdir "$mount_dir" 2>/dev/null || true
  exit "$status"
}
trap cleanup EXIT

sudo mount -o loop "$disk" "$mount_dir"
image_mounted=1
sudo mount --bind /dev "$mount_dir/dev"
dev_mounted=1

account=$(sudo awk -F: '$1 == "ubuntu" { print $3 ":" $4 ":" $6; exit }' "$mount_dir/etc/passwd")
IFS=: read -r uid gid home <<<"$account"
if [[ ! $uid =~ ^[0-9]+$ || ! $gid =~ ^[0-9]+$ || $home != /home/ubuntu || ! -d $mount_dir$home ]]; then
  printf 'The guest image does not have the expected ubuntu account.\n' >&2
  exit 1
fi

ssh_dir="$mount_dir$home/.ssh"
auth_file="$ssh_dir/authorized_keys"
sudo chown "$uid:$gid" "$mount_dir$home"
sudo install -d -m 0700 "$ssh_dir"
sudo touch "$auth_file"
sudo chmod 0600 "$auth_file"
sudo chown "$uid:$gid" "$ssh_dir" "$auth_file"
key=$(<"$public_key")
if ! sudo grep -Fqx -- "$key" "$auth_file"; then
  printf '%s\n' "$key" | sudo tee -a "$auth_file" >/dev/null
fi
sudo chown "$uid:$gid" "$auth_file"

shadow_status=$(sudo awk -F: '$1 == "ubuntu" { print $2; exit }' "$mount_dir/etc/shadow")
if [[ -z $shadow_status || $shadow_status == '!'* || $shadow_status == '*'* ]]; then
  random_password=$(openssl rand -hex 32)
  password_hash=$(openssl passwd -6 "$random_password")
  unset random_password
  sudo chroot "$mount_dir" /usr/sbin/usermod --password "$password_hash" ubuntu
  unset password_hash
fi

sshd_dropin="$mount_dir/etc/ssh/sshd_config.d/99-sandboxctl-manual.conf"
sudo install -d -m 0755 "$(dirname -- "$sshd_dropin")"
printf '%s\n' \
  'PubkeyAuthentication yes' \
  'PasswordAuthentication no' \
  'KbdInteractiveAuthentication no' \
  | sudo tee "$sshd_dropin" >/dev/null
sudo chmod 0644 "$sshd_dropin"
sudo install -d -m 0755 "$mount_dir/run/sshd"
sudo chroot "$mount_dir" /usr/sbin/sshd -t

sudo umount "$mount_dir/dev"
dev_mounted=0
sudo umount "$mount_dir"
image_mounted=0
printf 'Prepared stopped sandbox %s for public-key SSH login as ubuntu.\n' "$name"
