#!/bin/sh
set -eu

if [ "$#" -ne 1 ] || { [ "$1" != build-base ] && [ "$1" != build-dockerfile ]; }; then
    echo 'sandboxctl-image-builder: expected the build operation' >&2
    exit 2
fi
build_mode=$1

build_root=/var/tmp/sandboxctl-build
context=$build_root/context
user_context=$context/user
user_dockerfile_context=$build_root/user-dockerfile
user_dockerfile_input=$build_root/input/Dockerfile
user_dockerfile=$user_dockerfile_context/Dockerfile
context_archive=$build_root/context.tar
settings_path=$build_root/settings.json
provision_context=$context/provision
user_oci_tar=$build_root/user-image.oci.tar
user_oci=$build_root/user-image.oci
rootfs=$build_root/rootfs
output=$build_root/output
buildkit_root=$build_root/buildkit
buildkit_socket_dir=/run/sandboxctl-buildkit
buildkit_socket=$buildkit_socket_dir/buildkitd.sock
buildkit_log=$build_root/buildkitd.log
build_log=$build_root/build.log
squashfs_log=$build_root/mksquashfs.log
buildkit_version=v0.34.0

if [ ! -d "$build_root" ] || [ -L "$build_root" ]; then
    echo 'sandboxctl-image-builder: build directory is missing or unsafe' >&2
    exit 1
fi
if [ ! -d "$context" ] || [ -L "$context" ]; then
    echo 'sandboxctl-image-builder: build context is missing or unsafe' >&2
    exit 1
fi
for path in "$context/provision.Dockerfile" "$context/provision.sh" "$context/source-policy.json"; do
    if [ ! -s "$path" ] || [ -L "$path" ]; then
        echo "sandboxctl-image-builder: required build input is missing or unsafe: $path" >&2
        exit 1
    fi
done
if [ "$build_mode" = build-base ] && { [ ! -s "$context/Dockerfile" ] || [ -L "$context/Dockerfile" ]; }; then
    echo 'sandboxctl-image-builder: generated base Dockerfile is missing or unsafe' >&2
    exit 1
fi
if [ "$build_mode" = build-dockerfile ]; then
    for path in "$context_archive" "$user_dockerfile_input" "$settings_path"; do
        if [ ! -s "$path" ] || [ -L "$path" ]; then
            echo "sandboxctl-image-builder: required user build input is missing or unsafe: $path" >&2
            exit 1
        fi
    done
fi
if [ ! -x /usr/local/bin/buildctl ] || [ ! -x /usr/local/bin/buildkitd ]; then
    echo 'sandboxctl-image-builder: pinned BuildKit tools are missing' >&2
    exit 1
fi
if [ ! -x /usr/bin/mksquashfs ] || [ ! -x /usr/bin/jq ] || [ ! -x /usr/bin/tar ] || [ ! -x /usr/bin/sha256sum ]; then
    echo 'sandboxctl-image-builder: required image build tools are missing' >&2
    exit 1
fi
actual_buildctl_version=$(/usr/local/bin/buildctl --version | awk '{print $3}')
actual_buildkitd_version=$(/usr/local/bin/buildkitd --version | awk '{print $3}')
if [ "$actual_buildctl_version" != "$buildkit_version" ] || [ "$actual_buildkitd_version" != "$buildkit_version" ]; then
    printf 'sandboxctl-image-builder: BuildKit tools must both be %s (buildctl=%s, buildkitd=%s)\n' \
        "$buildkit_version" "$actual_buildctl_version" "$actual_buildkitd_version" >&2
    exit 1
fi

rm -rf -- "$user_context" "$user_dockerfile_context" "$provision_context" "$user_oci_tar" "$user_oci" "$rootfs" "$output" "$buildkit_root" "$buildkit_socket_dir"
mkdir -m 0700 -- "$output" "$user_context" "$provision_context"
if [ "$build_mode" = build-base ]; then
    mv -- "$context/Dockerfile" "$user_context/Dockerfile"
    user_dockerfile_dir=$user_context
else
    mkdir -m 0700 -- "$user_dockerfile_context"
    user_dockerfile_dir=$user_dockerfile_context
    if ! /usr/bin/jq -e '
        type == "object" and
        (.context_digest | type == "string" and test("^sha256:[0-9a-f]{64}$")) and
        (.dockerfile_hash | type == "string" and test("^sha256:[0-9a-f]{64}$")) and
        (.target == null or (.target | type == "string" and test("^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$"))) and
        (.build_args | type == "array" and length <= 128) and
        ([.build_args[].name] | length == (unique | length)) and
        all(.build_args[]; (.name | type == "string" and test("^[A-Za-z_][A-Za-z0-9_]*$")) and (.value | type == "string"))
    ' "$settings_path" >/dev/null; then
        echo 'sandboxctl-image-builder: build settings are invalid' >&2
        exit 1
    fi
    expected_dockerfile_hash=$(/usr/bin/jq -er '.dockerfile_hash' "$settings_path")
    actual_dockerfile_hash=$(sha256sum "$user_dockerfile_input" | awk '{print "sha256:" $1}')
    if [ "$expected_dockerfile_hash" != "$actual_dockerfile_hash" ]; then
        echo 'sandboxctl-image-builder: Dockerfile digest does not match build settings' >&2
        exit 1
    fi
    expected_context_digest=$(/usr/bin/jq -er '.context_digest | select(test("^sha256:[0-9a-f]{64}$"))' "$settings_path")
    actual_context_digest=$(sha256sum "$context_archive" | awk '{print "sha256:" $1}')
    if [ "$expected_context_digest" != "$actual_context_digest" ]; then
        echo 'sandboxctl-image-builder: context archive digest does not match build settings' >&2
        exit 1
    fi
    /usr/bin/tar -tf "$context_archive" >"$build_root/context.list"
    if ! awk '
        BEGIN { bad = 0 }
        {
            name = $0
            sub(/\/$/, "", name)
            if (name == "" || name == "." || name ~ /^\// || name ~ /(^|\/)\.\.(\/|$)/ || name ~ /[\r\t]/) bad = 1
            if (seen[name]++) bad = 1
        }
        END { exit bad }
    ' "$build_root/context.list"; then
        echo 'sandboxctl-image-builder: context archive paths or entries are invalid' >&2
        exit 1
    fi
    context_entry_count=$(wc -l <"$build_root/context.list")
    if [ "$context_entry_count" -gt 50000 ]; then
        echo 'sandboxctl-image-builder: context entry count exceeds the size limit' >&2
        exit 1
    fi
    /usr/bin/tar --extract --no-same-owner --no-same-permissions --no-overwrite-dir --directory "$user_context" --file "$context_archive"
    /bin/cp -- "$user_dockerfile_input" "$user_dockerfile"
    /bin/chmod 0600 "$user_dockerfile"
    if grep -Eiq '^[[:space:]]*#[[:space:]]*syntax[[:space:]]*=' "$user_dockerfile"; then
        echo 'sandboxctl-image-builder: custom Dockerfile frontends are not supported' >&2
        exit 1
    fi
fi
mv -- "$context/provision.Dockerfile" "$provision_context/Dockerfile"
mv -- "$context/provision.sh" "$provision_context/provision.sh"
mv -- "$context/source-policy.json" "$provision_context/source-policy.json"
mkdir -m 0700 -- "$buildkit_root" "$buildkit_socket_dir"
/usr/local/bin/buildkitd \
    --addr "unix://$buildkit_socket" \
    --root "$buildkit_root" \
    >"$buildkit_log" 2>&1 &
buildkitd_pid=$!
stop_buildkitd() {
    if kill -0 "$buildkitd_pid" 2>/dev/null; then
        kill -TERM "$buildkitd_pid" 2>/dev/null || true
    fi
    wait "$buildkitd_pid" 2>/dev/null || true
}
trap stop_buildkitd EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

attempt=0
while [ "$attempt" -lt 30 ]; do
    if /usr/local/bin/buildctl --addr "unix://$buildkit_socket" debug workers >/dev/null 2>&1; then
        break
    fi
    if ! kill -0 "$buildkitd_pid" 2>/dev/null; then
        echo 'sandboxctl-image-builder: buildkitd exited during startup' >&2
        tail -n 100 "$buildkit_log" >&2 || true
        exit 1
    fi
    attempt=$((attempt + 1))
    sleep 1
done
if [ "$attempt" -eq 30 ]; then
    echo 'sandboxctl-image-builder: buildkitd did not become ready' >&2
    tail -n 100 "$buildkit_log" >&2 || true
    exit 1
fi

echo 'sandboxctl-image-builder: building user filesystem as an OCI layout'
set -- \
    --addr "unix://$buildkit_socket" \
    build \
    --frontend dockerfile.v0 \
    --opt platform=linux/amd64 \
    --local "context=$user_context" \
    --local "dockerfile=$user_dockerfile_dir" \
    --source-policy-file "$provision_context/source-policy.json" \
    --output "type=oci,dest=$user_oci_tar"
if [ "$build_mode" = build-dockerfile ]; then
    selected_target=$(/usr/bin/jq -r '.target // empty' "$settings_path")
    if [ -n "$selected_target" ]; then
        set -- "$@" --opt "target=$selected_target"
    fi
    build_arg_data=$(/usr/bin/jq -r '.build_args[] | @base64' "$settings_path")
    while IFS= read -r encoded_arg; do
        [ -n "$encoded_arg" ] || continue
        arg_name=$(printf '%s' "$encoded_arg" | base64 --decode | /usr/bin/jq -er '.name | select(test("^[A-Za-z_][A-Za-z0-9_]*$"))')
        arg_value=$(printf '%s' "$encoded_arg" | base64 --decode | /usr/bin/jq -er '.value | strings + "\u0001"')
        arg_value=${arg_value%?}
        set -- "$@" --opt "build-arg:$arg_name=$arg_value"
    done <<EOF
$build_arg_data
EOF
fi
if ! /usr/local/bin/buildctl "$@" >"$build_log" 2>&1; then
    echo 'sandboxctl-image-builder: BuildKit user image build failed; last 80 log lines follow' >&2
    tail -n 80 "$build_log" >&2 || true
    exit 1
fi
mkdir -m 0700 -- "$user_oci"
if ! tar -xf "$user_oci_tar" -C "$user_oci"; then
    echo 'sandboxctl-image-builder: BuildKit OCI layout extraction failed' >&2
    exit 1
fi
user_digest=$(/usr/bin/jq -er '.manifests | if length == 1 then .[0].digest else error("expected one image manifest") end | select(test("^sha256:[0-9a-f]{64}$"))' "$user_oci/index.json")
echo 'sandboxctl-image-builder: user OCI layout is ready'

echo 'sandboxctl-image-builder: applying sandboxctl guest provisioning'
if ! /usr/local/bin/buildctl \
    --addr "unix://$buildkit_socket" \
    build \
    --frontend dockerfile.v0 \
    --opt platform=linux/amd64 \
    --opt "context:sandboxctl-user-image=oci-layout://sandboxctl-user-image@$user_digest" \
    --oci-layout "sandboxctl-user-image=$user_oci" \
    --local "context=$provision_context" \
    --local "dockerfile=$provision_context" \
    --source-policy-file "$provision_context/source-policy.json" \
    --output "type=local,dest=$rootfs" \
    >"$build_root/provision.log" 2>&1; then
    echo 'sandboxctl-image-builder: guest provisioning build failed; last 80 log lines follow' >&2
    tail -n 80 "$build_root/provision.log" >&2 || true
    exit 1
fi
echo 'sandboxctl-image-builder: sandboxctl guest provisioning completed'
stop_buildkitd
trap - EXIT

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
if ! /usr/bin/mksquashfs "$rootfs" "$output/root.squashfs" \
    -noappend \
    -comp xz \
    -b 1M \
    -no-xattrs \
    -no-progress \
    -all-time 0 \
    >"$squashfs_log" 2>&1; then
    echo 'sandboxctl-image-builder: mksquashfs failed; first 60 log lines follow' >&2
    sed -n '1,60p' "$squashfs_log" >&2 || true
    exit 1
fi
echo 'sandboxctl-image-builder: SquashFS creation completed'
test -s "$output/root.squashfs"
chmod 0644 "$output/root.squashfs"
chmod 0711 "$output"
printf '%s\n' "$output/root.squashfs"
