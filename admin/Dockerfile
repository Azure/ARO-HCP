ARG PLATFORM

# Base and builder image will need to be replaced by Fips compliant one
FROM --platform=${PLATFORM} mcr.microsoft.com/oss/go/microsoft/golang:1.24-fips-cbl-mariner2.0 AS builder

WORKDIR /app/admin
COPY . .
# https://github.com/microsoft/go/tree/microsoft/main/eng/doc/fips#build-option-to-require-fips-mode
ENV CGO_ENABLED=1 GOFLAGS='-tags=requirefips'
ARG ARO_HCP_IMAGE_TAG
ARG ARO_HCP_REVISION
RUN make build ARO_HCP_IMAGE_TAG=${ARO_HCP_IMAGE_TAG} ARO_HCP_REVISION=${ARO_HCP_REVISION}

FROM --platform=${PLATFORM} mcr.microsoft.com/cbl-mariner/distroless/base:2.0-nonroot
WORKDIR /
COPY --from=builder /app/admin/aro-hcp-admin .
ARG ARO_HCP_REVISION
LABEL vcs-ref="${ARO_HCP_REVISION}"
ENTRYPOINT ["/aro-hcp-admin"]
