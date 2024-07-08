FROM registry.access.redhat.com/ubi8/ubi:8.8 AS downloader

ENV OC_VERSION=4.16.0

RUN dnf -y install unzip glibc-langpack-en


RUN curl -sfL https://mirror.openshift.com/pub/openshift-v4/clients/ocp/${OC_VERSION}/openshift-client-linux.tar.gz \
    -o oc.tar.gz && \
    tar -zvxf oc.tar.gz && \
    mv oc kubectl /usr/local/bin

RUN curl -sfL https://mirror.openshift.com/pub/openshift-v4/clients/ocp/${OC_VERSION}/oc-mirror.tar.gz \
    -o oc-mirror.tar.gz && \
    tar -zvxf oc-mirror.tar.gz && \
    mv oc-mirror /usr/local/bin

FROM registry.access.redhat.com/ubi9/ubi-minimal:9.3


COPY --chown=0:0 --chmod=755 --from=downloader \
    /usr/local/bin/oc-mirror \
    /usr/local/bin/oc \
    /usr/local/bin/kubectl \
    /usr/local/bin/
