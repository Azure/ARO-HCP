ARG PLATFORM

# Builder image
FROM --platform=${PLATFORM} mcr.microsoft.com/oss/go/microsoft/golang:1.24-fips-azurelinux3.0 AS builder
COPY internal/go.mod internal/go.sum internal/
COPY dev-infrastructure/sre-tooling/tenant-quota/go.mod dev-infrastructure/sre-tooling/tenant-quota/go.sum dev-infrastructure/sre-tooling/tenant-quota/
RUN cd dev-infrastructure/sre-tooling/tenant-quota && go mod download
WORKDIR /app
COPY dev-infrastructure/sre-tooling/tenant-quota/ dev-infrastructure/sre-tooling/tenant-quota/
COPY internal/ internal/
ARG TAG
# https://github.com/microsoft/go/tree/microsoft/main/eng/doc/fips#build-option-to-require-fips-mode
ENV CGO_ENABLED=1 GOFLAGS='-tags=requirefips'

RUN cd dev-infrastructure/sre-tooling/tenant-quota && \
    go build -ldflags="-X github.com/Azure/ARO-HCP/internal/version.CommitSHA=${TAG}" -o custom-metrics-collector .

# Runtime image
FROM --platform=${PLATFORM} mcr.microsoft.com/azurelinux/distroless/base:3.0
USER 65532:65532
WORKDIR /
COPY --from=builder /app/dev-infrastructure/sre-tooling/tenant-quota/custom-metrics-collector .
ARG REVISION
LABEL vcs-ref="${REVISION}"
ENTRYPOINT ["/custom-metrics-collector"]


