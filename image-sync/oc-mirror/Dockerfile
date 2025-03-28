FROM --platform=linux/amd64 mcr.microsoft.com/cbl-mariner/base/core:2.0 AS downloader

RUN set -eux; \
# Upgrade all packages per https://eng.ms/docs/more/containers-secure-supply-chain/updating
    tdnf update -y; \
    tdnf -y install unzip wget tar ca-certificates; \
    tdnf clean all

ENV OC_MIRROR_4_16_VERSION=4.16.3
ENV OC_MIRROR_4_18_VERSION=4.18.7
ENV OC_VERSION=4.18.0-rc.9
ENV YQ_VERSION=v4.2.0

RUN curl -sfL https://mirror.openshift.com/pub/openshift-v4/clients/ocp/${OC_VERSION}/openshift-client-linux.tar.gz -o oc.tar.gz  && \
    tar -zvxf oc.tar.gz && \
    mv oc kubectl /usr/local/bin

RUN curl -sfL https://mirror.openshift.com/pub/openshift-v4/clients/ocp/${OC_MIRROR_4_16_VERSION}/oc-mirror.tar.gz \
    -o oc-mirror.tar.gz && \
    tar -zvxf oc-mirror.tar.gz && \
    mv oc-mirror /usr/local/bin/oc-mirror-4.16

RUN curl -sfL https://mirror.openshift.com/pub/openshift-v4/clients/ocp/${OC_MIRROR_4_18_VERSION}/oc-mirror.tar.gz \
    -o oc-mirror.tar.gz && \
    tar -zvxf oc-mirror.tar.gz && \
    mv oc-mirror /usr/local/bin/oc-mirror-4.18

RUN curl -sfL https://github.com/mikefarah/yq/releases/download/${YQ_VERSION}/yq_linux_amd64.tar.gz \
    -o yq.tar.gz && \
    tar -zvxf yq.tar.gz && \
    mv yq_linux_amd64 /usr/local/bin/yq

FROM --platform=linux/amd64 mcr.microsoft.com/azure-cli:cbl-mariner2.0

RUN mkdir --mode=777 /workspace; \
    mkdir --mode=777 /config; \
    tdnf update -y; \
    tdnf -y install ca-certificates yq; \
    tdnf clean all

WORKDIR /workspace

ADD mirror.sh /usr/local/bin/mirror.sh
ADD docker-login.sh /usr/local/bin/docker-login.sh

COPY --chown=0:0 --chmod=755 --from=downloader \
    /usr/local/bin/oc-mirror-4.16 \
    /usr/local/bin/oc-mirror-4.18 \
    /usr/local/bin/oc \
    /usr/local/bin/kubectl \
    /usr/local/bin/yq \
    /usr/local/bin/

ENTRYPOINT ["/usr/local/bin/mirror.sh"]
