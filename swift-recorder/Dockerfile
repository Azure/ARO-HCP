ARG PLATFORM

FROM --platform=${PLATFORM} mcr.microsoft.com/oss/go/microsoft/golang:1.26-azurelinux3.0 AS builder
ENV GOTOOLCHAIN=local GOWORK=/app/go.work GOFLAGS=-mod=vendor GOPROXY=off
WORKDIR /app
# Dockerfile.dockerignore includes the complete workspace and vendor tree,
# while excluding locally built binaries and development setup files.
COPY . .
ARG ARO_HCP_REVISION
ENV CGO_ENABLED=1 GOFIPS140=latest
RUN --mount=type=cache,target=/go/cache \
    GOCACHE=/go/cache \
    make --directory swift-recorder build ARO_HCP_REVISION=${ARO_HCP_REVISION}

FROM --platform=${PLATFORM} mcr.microsoft.com/azurelinux/distroless/base:3.0
# The namespace helper needs root and CAP_SYS_ADMIN for setns.
USER 0:0
WORKDIR /
COPY --from=builder /app/swift-recorder/bin/swift-recorder .
ARG ARO_HCP_REVISION
ARG ARO_HCP_COMMIT_TIME
LABEL vcs-ref="${ARO_HCP_REVISION}"
LABEL vcs-ref-time="${ARO_HCP_COMMIT_TIME}"
LABEL vcs-url="https://github.com/Azure/ARO-HCP"
ENTRYPOINT ["/swift-recorder", "controller"]
