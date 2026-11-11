# Orca per-workspace environment image: the devcontainer image plus sshd for
# Orca's SSH relay. Built on top of the devcontainer image by
# scripts/orca-vm/docker-build.sh (BASE_IMAGE), so the devcontainer's own
# Dockerfile or image stays unchanged. Host keys are removed here and generated
# per container on first start, so no two workspaces share a private host key.
ARG BASE_IMAGE=mcr.microsoft.com/devcontainers/base:debian
FROM ${BASE_IMAGE}

USER root
RUN set -eu; \
    if ! command -v sshd >/dev/null 2>&1 && [ ! -x /usr/sbin/sshd ]; then \
        if command -v apt-get >/dev/null 2>&1; then \
            apt-get update && apt-get install -y --no-install-recommends openssh-server \
                && rm -rf /var/lib/apt/lists/*; \
        elif command -v apk >/dev/null 2>&1; then \
            apk add --no-cache openssh-server; \
        elif command -v dnf >/dev/null 2>&1; then \
            dnf install -y openssh-server && dnf clean all; \
        else \
            echo "no supported package manager to install openssh-server" >&2; exit 1; \
        fi; \
    fi; \
    rm -f /etc/ssh/ssh_host_*; \
    mkdir -p /run/sshd /etc/ssh/sshd_config.d
COPY sshd_config /etc/ssh/sshd_config.d/10-orca.conf
COPY entrypoint.sh /usr/local/bin/orca-docker-ssh-entrypoint
RUN chmod 755 /usr/local/bin/orca-docker-ssh-entrypoint \
    && { grep -q '^Include /etc/ssh/sshd_config.d/' /etc/ssh/sshd_config \
         || sed -i '1i Include /etc/ssh/sshd_config.d/*.conf' /etc/ssh/sshd_config; }
EXPOSE 22
ENTRYPOINT ["/usr/local/bin/orca-docker-ssh-entrypoint"]
