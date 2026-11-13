ARG PLATFORM

# Builder: the same Microsoft Go FIPS toolchain as every other in-repo image.
# The kubectl entry point is toolbox/main.go (a copy of upstream's
# k8s.io/kubernetes/cmd/kubectl/kubectl.go), compiled from this repository's
# vendored k8s.io/kubectl — no prebuilt upstream binary, no network fetch.
FROM --platform=${PLATFORM} mcr.microsoft.com/oss/go/microsoft/golang:1.26-azurelinux3.0 AS builder
ENV GOTOOLCHAIN=local GOWORK=/app/go.work GOFLAGS=-mod=vendor GOPROXY=off
WORKDIR /app
COPY . .
ARG ARO_HCP_REVISION
ARG ARO_HCP_COMMIT_TIME
ENV CGO_ENABLED=1 GOFIPS140=latest
# The reported kubectl version is derived from the vendored module itself, so
# it can never drift from the code that was actually built (the failure mode
# of an independently set version label). The ARO-HCP revision is recorded
# separately in the image labels, not misrepresented as a Kubernetes commit.
# The build fails unless the binary carries the FIPS build provenance.
RUN KUBECTL_VERSION=$(GOFLAGS= go list -m -f {{.Version}} k8s.io/kubectl) && \
    go test ./toolbox/... && \
    go run ./toolbox/cmd/verify-skew ./config/config.yaml "${KUBECTL_VERSION}" && \
    go build -trimpath \
      -ldflags "-s -w \
        -X k8s.io/client-go/pkg/version.gitVersion=${KUBECTL_VERSION} \
        -X k8s.io/component-base/version.gitVersion=${KUBECTL_VERSION}" \
      -o /out/kubectl github.com/Azure/ARO-HCP/toolbox && \
    /out/kubectl version --client -o json | grep -q "\"gitVersion\": \"${KUBECTL_VERSION}\"" && \
    go version -m /out/kubectl | grep -q 'GOFIPS140=latest' && \
    go version -m /out/kubectl | grep -q 'microsoft_systemcrypto=1' && \
    go version -m /out/kubectl | grep -q 'CGO_ENABLED=1'

# Runtime image: azurelinux base provides bash, coreutils and the FIPS
# OpenSSL provider (symcryptprovider) that the Microsoft Go crypto boundary
# requires. Consumers (mgmt-fixes jobs, ACM finalize jobs, the Velero install
# job, the arobit cert-watchdog) need exactly a shell and kubectl, including
# `kubectl kustomize`.
FROM --platform=${PLATFORM} mcr.microsoft.com/azurelinux/base/core:3.0
USER 65532:65532
COPY --from=builder /out/kubectl /usr/local/bin/kubectl
ARG ARO_HCP_REVISION
ARG ARO_HCP_COMMIT_TIME
LABEL vcs-ref="${ARO_HCP_REVISION}"
LABEL vcs-ref-time="${ARO_HCP_COMMIT_TIME}"
LABEL vcs-url="https://github.com/Azure/ARO-HCP"
ENTRYPOINT ["/usr/local/bin/kubectl"]
