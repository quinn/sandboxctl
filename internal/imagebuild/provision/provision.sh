#!/bin/sh
# Copyright 2023 FoundryLabs, Inc.
# Derived from E2B runtime provisioning and substantially modified for sandboxctl.
# See third_party/licenses/e2b-apache-2.0.txt.
set -eu

EXPECTED_ID={{.ID}}
PACKAGES="{{.Packages}}"
INIT_BINARY={{.InitBinary}}
SSH_SERVICE={{.SSHService}}
ADMIN_GROUP={{.AdminGroup}}
CA_BUNDLE={{.CABundle}}

if [ ! -r /etc/os-release ]; then
    echo "sandboxctl: /etc/os-release is missing" >&2
    exit 1
fi
. /etc/os-release
if [ "${ID:-}" != "$EXPECTED_ID" ]; then
    echo "sandboxctl: expected $EXPECTED_ID, found ${ID:-unknown}" >&2
    exit 1
fi

package_installed() {
    {{.PackageQuery}}
}

install_packages() {
    {{.PackageSetup}}
}

missing=""
for package in $PACKAGES; do
    if ! package_installed "$package"; then
        missing="$missing $package"
    fi
done
if [ -n "$missing" ]; then
    # The profile contains fixed package names. Word splitting passes one argv
    # item per package to apt-get.
    # shellcheck disable=SC2086
    install_packages $missing
fi

if [ ! -s "$CA_BUNDLE" ]; then
    {{.CARefresh}}
fi
if [ ! -s "$CA_BUNDLE" ]; then
    echo "sandboxctl: CA bundle is missing at $CA_BUNDLE" >&2
    exit 1
fi

if ! getent passwd dev >/dev/null 2>&1; then
    useradd --create-home --home-dir /home/dev --shell /bin/bash --groups "$ADMIN_GROUP" dev
fi
dev_uid=$(id -u dev)
dev_gid=$(id -g dev)
dev_home=$(getent passwd dev | cut -d: -f6)
if [ "$dev_uid" -eq 0 ] || [ "$dev_home" != /home/dev ]; then
    echo "sandboxctl: existing dev account has an unsafe UID or home directory" >&2
    exit 1
fi
usermod --append --groups "$ADMIN_GROUP" dev
install -d -o "$dev_uid" -g "$dev_gid" -m 0750 /home/dev
install -d -o "$dev_uid" -g "$dev_gid" -m 0700 /home/dev/.ssh

# Guest sudo policy: dev may use sudo without a password inside this isolated
# development VM. This policy does not grant privileges on the host.
printf 'dev ALL=(ALL) NOPASSWD: ALL\n' >/etc/sudoers.d/90-sandboxctl-dev
chmod 0440 /etc/sudoers.d/90-sandboxctl-dev
visudo -cf /etc/sudoers.d/90-sandboxctl-dev

# A random hash keeps PAM account checks usable for public-key login. SSH
# password and keyboard-interactive authentication remain disabled below.
random_password=$(openssl rand -hex 32)
password_hash=$(openssl passwd -6 "$random_password")
unset random_password
usermod --password "$password_hash" dev
unset password_hash

install -d -m 0755 /etc/ssh
if grep -qE '^[[:space:]]*Include[[:space:]]+/etc/ssh/sshd_config\.d/' /etc/ssh/sshd_config; then
    ssh_config=/etc/ssh/sshd_config.d/00-sandboxctl.conf
    install -d -m 0755 /etc/ssh/sshd_config.d
else
    ssh_config=/etc/ssh/sshd_config
fi
cat >"$ssh_config" <<'EOF'
PermitRootLogin no
PermitEmptyPasswords no
PasswordAuthentication no
KbdInteractiveAuthentication no
PubkeyAuthentication yes
EOF
chmod 0644 "$ssh_config"
mkdir -p /run/sshd
sshd -t
check_sshd_option() {
    option=$1
    expected=$2
    actual=$(sshd -T | awk -v key="$option" '$1 == key { print $2; exit }')
    if [ "$actual" != "$expected" ]; then
        echo "sandboxctl: sshd effective $option is $actual, expected $expected" >&2
        exit 1
    fi
}
check_sshd_option permitrootlogin no
check_sshd_option permitemptypasswords no
check_sshd_option passwordauthentication no
check_sshd_option kbdinteractiveauthentication no
check_sshd_option pubkeyauthentication yes

install -d -m 0755 /usr/local/libexec /usr/local/share/sandboxctl /etc/systemd/system
cat >/usr/local/libexec/sandboxctl-bootstrap <<'EOF'
#!/bin/sh
set -eu

metadata_address=169.254.169.254
token=
attempt=0
while [ "$attempt" -lt 30 ]; do
    if token=$(curl --noproxy '*' --fail --silent --show-error --connect-timeout 2 --max-time 4 \
        -X PUT "http://${metadata_address}/latest/api/token" \
        -H 'X-metadata-token-ttl-seconds: 60' 2>/dev/null); then
        break
    fi
    attempt=$((attempt + 1))
    sleep 1
done
if [ -z "$token" ]; then
    echo 'sandboxctl-bootstrap: could not obtain an MMDS token' >&2
    exit 1
fi

metadata=$(curl --noproxy '*' --fail --silent --show-error --connect-timeout 2 --max-time 4 \
    -H "X-metadata-token: $token" \
    -H 'Accept: application/json' \
    "http://${metadata_address}/latest/meta-data/")
unset token

schema_version=$(printf '%s' "$metadata" | jq -er '.sandboxctl.schema_version | select(type == "number")')
if [ "$schema_version" != 1 ]; then
    echo "sandboxctl-bootstrap: unsupported metadata schema $schema_version" >&2
    exit 1
fi
hostname=$(printf '%s' "$metadata" | jq -er '.sandboxctl.hostname | select(type == "string")')
ssh_user=$(printf '%s' "$metadata" | jq -er '.sandboxctl.ssh_user | select(type == "string")')
authorized_key=$(printf '%s' "$metadata" | jq -er '.sandboxctl.ssh_authorized_key | select(type == "string")')
address=$(printf '%s' "$metadata" | jq -er '.sandboxctl.network.address | select(type == "string")')
gateway=$(printf '%s' "$metadata" | jq -er '.sandboxctl.network.gateway | select(type == "string")')
interface=$(printf '%s' "$metadata" | jq -er '.sandboxctl.network.interface | select(type == "string")')

case "$hostname" in
    ''|*[!a-zA-Z0-9.-]*|-*|.*|*-|*.) echo 'sandboxctl-bootstrap: invalid hostname' >&2; exit 1 ;;
esac
if [ "${#hostname}" -gt 63 ] || [ "$ssh_user" != dev ]; then
    echo 'sandboxctl-bootstrap: invalid hostname or SSH user' >&2
    exit 1
fi
case "$authorized_key" in
    'ssh-ed25519 '*) ;;
    *) echo 'sandboxctl-bootstrap: invalid SSH public key type' >&2; exit 1 ;;
esac
key_lines=$(printf '%s\n' "$authorized_key" | wc -l)
if [ "$key_lines" -ne 1 ]; then
    echo 'sandboxctl-bootstrap: SSH key must be one line' >&2
    exit 1
fi
case "$address" in
    *.*.*.*/30) ;;
    *) echo 'sandboxctl-bootstrap: guest address must be IPv4 with a /30 prefix' >&2; exit 1 ;;
esac
case "$interface" in
    ''|*[!a-zA-Z0-9_.-]*) echo 'sandboxctl-bootstrap: invalid interface name' >&2; exit 1 ;;
esac
if [ "${#interface}" -gt 15 ]; then
    echo 'sandboxctl-bootstrap: interface name is too long' >&2
    exit 1
fi

actual_address=$(ip -o -4 address show dev "$interface" | awk '{print $4}')
if [ "$actual_address" != "$address" ]; then
    echo 'sandboxctl-bootstrap: MMDS address differs from the early guest address' >&2
    exit 1
fi
actual_gateway=$(ip route show default dev "$interface" | awk '$1 == "default" { for (i = 1; i <= NF; i++) if ($i == "via") print $(i + 1); exit }')
if [ "$actual_gateway" != "$gateway" ]; then
    echo 'sandboxctl-bootstrap: MMDS gateway differs from the early guest route' >&2
    exit 1
fi

printf '%s\n' "$hostname" >/etc/hostname
hostnamectl set-hostname "$hostname"
ssh_dir=/home/dev/.ssh
install -d -o dev -g dev -m 0700 "$ssh_dir"
key_tmp=$(mktemp "$ssh_dir/.authorized_keys.XXXXXX")
trap 'rm -f "$key_tmp"' EXIT HUP INT TERM
printf '%s\n' "$authorized_key" >"$key_tmp"
chown dev:dev "$key_tmp"
chmod 0600 "$key_tmp"
mv -f "$key_tmp" "$ssh_dir/authorized_keys"
trap - EXIT HUP INT TERM

ssh-keygen -A
install -d -m 0755 /var/lib/sandboxctl
marker_tmp=$(mktemp /var/lib/sandboxctl/.bootstrap-complete.XXXXXX)
printf 'schema_version=%s\nhostname=%s\n' "$schema_version" "$hostname" >"$marker_tmp"
chmod 0644 "$marker_tmp"
mv -f "$marker_tmp" /var/lib/sandboxctl/bootstrap-complete
EOF
chmod 0755 /usr/local/libexec/sandboxctl-bootstrap

cat >/etc/systemd/system/sandboxctl-bootstrap.service <<'EOF'
[Unit]
Description=Configure this sandbox from Firecracker metadata
DefaultDependencies=no
After=local-fs.target
Before=ssh.socket ssh.service network-online.target

[Service]
Type=oneshot
ExecStart=/usr/local/libexec/sandboxctl-bootstrap
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
EOF
chmod 0644 /etc/systemd/system/sandboxctl-bootstrap.service

{{range .BootSetup}}
{{.}}
{{end}}

mkdir -p /usr/sbin
ln -sf "$INIT_BINARY" /usr/sbin/init
if [ ! -L /sbin ]; then
    ln -sf "$INIT_BINARY" /sbin/init
fi

mkdir -p /usr/local/share/sandboxctl
umask 077
cat >/usr/local/share/sandboxctl/image-build.env <<EOF
SANDBOXCTL_DISTRO_ID='$EXPECTED_ID'
SANDBOXCTL_INIT_BIN='$INIT_BINARY'
SANDBOXCTL_ADMIN_GROUP='$ADMIN_GROUP'
EOF
chmod 0600 /usr/local/share/sandboxctl/image-build.env

rm -f /etc/machine-id /etc/ssh/ssh_host_*
