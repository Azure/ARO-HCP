# Base and builder image will need to be replaced by Fips compliant one
FROM --platform=linux/amd64 mcr.microsoft.com/oss/go/microsoft/golang:1.23-fips-cbl-mariner2.0@sha256:630c525f6b6d538d78c4f9480d0e257b7ddc6c4ee869b3b4051c4eaa357289d2 as builder

WORKDIR /app
ADD archive.tar.gz .
# https://github.com/microsoft/go/tree/microsoft/main/eng/doc/fips#build-option-to-require-fips-mode
ENV CGO_ENABLED=1 GOFLAGS='-tags=requirefips'
RUN cd frontend && make frontend

FROM --platform=linux/amd64 mcr.microsoft.com/cbl-mariner/distroless/base:2.0-nonroot@sha256:c83be1ce06e4f27f664db7b23551a7a6fef5d0bc49d6781a0f5a61815e682239
WORKDIR /
COPY --from=builder /app/frontend/aro-hcp-frontend .
ENTRYPOINT ["/aro-hcp-frontend"]
