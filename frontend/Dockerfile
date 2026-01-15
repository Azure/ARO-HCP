ARG PLATFORM

# Base and builder image will need to be replaced by Fips compliant one
FROM --platform=${PLATFORM} mcr.microsoft.com/oss/go/microsoft/golang:1.24-fips-azurelinux3.0 AS builder
COPY internal/go.mod internal/go.sum internal/
COPY test-integration/go.mod test-integration/
COPY frontend/go.mod frontend/go.sum frontend/
COPY test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp/go.mod test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp/
RUN cd frontend && go mod download
WORKDIR /app
COPY . .
ARG ARO_HCP_IMAGE_TAG
# https://github.com/microsoft/go/tree/microsoft/main/eng/doc/fips#build-option-to-require-fips-mode
ENV CGO_ENABLED=1 GOFLAGS='-tags=requirefips'
RUN make --directory frontend ARO_HCP_IMAGE_TAG=${ARO_HCP_IMAGE_TAG}

FROM --platform=${PLATFORM} mcr.microsoft.com/azurelinux/distroless/base:3.0
USER 65532:65532
WORKDIR /
COPY --from=builder /app/frontend/aro-hcp-frontend .
ARG ARO_HCP_REVISION
LABEL vcs-ref="${ARO_HCP_REVISION}"
ENTRYPOINT ["/aro-hcp-frontend"]
