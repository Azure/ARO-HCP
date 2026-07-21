ARG PLATFORM

FROM --platform=${PLATFORM} mcr.microsoft.com/oss/go/microsoft/golang:1.25-azurelinux3.0 AS builder
COPY tooling/azutils/go.mod tooling/azutils/go.sum tooling/azutils/
COPY tooling/utilitytypes/go.mod tooling/utilitytypes/
COPY tooling/hcpctl/go.mod tooling/hcpctl/go.sum tooling/hcpctl/
COPY tooling/metricscache/go.mod tooling/metricscache/go.sum tooling/metricscache/
COPY tooling/aro-hcp-exporter/go.mod tooling/aro-hcp-exporter/go.sum tooling/aro-hcp-exporter/
RUN cd tooling/aro-hcp-exporter && go mod download
WORKDIR /app
COPY . .
# https://github.com/microsoft/go/tree/microsoft/main/eng/doc/fips#build-option-to-require-fips-mode
# NOTE: starting with go 1.27, GODEBUG=fips140=only can be used at runtime to also reject
# non-FIPS algorithms. See https://github.com/microsoft/go/tree/microsoft/main/eng/doc/fips
ENV CGO_ENABLED=1 GOFIPS140=latest
RUN make --directory tooling/aro-hcp-exporter build

FROM --platform=${PLATFORM} mcr.microsoft.com/azurelinux/distroless/base:3.0
USER 65532:65532
WORKDIR /
COPY --from=builder /app/tooling/aro-hcp-exporter/aro-hcp-exporter .
ENTRYPOINT ["/aro-hcp-exporter"]
