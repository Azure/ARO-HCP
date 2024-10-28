FROM --platform=linux/amd64 mcr.microsoft.com/cbl-mariner/base/core:2.0 AS downloader

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

FROM --platform=linux/amd64 mcr.microsoft.com/oss/go/microsoft/golang:1.23-fips-cbl-mariner2.0@sha256:28a743b14a9d4e9ff19c522dfaa97b38cb603badf69181f983f5033708552564 as builder

WORKDIR /app
ADD . .
# https://github.com/microsoft/go/tree/microsoft/main/eng/doc/fips#build-option-to-require-fips-mode
RUN CGO_ENABLED=1 go build -tags=requirefips -o imageset-config-tmpl .

FROM --platform=linux/amd64 mcr.microsoft.com/cbl-mariner/base/core:2.0

RUN mkdir --mode=777 /workspace; \
    mkdir --mode=777 /config; \
    tdnf update -y; \
    tdnf -y install ca-certificates; \
    tdnf clean all

WORKDIR /workspace

COPY --chown=0:0 --chmod=755 --from=downloader \
    /usr/local/bin/oc-mirror \
    /usr/local/bin/oc \
    /usr/local/bin/kubectl \
    /usr/local/bin/
COPY --chown=0:0 --chmod=755 --from=builder \
    /app/imageset-config.yml.tmpl \
    /config/
COPY --chown=0:0 --chmod=755 --from=builder \
    /app/mirror.sh \
    /app/imageset-config-tmpl \
    /usr/local/bin/

ENTRYPOINT ["/usr/local/bin/mirror.sh"]
