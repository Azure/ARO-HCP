ARG PLATFORM

# Base and builder image will need to be replaced by Fips compliant one
FROM --platform=${PLATFORM} mcr.microsoft.com/oss/go/microsoft/golang:1.25-fips-azurelinux3.0 as builder
COPY internal/go.mod internal/go.sum internal/
COPY test-integration/go.mod test-integration/
COPY admin/server/go.mod admin/server/go.sum admin/server/
RUN cd admin/server && go mod download
WORKDIR /app
COPY . .
# https://github.com/microsoft/go/tree/microsoft/main/eng/doc/fips#build-option-to-require-fips-mode
ENV CGO_ENABLED=1 GOFLAGS='-tags=requirefips'
ARG ARO_HCP_IMAGE_TAG
ARG ARO_HCP_REVISION
RUN make --directory admin build ARO_HCP_IMAGE_TAG=${ARO_HCP_IMAGE_TAG} ARO_HCP_REVISION=${ARO_HCP_REVISION}

FROM --platform=${PLATFORM} mcr.microsoft.com/azurelinux/distroless/base:3.0
USER 65532:65532
WORKDIR /
COPY --from=builder /app/admin/aro-hcp-admin .
ARG ARO_HCP_REVISION
LABEL vcs-ref="${ARO_HCP_REVISION}"
ENTRYPOINT ["/aro-hcp-admin", "serve"]
