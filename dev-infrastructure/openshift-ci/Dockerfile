ARG BUILDER_IMAGE_TAG
FROM registry.ci.openshift.org/ocp/builder:${BUILDER_IMAGE_TAG}

# Tool versions - defaults here must be kept in sync with /versions.mk
ARG PROMTOOL_VERSION=3.2.1

USER root
RUN cat /etc/dnf/dnf.conf || true
RUN rpm --import https://packages.microsoft.com/keys/microsoft.asc
RUN dnf install -y https://packages.microsoft.com/config/rhel/9.0/packages-microsoft-prod.rpm
RUN mkdir -p /etc/yum.repos.art/ci/ /etc/yum.repos.art/localdev/ && \
    ln -s /etc/yum.repos.d/microsoft-prod.repo /etc/yum.repos.art/ci/ && \
    ln -s /etc/yum.repos.d/microsoft-prod.repo /etc/yum.repos.art/localdev/
RUN dnf install -y azure-cli libicu make git procps-ng
RUN az bicep install && mv /root/.azure/bin/bicep /usr/local/bin
# Install kubectl and kubelogin
RUN ARCH=$([ "$(uname -m)" = "aarch64" ] && echo "arm64" || echo "amd64") && \
    KUBECTL_VER=$(curl -fsSL --retry 3 https://dl.k8s.io/release/stable.txt) && \
    curl -fsSL -o /usr/local/bin/kubectl "https://dl.k8s.io/release/${KUBECTL_VER}/bin/linux/${ARCH}/kubectl" && \
    chmod +x /usr/local/bin/kubectl && \
    curl -fsSL -o /tmp/kubelogin.zip "https://github.com/Azure/kubelogin/releases/latest/download/kubelogin-linux-${ARCH}.zip" && \
    python3 -m zipfile -e /tmp/kubelogin.zip /tmp/kubelogin && \
    mv /tmp/kubelogin/bin/linux_${ARCH}/kubelogin /usr/local/bin/kubelogin && \
    rm -rf /tmp/kubelogin.zip /tmp/kubelogin && \
    chmod +x /usr/local/bin/kubelogin
RUN ARCH=$([ "$(uname -m)" = "aarch64" ] && echo "arm64" || echo "amd64") && \
    SUFFIX=$([ "$ARCH" = "arm64" ] && echo "-arm64" || echo "") && \
    curl -fsSLO "https://mirror.openshift.com/pub/openshift-v4/clients/ocp/stable/openshift-client-linux${SUFFIX}.tar.gz" && \
    tar -xzf openshift-client-linux${SUFFIX}.tar.gz -C /usr/local/bin oc && \
    rm openshift-client-linux${SUFFIX}.tar.gz && \
    chmod +x /usr/local/bin/oc
RUN ARCH=$([ "$(uname -m)" = "aarch64" ] && echo "arm64" || echo "amd64") && \
    curl -fsSL -o - https://github.com/prometheus/prometheus/releases/download/v${PROMTOOL_VERSION}/prometheus-${PROMTOOL_VERSION}.linux-${ARCH}.tar.gz | tar xzf - && \
    mv prometheus-${PROMTOOL_VERSION}.linux-${ARCH}/promtool /usr/local/bin/promtool && \
    chmod +x /usr/local/bin/promtool && \
    rm -rf prometheus-${PROMTOOL_VERSION}.linux-${ARCH}
# Install Go-based development tools (bingo-managed)
COPY . /tmp/aro-hcp
RUN cd /tmp/aro-hcp && \
    GOFLAGS='-mod=readonly' GOBIN=/usr/local/bin make install-tools && \
    go clean -cache -modcache && \
    rm -rf /tmp/aro-hcp
# Runtime Go builds in CI run as an arbitrary OpenShift UID against a cold module
# cache. On a checksum miss Go consults the checksum DB and writes
# /go/pkg/sumdb/sum.golang.org/latest; the runner presents that path read-only to
# the pod, causing intermittent
# "verifying module: ... open /go/pkg/sumdb/sum.golang.org/latest: permission denied".
# GOSUMDB=off removes the sumdb dependency entirely. Integrity is still enforced by the
# committed go.sum / go.work.sum, which are verified on every build; GOSUMDB is only
# consulted to authenticate a module hash that is not already recorded in the consulted
# sum file (e.g. on a cold cache), so disabling it in CI trusts the committed sums alone.
# Set after install-tools so build-time tool compilation keeps sumdb verification; the
# ENV persists into the running container, covering the arbitrary-UID runtime builds.
ENV GOSUMDB=off
# Pre-create /go/pkg/sumdb so the group-writable perms below also cover it
# (defense in depth alongside GOSUMDB=off).
RUN mkdir -p /go/pkg/mod /go/pkg/sumdb /go/bin && \
    chgrp -R 0 /go && \
    chmod -R g=u /go
