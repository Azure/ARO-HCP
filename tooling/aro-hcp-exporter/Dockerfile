ARG PLATFORM

FROM --platform=${PLATFORM} mcr.microsoft.com/oss/go/microsoft/golang:1.25-azurelinux3.0 AS builder
WORKDIR /app
COPY ./hcpctl/ hcpctl/
COPY ./aro-hcp-exporter/ aro-hcp-exporter/
# https://github.com/microsoft/go/tree/microsoft/main/eng/doc/fips#build-option-to-require-fips-mode
# NOTE: starting with go 1.27, GODEBUG=fips140=only can be used at runtime to also reject
# non-FIPS algorithms. See https://github.com/microsoft/go/tree/microsoft/main/eng/doc/fips
ENV CGO_ENABLED=1 GOFIPS140=latest
RUN cd aro-hcp-exporter && make build

FROM --platform=${PLATFORM} mcr.microsoft.com/azurelinux/distroless/base:3.0
USER 65532:65532
WORKDIR /
COPY --from=builder /app/aro-hcp-exporter/aro-hcp-exporter .
ENTRYPOINT ["/aro-hcp-exporter"]
