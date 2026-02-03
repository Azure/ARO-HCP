FROM registry.access.redhat.com/ubi9/ubi:latest
USER root
RUN cat /etc/dnf/dnf.conf || true
RUN rpm --import https://packages.microsoft.com/keys/microsoft.asc
RUN dnf install -y https://packages.microsoft.com/config/rhel/9.0/packages-microsoft-prod.rpm
RUN mkdir -p /etc/yum.repos.art/ci/ && ln -s /etc/yum.repos.d/microsoft-prod.repo /etc/yum.repos.art/ci/
RUN dnf install -y azure-cli libicu make git procps-ng
# Install Go 1.25.7 specifically
RUN curl -fsSL https://go.dev/dl/go1.25.7.linux-amd64.tar.gz | tar -xzf - -C /usr/local && \
    ln -s /usr/local/go/bin/go /usr/local/bin/go && \
    ln -s /usr/local/go/bin/gofmt /usr/local/bin/gofmt
RUN mkdir -p /usr/local/bin
ENV PATH="/usr/local/bin:/usr/bin:$PATH"
RUN az bicep install && mv /root/.azure/bin/bicep /usr/local/bin
# Install kubectl and kubelogin
RUN ARCH=$([ "$(uname -m)" = "aarch64" ] && echo "arm64" || echo "amd64") && \
    curl -fsSL -o /usr/local/bin/kubectl "https://dl.k8s.io/release/v1.35.0/bin/linux/${ARCH}/kubectl" && \
    chmod +x /usr/local/bin/kubectl && \
    curl -fsSL -o /tmp/kubelogin.zip "https://github.com/Azure/kubelogin/releases/download/v0.2.14/kubelogin-linux-${ARCH}.zip" && \
    python3 -m zipfile -e /tmp/kubelogin.zip /tmp/kubelogin && \
    mv /tmp/kubelogin/bin/linux_${ARCH}/kubelogin /usr/local/bin/kubelogin && \
    rm -rf /tmp/kubelogin.zip /tmp/kubelogin && \
    chmod +x /usr/local/bin/kubelogin
RUN curl -fsSLO "https://mirror.openshift.com/pub/openshift-v4/clients/ocp/stable/openshift-client-linux.tar.gz" && \
    tar -xzf openshift-client-linux.tar.gz -C /usr/local/bin oc && \
    rm openshift-client-linux.tar.gz && \
    chmod +x /usr/local/bin/oc
RUN curl -fsSL -o - https://github.com/prometheus/prometheus/releases/download/v3.2.1/prometheus-3.2.1.linux-amd64.tar.gz | tar xzf - && \
    mv prometheus-3.2.1.linux-amd64/promtool /usr/local/bin/promtool && \
    chmod +x /usr/local/bin/promtool && \
    rm -rf prometheus-3.2.1.linux-amd64