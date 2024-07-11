# Base and builder image will need to be replaced by Fips compliant one
FROM --platform=${TARGETPLATFORM:-linux/amd64} mcr.microsoft.com/oss/go/microsoft/golang:1.22-fips-cbl-mariner2.0@sha256:d024591e53909d3f5d9f27cde4e1fea45f32963d97711042db36a499779630f8 as builder

WORKDIR /app
ADD archive.tar.gz .
# https://github.com/microsoft/go/tree/microsoft/main/eng/doc/fips#build-option-to-require-fips-mode
ENV CGO_ENABLED=1 GOFLAGS='-tags=requirefips'
RUN cd frontend && make frontend

FROM --platform=${TARGETPLATFORM:-linux/amd64} mcr.microsoft.com/cbl-mariner/distroless/base:2.0-nonroot@sha256:e4faf092445ca7608aedfd7261788c270fc604dca9d1d8c8c4d18dedb1615142
WORKDIR /
COPY --from=builder /app/frontend/aro-hcp-frontend .
ENTRYPOINT ["/aro-hcp-frontend"]
