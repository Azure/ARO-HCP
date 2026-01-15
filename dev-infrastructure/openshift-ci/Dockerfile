FROM registry.access.redhat.com/ubi9/ubi:latest
USER root
RUN cat /etc/dnf/dnf.conf || true
RUN rpm --import https://packages.microsoft.com/keys/microsoft.asc
RUN dnf install -y https://packages.microsoft.com/config/rhel/9.0/packages-microsoft-prod.rpm
RUN mkdir -p /etc/yum.repos.art/ci/ && ln -s /etc/yum.repos.d/microsoft-prod.repo /etc/yum.repos.art/ci/
RUN dnf install -y azure-cli libicu make git procps-ng
# Install Go 1.24.11 specifically
RUN curl -L https://go.dev/dl/go1.24.11.linux-amd64.tar.gz | tar -xzf - -C /usr/local && \
    ln -s /usr/local/go/bin/go /usr/local/bin/go && \
    ln -s /usr/local/go/bin/gofmt /usr/local/bin/gofmt
RUN mkdir -p /usr/local/bin
ENV PATH="/usr/local/bin:/usr/bin:$PATH"
RUN az bicep install && mv /root/.azure/bin/bicep /usr/local/bin
RUN az aks install-cli --install-location /usr/local/bin/kubectl --kubelogin-install-location /usr/local/bin/kubelogin
RUN curl -LO "https://mirror.openshift.com/pub/openshift-v4/clients/ocp/stable/openshift-client-linux.tar.gz" && \
    tar -xzf openshift-client-linux.tar.gz -C /usr/local/bin oc && \
    rm openshift-client-linux.tar.gz && \
    chmod +x /usr/local/bin/oc
RUN curl -sfLo - https://github.com/prometheus/prometheus/releases/download/v3.2.1/prometheus-3.2.1.linux-amd64.tar.gz | tar xzf - && \
    mv prometheus-3.2.1.linux-amd64/promtool /usr/local/bin/promtool && \
    chmod +x /usr/local/bin/promtool && \
    rm -rf prometheus-3.2.1.linux-amd64