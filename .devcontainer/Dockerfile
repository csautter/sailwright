FROM mcr.microsoft.com/devcontainers/go:2.3.5-1.27-trixie

ENV DEBIAN_FRONTEND=noninteractive
ENV NODE_VERSION=24.22.0
# renovate: datasource=npm depName=renovate versioning=npm
ENV RENOVATE_VERSION="44.168.3"
# renovate: datasource=npm depName=@openai/codex versioning=npm
ENV CODEX_VERSION="0.164.0"
# renovate: datasource=npm depName=azure-functions-core-tools versioning=npm
ENV AZURE_FUNCTIONS_CORE_TOOLS_VERSION="4.16.0"
# renovate: datasource=custom.powershell-linux-x64 depName=PowerShell/PowerShell versioning=semver
ENV POWERSHELL_VERSION="7.6.7"
ENV POWERSHELL_SHA256_X64="6896f9e3a6b0a354bf470835a96da76d2f801cd50dcf047ad7c4563bf93bf431"
# renovate: datasource=custom.powershell-linux-arm64 depName=PowerShell/PowerShell version=7.6.7 versioning=semver
ENV POWERSHELL_SHA256_ARM64="ba465efdfb0984c1c5dc5ed9eb0b79a7e91a1b52c45f99377058c0659b84d3f1"
# renovate: datasource=custom.terragrunt-linux-amd64 depName=gruntwork-io/terragrunt versioning=semver
ENV TERRAGRUNT_VERSION="1.1.6"
ENV TERRAGRUNT_SHA256_AMD64="d75a80bb264758ba00dabcb17f4b507fcdab4ca90d9e41f96750df036bf69b04"
# renovate: datasource=custom.terragrunt-linux-arm64 depName=gruntwork-io/terragrunt version=1.1.6 versioning=semver
ENV TERRAGRUNT_SHA256_ARM64="44b43df99bae7a309fdb831180bd9efb84fb145cf38a80c139e490395581649d"
# renovate: datasource=custom.tflint-linux-amd64 depName=terraform-linters/tflint versioning=semver
ENV TFLINT_VERSION="0.66.0"
ENV TFLINT_SHA256_AMD64="22be11cadc25cd02401e94c0ccc91aa63467debb8485c2a755d98f4c0a9d1e2e"
# renovate: datasource=custom.tflint-linux-arm64 depName=terraform-linters/tflint version=0.66.0 versioning=semver
ENV TFLINT_SHA256_ARM64="1105a0a5ddee7cdac7957a3475571a946b839192ea1bc0c722c6c4686001067e"

# renovate: datasource=github-releases depName=hashicorp/terraform versioning=hashicorp
ENV TERRAFORM_VERSION="1.17.1"
# renovate: datasource=custom.trivy-linux-amd64 depName=aquasecurity/trivy versioning=semver
ENV TRIVY_VERSION="0.76.1"
ENV TRIVY_SHA256_AMD64="c7cbe3137fb8a48a17574bede3dd6beb16742d1e9772929940fc90082c39d9c0"
# renovate: datasource=custom.trivy-linux-arm64 depName=aquasecurity/trivy version=0.76.1 versioning=semver
ENV TRIVY_SHA256_ARM64="e174dff2a6dba5e3ae41f8d0537a6324405bd08132497340304c6b20e0f16a1f"

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

RUN npm install --global "@openai/codex@${CODEX_VERSION}" \
    && codex --version \
    && npm cache clean --force

RUN npm install --global "azure-functions-core-tools@${AZURE_FUNCTIONS_CORE_TOOLS_VERSION}" --unsafe-perm true \
    && func --version \
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
        "terraform=${TERRAFORM_VERSION}-1" \
    && rm -rf /var/lib/apt/lists/*

RUN arch="$(dpkg --print-architecture)" \
    && case "${arch}" in \
        amd64) trivy_arch="64bit"; trivy_sha256="${TRIVY_SHA256_AMD64}" ;; \
        arm64) trivy_arch="ARM64"; trivy_sha256="${TRIVY_SHA256_ARM64}" ;; \
        *) echo "Unsupported Trivy architecture: ${arch}" >&2; exit 1 ;; \
    esac \
    && curl -fsSL "https://github.com/aquasecurity/trivy/releases/download/v${TRIVY_VERSION}/trivy_${TRIVY_VERSION}_Linux-${trivy_arch}.tar.gz" -o /tmp/trivy.tar.gz \
    && printf '%s  %s\n' "${trivy_sha256}" /tmp/trivy.tar.gz | sha256sum -c - \
    && tar -xzf /tmp/trivy.tar.gz -C /usr/local/bin trivy \
    && rm -f /tmp/trivy.tar.gz \
    && trivy --version

RUN go install github.com/securego/gosec/v2/cmd/gosec@v2.29.0
