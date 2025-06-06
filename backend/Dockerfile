ARG PLATFORM

# Builder image installs tools needed to build aro-hcp-backend
FROM --platform=${PLATFORM} mcr.microsoft.com/oss/go/microsoft/golang:1.24-fips-cbl-mariner2.0 AS builder
RUN yum install --assumeyes jq
ARG CURRENT_COMMIT
COPY internal/go.mod internal/go.sum internal/
COPY backend/go.mod backend/go.sum backend/
RUN cd backend && go mod download
WORKDIR /app
COPY . .
# https://github.com/microsoft/go/tree/microsoft/main/eng/doc/fips#build-option-to-require-fips-mode
ENV CGO_ENABLED=1 GOFLAGS='-tags=requirefips' CURRENT_COMMIT=${CURRENT_COMMIT}
RUN make --directory backend CURRENT_COMMIT=${CURRENT_COMMIT} ENV_VARS_FILE=/app/image-environment


# Deployment image copies aro-hcp-backend from builder image
FROM --platform=${PLATFORM} mcr.microsoft.com/cbl-mariner/distroless/base:2.0-nonroot
WORKDIR /
COPY --from=builder /app/backend/aro-hcp-backend .
ENTRYPOINT ["/aro-hcp-backend"]
