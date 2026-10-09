#!/bin/sh
set -eu

if [ "$#" -ne 1 ] || [ "$1" != build ]; then
    echo 'sandboxctl-image-builder: expected the build operation' >&2
    exit 2
fi

build_root=/var/tmp/sandboxctl-build
context=$build_root/context
rootfs=$build_root/rootfs
output=$build_root/output

if [ ! -d "$build_root" ] || [ -L "$build_root" ]; then
    echo 'sandboxctl-image-builder: build directory is missing or unsafe' >&2
    exit 1
fi
if [ ! -d "$context" ] || [ -L "$context" ]; then
    echo 'sandboxctl-image-builder: build context is missing or unsafe' >&2
    exit 1
fi
if [ ! -s "$context/Dockerfile" ] || [ ! -s "$context/provision.sh" ]; then
    echo 'sandboxctl-image-builder: build context is incomplete' >&2
    exit 1
fi

rm -rf -- "$rootfs" "$output"
mkdir -m 0700 -- "$output"
/usr/local/bin/buildctl \
    --addr unix:///run/buildkit/buildkitd.sock \
    build \
    --frontend dockerfile.v0 \
    --local "context=$context" \
    --local "dockerfile=$context" \
    --output "type=local,dest=$rootfs"

for path in \
    "$rootfs/etc/os-release" \
    "$rootfs/etc/passwd" \
    "$rootfs/etc/ssh/sshd_config" \
    "$rootfs/usr/sbin/sshd" \
    "$rootfs/usr/local/libexec/sandboxctl-bootstrap" \
    "$rootfs/etc/systemd/system/multi-user.target.wants/sandboxctl-bootstrap.service"; do
    if [ ! -e "$path" ]; then
        echo "sandboxctl-image-builder: required guest file is missing: $path" >&2
        exit 1
    fi
done
if [ ! -x "$rootfs/usr/sbin/sshd" ] || [ ! -x "$rootfs/usr/local/libexec/sandboxctl-bootstrap" ]; then
    echo 'sandboxctl-image-builder: SSH server or bootstrap service is not executable' >&2
    exit 1
fi

if ! grep -q '^dev:[^:]*:[1-9][0-9]*:' "$rootfs/etc/passwd"; then
    echo 'sandboxctl-image-builder: guest dev account is missing or invalid' >&2
    exit 1
fi
if find "$rootfs" -xdev -name envd -print -quit | grep -q .; then
    echo 'sandboxctl-image-builder: E2B envd files are not allowed' >&2
    exit 1
fi

mkdir -p "$rootfs/dev"
rm -f -- "$rootfs/dev/null"
mknod -m 0666 "$rootfs/dev/null" c 1 3
sshd_config=$(chroot "$rootfs" /usr/sbin/sshd -T)
check_sshd_option() {
    option=$1
    expected=$2
    actual=$(printf '%s\n' "$sshd_config" | awk -v key="$option" '$1 == key { print $2; exit }')
    if [ "$actual" != "$expected" ]; then
        echo "sandboxctl-image-builder: sshd $option is $actual, expected $expected" >&2
        exit 1
    fi
}
check_sshd_option permitrootlogin no
check_sshd_option permitemptypasswords no
check_sshd_option passwordauthentication no
check_sshd_option kbdinteractiveauthentication no
check_sshd_option pubkeyauthentication yes

rm -f -- "$rootfs/etc/machine-id" "$rootfs/etc/ssh/ssh_host_"*
rm -f -- "$rootfs/var/lib/sandboxctl/bootstrap-complete"
find "$rootfs/var/cache/apt/archives" -type f -delete 2>/dev/null || true
find "$rootfs/var/lib/apt/lists" -type f -delete 2>/dev/null || true
find "$rootfs/var/log" -type f -delete 2>/dev/null || true
find "$rootfs/tmp" -mindepth 1 -delete 2>/dev/null || true
find "$rootfs/var/tmp" -mindepth 1 -delete 2>/dev/null || true
rm -f -- "$rootfs/dev/null"
sync
/usr/bin/mksquashfs "$rootfs" "$output/root.squashfs" \
    -noappend \
    -comp xz \
    -b 1M \
    -no-xattrs \
    -no-progress \
    -repro-time 0
test -s "$output/root.squashfs"
printf '%s\n' "$output/root.squashfs"
