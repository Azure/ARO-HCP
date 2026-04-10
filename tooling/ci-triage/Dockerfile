ARG PLATFORM

FROM --platform=${PLATFORM} mcr.microsoft.com/oss/go/microsoft/golang:1.25-azurelinux3.0 AS builder
WORKDIR /app
COPY ./ci-triage/ ci-triage/
# https://github.com/microsoft/go/tree/microsoft/main/eng/doc/fips#build-option-to-require-fips-mode
# NOTE: starting with go 1.27, GODEBUG=fips140=only can be used at runtime to also reject
# non-FIPS algorithms. See https://github.com/microsoft/go/tree/microsoft/main/eng/doc/fips
# CGO_ENABLED=1 is also required for mattn/go-sqlite3
ENV CGO_ENABLED=1 GOFIPS140=latest
RUN cd ci-triage && make build

FROM --platform=${PLATFORM} mcr.microsoft.com/azurelinux/distroless/base:3.0
USER 65532:65532
WORKDIR /
COPY --from=builder /app/ci-triage/ci-triage .
ENTRYPOINT ["/ci-triage"]
