FROM mcr.microsoft.com/devcontainers/base:ubuntu@sha256:b1e3b08610ae0b2599f57c3a492eebc93d1d26dbaefb4a0bad0caa7149e4ec95

ARG TARGETARCH
# renovate: datasource=github-releases depName=kubernetes/kubernetes
ARG KUBECTL_VERSION=v1.37.2
# renovate: datasource=github-releases depName=kubernetes-sigs/kind
ARG KIND_VERSION=v0.33.0

RUN apt-get update \
    && apt-get install --yes --no-install-recommends \
        ca-certificates curl fuse-overlayfs make podman slirp4netns uidmap \
    && apt-get clean \
    && find /var/lib/apt/lists -maxdepth 1 -type f -delete

RUN set -eux; \
    case "${TARGETARCH}" in amd64|arm64) ;; *) echo "Unsupported architecture: ${TARGETARCH}" >&2; exit 1 ;; esac; \
    cd /tmp; \
    curl --fail --location --silent --show-error --output kubectl \
        "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${TARGETARCH}/kubectl"; \
    curl --fail --location --silent --show-error --output kubectl.sha256 \
        "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${TARGETARCH}/kubectl.sha256"; \
    echo "$(cat kubectl.sha256)  kubectl" | sha256sum --check; \
    install kubectl /usr/local/bin/kubectl; \
    curl --fail --location --silent --show-error --output "kind-linux-${TARGETARCH}" \
        "https://github.com/kubernetes-sigs/kind/releases/download/${KIND_VERSION}/kind-linux-${TARGETARCH}"; \
    curl --fail --location --silent --show-error --output "kind-linux-${TARGETARCH}.sha256sum" \
        "https://github.com/kubernetes-sigs/kind/releases/download/${KIND_VERSION}/kind-linux-${TARGETARCH}.sha256sum"; \
    sha256sum --check "kind-linux-${TARGETARCH}.sha256sum"; \
    install "kind-linux-${TARGETARCH}" /usr/local/bin/kind; \
    rm -f kubectl kubectl.sha256 "kind-linux-${TARGETARCH}" "kind-linux-${TARGETARCH}.sha256sum"
