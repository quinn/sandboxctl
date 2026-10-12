#!/bin/sh
# Copyright 2023 FoundryLabs, Inc.
# Derived from E2B runtime provisioning and substantially modified for sandboxctl.
# See third_party/licenses/e2b-apache-2.0.txt.
set -eu

EXPECTED_ID={{.ID}}
EXPECTED_VERSION="{{.GuestVersion}}"
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
if [ "${VERSION_ID:-}" != "$EXPECTED_VERSION" ]; then
    echo "sandboxctl: expected guest version $EXPECTED_VERSION, found ${VERSION_ID:-unknown}" >&2
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

install -d -m 0755 /usr/local/share/sandboxctl

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

rm -f /etc/machine-id
