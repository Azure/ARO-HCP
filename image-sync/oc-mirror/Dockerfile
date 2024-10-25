FROM mcr.microsoft.com/cbl-mariner/base/core:2.0 AS downloader

RUN set -eux; \
# Upgrade all packages per https://eng.ms/docs/more/containers-secure-supply-chain/updating
    tdnf update -y; \
    tdnf -y install unzip wget tar ca-certificates; \ 
    tdnf clean all

ENV OC_VERSION=4.16.3

RUN curl -sfL https://mirror.openshift.com/pub/openshift-v4/clients/ocp/${OC_VERSION}/openshift-client-linux.tar.gz -o oc.tar.gz  && \
    tar -zvxf oc.tar.gz && \
    mv oc kubectl /usr/local/bin

RUN curl -sfL https://mirror.openshift.com/pub/openshift-v4/clients/ocp/${OC_VERSION}/oc-mirror.tar.gz \
    -o oc-mirror.tar.gz && \
    tar -zvxf oc-mirror.tar.gz && \
    mv oc-mirror /usr/local/bin

FROM mcr.microsoft.com/cbl-mariner/base/core:2.0

RUN mkdir --mode=777 /workspace; \
    tdnf update -y; \
    tdnf -y install ca-certificates; \
    tdnf clean all

WORKDIR /workspace

COPY --chown=0:0 --chmod=755 --from=downloader \
    /usr/local/bin/oc-mirror \
    /usr/local/bin/oc \
    /usr/local/bin/kubectl \
    /usr/local/bin/

ENTRYPOINT ["oc-mirror", "--continue-on-error", "--config", "/etc/oc-mirror/imageset-config.yml"] 
