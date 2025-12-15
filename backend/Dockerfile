ARG PLATFORM

# Builder image installs tools needed to build aro-hcp-backend
FROM --platform=${PLATFORM} mcr.microsoft.com/oss/go/microsoft/golang:1.24-fips-azurelinux3.0 AS builder
COPY internal/go.mod internal/go.sum internal/
COPY test-integration/go.mod test-integration/
COPY backend/go.mod backend/go.sum backend/
RUN cd backend && go mod download
WORKDIR /app
COPY . .
ARG TAG
# https://github.com/microsoft/go/tree/microsoft/main/eng/doc/fips#build-option-to-require-fips-mode
ENV CGO_ENABLED=1 GOFLAGS='-tags=requirefips'
RUN make --directory backend ARO_HCP_IMAGE_TAG=${ARO_HCP_IMAGE_TAG}


# Deployment image copies aro-hcp-backend from builder image
FROM --platform=${PLATFORM} mcr.microsoft.com/azurelinux/distroless/base:3.0
USER 65532:65532
WORKDIR /
COPY --from=builder /app/backend/aro-hcp-backend .
ARG ARO_HCP_REVISION
LABEL vcs-ref="${ARO_HCP_REVISION}"
ENTRYPOINT ["/aro-hcp-backend"]
