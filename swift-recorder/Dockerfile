ARG PLATFORM

FROM --platform=${PLATFORM} mcr.microsoft.com/oss/go/microsoft/golang:1.25-azurelinux3.0 AS builder
COPY internal/go.mod internal/go.sum internal/
COPY swift-recorder/go.mod swift-recorder/go.sum swift-recorder/
RUN cd swift-recorder && go mod download
WORKDIR /app
COPY internal/ internal/
COPY --exclude=swift-recorder/bin swift-recorder/ swift-recorder/
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
