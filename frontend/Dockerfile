# Base and builder image will need to be replaced by Fips compliant one
FROM --platform=${TARGETPLATFORM:-linux/amd64} mcr.microsoft.com/oss/go/microsoft/golang:1.23-fips-cbl-mariner2.0@sha256:a6fe4aff3ebf62d2b37e79ae2ae069a5b8319123ec40763477f3937f681c0760 as builder

WORKDIR /app
ADD archive.tar.gz .
# https://github.com/microsoft/go/tree/microsoft/main/eng/doc/fips#build-option-to-require-fips-mode
ENV CGO_ENABLED=1 GOFLAGS='-tags=requirefips'
RUN cd frontend && make frontend

FROM --platform=${TARGETPLATFORM:-linux/amd64} mcr.microsoft.com/cbl-mariner/distroless/base:2.0-nonroot@sha256:92275882d3b3f18da5ed51ab0b3bf0c5e5255390fe86f00414fca5007c377a11
WORKDIR /
COPY --from=builder /app/frontend/aro-hcp-frontend .
ENTRYPOINT ["/aro-hcp-frontend"]
