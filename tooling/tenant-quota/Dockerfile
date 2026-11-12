ARG PLATFORM

# Builder image
FROM --platform=${PLATFORM} mcr.microsoft.com/oss/go/microsoft/golang:1.26-azurelinux3.0 AS builder
ENV GOTOOLCHAIN=local GOWORK=/app/go.work GOFLAGS=-mod=vendor GOPROXY=off
WORKDIR /app
# Dockerfile.dockerignore includes the complete workspace and vendor tree,
# while excluding locally built binaries and development setup files.
COPY . .
ARG TAG
# https://github.com/microsoft/go/tree/microsoft/main/eng/doc/fips#build-option-to-require-fips-mode
# NOTE: starting with go 1.27, GODEBUG=fips140=only can be used at runtime to also reject
# non-FIPS algorithms. See https://github.com/microsoft/go/tree/microsoft/main/eng/doc/fips
ENV CGO_ENABLED=1 GOFIPS140=latest

RUN cd tooling/tenant-quota && \
    go build -ldflags="-X github.com/Azure/ARO-HCP/internal/version.CommitSHA=${TAG}" -o tenant-quota-collector .

# Runtime image
FROM --platform=${PLATFORM} mcr.microsoft.com/azurelinux/distroless/base:3.0
USER 65532:65532
WORKDIR /
COPY --from=builder /app/tooling/tenant-quota/tenant-quota-collector .
ARG REVISION
LABEL vcs-ref="${REVISION}"
ENTRYPOINT ["/tenant-quota-collector"]
