FROM ubuntu:24.04@sha256:224a1869083a311ef3f13648a154ba79832fbef6364d31493642ca03082da254

ARG APT_SNAPSHOT=20261009T000000Z
ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates=20240203

RUN apt-get -o APT::Update::Error-Mode=any update --snapshot "$APT_SNAPSHOT" \
    && apt-get install --snapshot "$APT_SNAPSHOT" -y --no-install-recommends \
        curl \
        git \
        iproute2 \
        iputils-ping \
        jq \
        openssh-server \
        openssl \
        squashfs-tools \
        sudo \
        systemd \
        systemd-sysv \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/* /var/cache/apt/archives/* /var/log/* /tmp/* /var/tmp/* \
    && rm -rf /home/ubuntu \
    && rm -f /etc/machine-id /var/lib/dbus/machine-id /etc/ssh/ssh_host_* \
    && mkdir -p /var/lib/sandboxctl /run/sshd

COPY --chown=0:0 buildkit/bin/buildctl /usr/local/bin/buildctl
COPY --chown=0:0 buildkit/bin/buildkitd /usr/local/bin/buildkitd
COPY --chown=0:0 buildkit/bin/buildkit-runc /usr/local/bin/buildkit-runc

RUN chmod 0755 /usr/local/bin/buildctl /usr/local/bin/buildkitd /usr/local/bin/buildkit-runc \
    && systemctl --root=/ set-default multi-user.target \
    && systemctl --root=/ enable ssh.socket \
    && systemctl --root=/ mask systemd-firstboot.service
