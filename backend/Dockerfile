# Builder image installs tools needed to build aro-hcp-backend
FROM --platform=linux/amd64 mcr.microsoft.com/oss/go/microsoft/golang:1.24-fips-cbl-mariner2.0@sha256:a6a9c72282cc8e689c79b393bf875aa8e02e224e6d8928d7b366dfe62389d072 as builder
WORKDIR /app
ADD archive.tar.gz .
# https://github.com/microsoft/go/tree/microsoft/main/eng/doc/fips#build-option-to-require-fips-mode
ENV CGO_ENABLED=1 GOFLAGS='-tags=requirefips'
RUN cd backend && make backend


# Deployment image copies aro-hcp-backend from builder image
FROM --platform=linux/amd64 mcr.microsoft.com/cbl-mariner/distroless/base:2.0-nonroot@sha256:c83be1ce06e4f27f664db7b23551a7a6fef5d0bc49d6781a0f5a61815e682239
WORKDIR /
COPY --from=builder /app/backend/aro-hcp-backend .
ENTRYPOINT ["/aro-hcp-backend"]
