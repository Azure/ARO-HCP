ARG PLATFORM

# Builder image
FROM --platform=${PLATFORM} mcr.microsoft.com/oss/go/microsoft/golang:1.25-azurelinux3.0 AS builder
COPY internal/go.mod internal/go.sum internal/
COPY tooling/tenant-quota/go.mod tooling/tenant-quota/go.sum tooling/tenant-quota/
RUN cd tooling/tenant-quota && go mod download
WORKDIR /app
COPY tooling/tenant-quota/ tooling/tenant-quota/
COPY internal/ internal/
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
