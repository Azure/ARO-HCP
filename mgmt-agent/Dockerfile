ARG PLATFORM

# Builder image installs tools needed to build mgmt-agent
FROM --platform=${PLATFORM} mcr.microsoft.com/oss/go/microsoft/golang:1.25-azurelinux3.0 AS builder
COPY internal/go.mod internal/go.sum internal/
COPY mgmt-agent/go.mod mgmt-agent/go.sum mgmt-agent/
COPY swift-recorder/go.mod swift-recorder/go.sum swift-recorder/
RUN cd mgmt-agent && go mod download
RUN cd swift-recorder && go mod download
WORKDIR /app
COPY internal/ internal/
COPY --exclude=mgmt-agent/bin mgmt-agent/ mgmt-agent/
COPY --exclude=swift-recorder/bin swift-recorder/ swift-recorder/
ARG ARO_HCP_REVISION
# https://github.com/microsoft/go/tree/microsoft/main/eng/doc/fips#build-option-to-require-fips-mode
# NOTE: starting with go 1.27, GODEBUG=fips140=only can be used at runtime to also reject
# non-FIPS algorithms. See https://github.com/microsoft/go/tree/microsoft/main/eng/doc/fips
ENV CGO_ENABLED=1 GOFIPS140=latest
RUN --mount=type=cache,target=/go/cache \
    GOCACHE=/go/cache \
    make --directory mgmt-agent build ARO_HCP_REVISION=${ARO_HCP_REVISION}
# Temporary CI bootstrap: the existing mgmt-agent image dependency carries the
# recorder until its dedicated image build is onboarded in openshift/release.
RUN --mount=type=cache,target=/go/cache \
    GOCACHE=/go/cache \
    make --directory swift-recorder build ARO_HCP_REVISION=${ARO_HCP_REVISION}

FROM --platform=${PLATFORM} mcr.microsoft.com/azurelinux/distroless/base:3.0
USER 65532:65532
WORKDIR /
COPY --from=builder /app/mgmt-agent/bin/mgmt-agent .
COPY --from=builder /app/swift-recorder/bin/swift-recorder .
ARG ARO_HCP_REVISION
ARG ARO_HCP_COMMIT_TIME
LABEL vcs-ref="${ARO_HCP_REVISION}"
LABEL vcs-ref-time="${ARO_HCP_COMMIT_TIME}"
LABEL vcs-url="https://github.com/Azure/ARO-HCP"
ENTRYPOINT ["/mgmt-agent", "controller"]
