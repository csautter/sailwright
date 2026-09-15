FROM mcr.microsoft.com/devcontainers/go:2.3-1.27-trixie

ENV DEBIAN_FRONTEND=noninteractive
ENV NODE_VERSION=24.18.0
# renovate: datasource=npm depName=renovate versioning=npm
ENV RENOVATE_VERSION="43.272.2"
# renovate: datasource=custom.powershell-linux-x64 depName=PowerShell/PowerShell versioning=semver
ENV POWERSHELL_VERSION="7.6.3"
ENV POWERSHELL_SHA256_X64="856d0765d2332377f9d7a4aea76efdfde4de51446e7738dde2dfda41dba9e2a7"
# renovate: datasource=custom.powershell-linux-arm64 depName=PowerShell/PowerShell version=7.6.3 versioning=semver
ENV POWERSHELL_SHA256_ARM64="7a14a385eca7dc5bedc1c8aa3d8b765f449ada30aabe5785a9fd331266eb062d"
# renovate: datasource=custom.terragrunt-linux-amd64 depName=gruntwork-io/terragrunt versioning=semver
ENV TERRAGRUNT_VERSION="1.1.1"
ENV TERRAGRUNT_SHA256_AMD64="98b0400ba1ae27120adeeb98ba97e75eba0bab0b4cbd7b8bb148d4ed29e2f823"
# renovate: datasource=custom.terragrunt-linux-arm64 depName=gruntwork-io/terragrunt version=1.1.1 versioning=semver
ENV TERRAGRUNT_SHA256_ARM64="9662356c40130bfbcd20e88e556dfa33e487c31af153803aca85c8a299d48ab6"
# renovate: datasource=custom.tflint-linux-amd64 depName=terraform-linters/tflint versioning=semver
ENV TFLINT_VERSION="0.63.1"
ENV TFLINT_SHA256_AMD64="8441a7d97df20431f19c9b9d27ff4c63e308c964e86660bc7cc0cf7bbe0725e8"
# renovate: datasource=custom.tflint-linux-arm64 depName=terraform-linters/tflint version=0.63.1 versioning=semver
ENV TFLINT_SHA256_ARM64="6d858ca7f11858c3fe3c5e29cc746823abccb55e2d2e2da130fa7ad7ea4eecb8"

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ansible \
        apt-transport-https \
        ca-certificates \
        curl \
        gpg \
        ruby-full \
        shellcheck \
        unzip \
        xz-utils \
        yq \
    && rm -rf /var/lib/apt/lists/*

RUN . /etc/os-release \
    && case "${ID}:${VERSION_CODENAME}" in \
        debian:trixie) azure_cli_suite="bookworm" ;; \
        *) azure_cli_suite="${VERSION_CODENAME}" ;; \
    esac \
    && mkdir -p /etc/apt/keyrings \
    && curl -sLS https://packages.microsoft.com/keys/microsoft.asc | \
        gpg --dearmor --yes -o /etc/apt/keyrings/microsoft.gpg \
    && chmod go+r /etc/apt/keyrings/microsoft.gpg \
    && printf 'Types: deb\nURIs: https://packages.microsoft.com/repos/azure-cli/\nSuites: %s\nComponents: main\nArchitectures: %s\nSigned-by: /etc/apt/keyrings/microsoft.gpg\n' \
        "${azure_cli_suite}" "$(dpkg --print-architecture)" \
        > /etc/apt/sources.list.d/azure-cli.sources \
    && apt-get update \
    && apt-get install -y --no-install-recommends azure-cli \
    && rm -rf /var/lib/apt/lists/* \
    && az version

RUN arch="$(dpkg --print-architecture)" \
    && case "${arch}" in \
        amd64) powershell_arch="x64"; powershell_sha256="${POWERSHELL_SHA256_X64}" ;; \
        arm64) powershell_arch="arm64"; powershell_sha256="${POWERSHELL_SHA256_ARM64}" ;; \
        *) echo "Unsupported PowerShell architecture: ${arch}" >&2; exit 1 ;; \
    esac \
    && mkdir -p /opt/microsoft/powershell/7 \
    && curl -fsSL "https://github.com/PowerShell/PowerShell/releases/download/v${POWERSHELL_VERSION}/powershell-${POWERSHELL_VERSION}-linux-${powershell_arch}.tar.gz" -o /tmp/powershell.tar.gz \
    && printf '%s  %s\n' "${powershell_sha256}" /tmp/powershell.tar.gz | sha256sum -c - \
    && tar -xzf /tmp/powershell.tar.gz -C /opt/microsoft/powershell/7 \
    && chmod +x /opt/microsoft/powershell/7/pwsh \
    && ln -sf /opt/microsoft/powershell/7/pwsh /usr/local/bin/pwsh \
    && rm -f /tmp/powershell.tar.gz \
    && pwsh --version

RUN arch="$(dpkg --print-architecture)" \
    && case "${arch}" in \
        amd64) node_arch="x64" ;; \
        arm64) node_arch="arm64" ;; \
        *) echo "Unsupported Node.js architecture: ${arch}" >&2; exit 1 ;; \
    esac \
    && curl -fsSL "https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-${node_arch}.tar.xz" -o /tmp/node.tar.xz \
    && rm -rf /usr/local/lib/node_modules /usr/local/include/node /usr/local/bin/node /usr/local/bin/npm /usr/local/bin/npx \
    && tar -xJf /tmp/node.tar.xz -C /usr/local --strip-components=1 \
    && rm -f /tmp/node.tar.xz \
    && node --version \
    && npm --version \
    && npx --version

RUN npm install --global "renovate@${RENOVATE_VERSION}" \
    && renovate-config-validator --version \
    && npm cache clean --force

RUN arch="$(dpkg --print-architecture)" \
    && case "${arch}" in \
        amd64) terragrunt_arch="amd64"; terragrunt_sha256="${TERRAGRUNT_SHA256_AMD64}" ;; \
        arm64) terragrunt_arch="arm64"; terragrunt_sha256="${TERRAGRUNT_SHA256_ARM64}" ;; \
        *) echo "Unsupported Terragrunt architecture: ${arch}" >&2; exit 1 ;; \
    esac \
    && curl -fsSL "https://github.com/gruntwork-io/terragrunt/releases/download/v${TERRAGRUNT_VERSION}/terragrunt_linux_${terragrunt_arch}" -o /usr/local/bin/terragrunt \
    && printf '%s  %s\n' "${terragrunt_sha256}" /usr/local/bin/terragrunt | sha256sum -c - \
    && chmod +x /usr/local/bin/terragrunt \
    && terragrunt --version

RUN arch="$(dpkg --print-architecture)" \
    && case "${arch}" in \
        amd64) tflint_arch="amd64"; tflint_sha256="${TFLINT_SHA256_AMD64}" ;; \
        arm64) tflint_arch="arm64"; tflint_sha256="${TFLINT_SHA256_ARM64}" ;; \
        *) echo "Unsupported TFLint architecture: ${arch}" >&2; exit 1 ;; \
    esac \
    && curl -fsSL "https://github.com/terraform-linters/tflint/releases/download/v${TFLINT_VERSION}/tflint_linux_${tflint_arch}.zip" -o /tmp/tflint.zip \
    && printf '%s  %s\n' "${tflint_sha256}" /tmp/tflint.zip | sha256sum -c - \
    && unzip -o /tmp/tflint.zip -d /usr/local/bin \
    && rm -f /tmp/tflint.zip \
    && chmod +x /usr/local/bin/tflint \
    && tflint --version

RUN . /etc/os-release \
    && curl -fsSL https://apt.releases.hashicorp.com/gpg | \
        gpg --dearmor --yes -o /usr/share/keyrings/hashicorp-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/hashicorp-archive-keyring.gpg] https://apt.releases.hashicorp.com ${VERSION_CODENAME} main" \
        > /etc/apt/sources.list.d/hashicorp.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends \
        packer \
        terraform \
    && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL https://aquasecurity.github.io/trivy-repo/deb/public.key | \
        gpg --dearmor --yes -o /usr/share/keyrings/trivy.gpg \
    && chmod go+r /usr/share/keyrings/trivy.gpg \
    && echo "deb [signed-by=/usr/share/keyrings/trivy.gpg] https://aquasecurity.github.io/trivy-repo/deb generic main" \
        > /etc/apt/sources.list.d/trivy.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends trivy \
    && rm -rf /var/lib/apt/lists/* \
    && trivy --version

RUN go install github.com/securego/gosec/v2/cmd/gosec@v2.27.1
