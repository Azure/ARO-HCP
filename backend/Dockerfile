ARG PLATFORM

# Builder image installs tools needed to build aro-hcp-backend
FROM --platform=${PLATFORM} mcr.microsoft.com/oss/go/microsoft/golang:1.25-azurelinux3.0 AS builder
COPY internal/go.mod internal/go.sum internal/
COPY test-integration/go.mod test-integration/
COPY backend/go.mod backend/go.sum backend/
RUN cd backend && go mod download
WORKDIR /app
COPY . .
ARG TAG
# https://github.com/microsoft/go/tree/microsoft/main/eng/doc/fips#build-option-to-require-fips-mode
# NOTE: starting with go 1.27, GODEBUG=fips140=only can be used at runtime to also reject
# non-FIPS algorithms. See https://github.com/microsoft/go/tree/microsoft/main/eng/doc/fips
ENV CGO_ENABLED=1 GOFIPS140=latest
RUN make --directory backend ARO_HCP_IMAGE_TAG=${ARO_HCP_IMAGE_TAG}


# Deployment image copies aro-hcp-backend from builder image
FROM --platform=${PLATFORM} mcr.microsoft.com/azurelinux/distroless/base:3.0
USER 65532:65532
WORKDIR /
COPY --from=builder /app/backend/aro-hcp-backend .
ARG ARO_HCP_REVISION
ARG ARO_HCP_COMMIT_TIME
LABEL vcs-ref="${ARO_HCP_REVISION}"
LABEL vcs-ref-time="${ARO_HCP_COMMIT_TIME}"
LABEL vcs-url="https://github.com/Azure/ARO-HCP"
ENTRYPOINT ["/aro-hcp-backend"]
