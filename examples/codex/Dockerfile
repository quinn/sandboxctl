FROM ubuntu:24.04@sha256:224a1869083a311ef3f13648a154ba79832fbef6364d31493642ca03082da254

ARG CODEX_VERSION=0.160.0

RUN apt-get -o APT::Update::Error-Mode=any update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        git \
        less \
        ripgrep \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

# Use the documented installer to install the complete native package.
RUN curl --fail --show-error --silent --location --retry 3 \
        https://chatgpt.com/codex/install.sh \
        --output /tmp/codex-install.sh \
    && CODEX_NON_INTERACTIVE=true \
        CODEX_HOME=/usr/local/lib/codex \
        CODEX_INSTALL_DIR=/usr/local/bin \
        sh /tmp/codex-install.sh --release "$CODEX_VERSION" \
    && test -f /usr/local/lib/codex/packages/standalone/current/codex-package.json \
    && test -x /usr/local/lib/codex/packages/standalone/current/bin/codex-code-mode-host \
    && test -x /usr/local/lib/codex/packages/standalone/current/codex-resources/bwrap \
    && codex --version \
    && rm -f /tmp/codex-install.sh

# Provisioning copies these global rules into /home/dev/.codex.
COPY --chmod=0600 AGENTS.md /etc/skel/.codex/AGENTS.md
RUN chmod 0700 /etc/skel/.codex

# Block the curated plugin cache when provisioning creates the dev account.
RUN mkdir -p /etc/skel/.codex/plugins/cache \
    && touch /etc/skel/.codex/plugins/cache/openai-curated-remote \
    && chmod 0000 /etc/skel/.codex/plugins/cache/openai-curated-remote

# sandboxctl adds systemd, SSH, and the dev account after this build.
# Sign in as dev after the sandbox starts.
