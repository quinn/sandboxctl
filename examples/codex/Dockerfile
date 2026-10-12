FROM ubuntu:24.04@sha256:224a1869083a311ef3f13648a154ba79832fbef6364d31493642ca03082da254

ARG CODEX_VERSION=0.160.0
ARG CODEX_SHA256=306865417d4ee7a927785852910a527f41e1e159add390ac5ae3accb67d44a13

RUN apt-get -o APT::Update::Error-Mode=any update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        git \
        less \
        ripgrep \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

# Install the standalone Linux x86-64 binary.
RUN curl --fail --show-error --silent --location --retry 3 \
        "https://github.com/openai/codex/releases/download/rust-v${CODEX_VERSION}/codex-x86_64-unknown-linux-musl.tar.gz" \
        --output /tmp/codex.tar.gz \
    && printf '%s  %s\n' "$CODEX_SHA256" /tmp/codex.tar.gz | sha256sum --check --strict \
    && mkdir /tmp/codex-extract \
    && tar -xzf /tmp/codex.tar.gz -C /tmp/codex-extract \
    && install -m 0755 /tmp/codex-extract/codex-x86_64-unknown-linux-musl /usr/local/bin/codex \
    && rm -rf /tmp/codex.tar.gz /tmp/codex-extract

# Provisioning copies these global rules into /home/dev/.codex.
COPY --chmod=0600 AGENTS.md /etc/skel/.codex/AGENTS.md
RUN chmod 0700 /etc/skel/.codex

# sandboxctl adds systemd, SSH, and the dev account after this build.
# Sign in as dev after the sandbox starts.
