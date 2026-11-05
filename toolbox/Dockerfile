ARG PLATFORM

# kubectl source stage: the official upstream kubectl image, digest-pinned.
# kubectl is a static Go binary, so it runs unchanged on the azurelinux
# runtime below. Bump the digest together with the KUBECTL_VERSION label
# default in the runtime stage.
# v1.36.6
FROM --platform=${PLATFORM} registry.k8s.io/kubectl@sha256:7d7f926d2fa87ee4af9a0fb072e02a9ba64215125609beb5086888fd883f5419 AS kubectl

# Runtime image: azurelinux base provides bash and coreutils; the toolbox
# adds only kubectl. Consumers (mgmt-fixes jobs, ACM finalize jobs, the
# Velero install job, the arobit cert-watchdog) need exactly a shell and
# kubectl, including `kubectl kustomize`. Both stages follow PLATFORM; the
# Makefile pins linux/amd64 like the other in-repo images, and both base
# images are multi-arch, so other platforms build by overriding PLATFORM.
FROM --platform=${PLATFORM} mcr.microsoft.com/azurelinux/base/core:3.0
USER 65532:65532
COPY --from=kubectl /bin/kubectl /usr/local/bin/kubectl
ARG ARO_HCP_REVISION
ARG ARO_HCP_COMMIT_TIME
ARG KUBECTL_VERSION=v1.36.6
LABEL vcs-ref="${ARO_HCP_REVISION}"
LABEL vcs-ref-time="${ARO_HCP_COMMIT_TIME}"
LABEL vcs-url="https://github.com/Azure/ARO-HCP"
LABEL io.artifacthub.kubectl.version="${KUBECTL_VERSION}"
ENTRYPOINT ["/usr/local/bin/kubectl"]
