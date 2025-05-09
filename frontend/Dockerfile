ARG PLATFORM

# Base and builder image will need to be replaced by Fips compliant one
FROM --platform=${PLATFORM} mcr.microsoft.com/oss/go/microsoft/golang:1.24-fips-cbl-mariner2.0 AS builder
RUN yum install --assumeyes jq
ARG CURRENT_COMMIT
WORKDIR /app
COPY . .
# https://github.com/microsoft/go/tree/microsoft/main/eng/doc/fips#build-option-to-require-fips-mode
ENV CGO_ENABLED=1 GOFLAGS='-tags=requirefips' CURRENT_COMMIT=${CURRENT_COMMIT}
RUN make --directory frontend CURRENT_COMMIT=${CURRENT_COMMIT}

FROM --platform=${PLATFORM} mcr.microsoft.com/cbl-mariner/distroless/base:2.0-nonroot
WORKDIR /
COPY --from=builder /app/frontend/aro-hcp-frontend .
ENTRYPOINT ["/aro-hcp-frontend"]
