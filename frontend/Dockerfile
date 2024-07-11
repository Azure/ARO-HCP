# Base and builder image will need to be replaced by Fips compliant one
FROM --platform=${TARGETPLATFORM:-linux/amd64} mcr.microsoft.com/oss/go/microsoft/golang:1.22-fips-cbl-mariner2.0@sha256:d024591e53909d3f5d9f27cde4e1fea45f32963d97711042db36a499779630f8 as builder

WORKDIR /app
ADD archive.tar.gz .
# https://github.com/microsoft/go/tree/microsoft/main/eng/doc/fips#build-option-to-require-fips-mode
ENV CGO_ENABLED=1 GOFLAGS='-tags=requirefips'
RUN cd frontend && make frontend

FROM --platform=${TARGETPLATFORM:-linux/amd64} mcr.microsoft.com/cbl-mariner/distroless/base:2.0-nonroot@sha256:ef0dc582fc2a8dd34fbb41341a3a9a1aaa70d4542ff04ce4e33a641e52e4807e
WORKDIR /
COPY --from=builder /app/frontend/aro-hcp-frontend .
ENTRYPOINT ["/aro-hcp-frontend"]
