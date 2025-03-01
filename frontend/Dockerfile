# Base and builder image will need to be replaced by Fips compliant one
FROM --platform=linux/amd64 mcr.microsoft.com/oss/go/microsoft/golang:1.24-fips-cbl-mariner2.0@sha256:6f957d3a967ef83c5dbb7ea97184e69ff9c78412e7aa29da63eb3acbb4ab8216 as builder

WORKDIR /app
ADD archive.tar.gz .
# https://github.com/microsoft/go/tree/microsoft/main/eng/doc/fips#build-option-to-require-fips-mode
ENV CGO_ENABLED=1 GOFLAGS='-tags=requirefips'
RUN cd frontend && make frontend

FROM --platform=linux/amd64 mcr.microsoft.com/cbl-mariner/distroless/base:2.0-nonroot@sha256:ce44fc29db88c9aba8041a50c1abcd19a54f997c2b99a8c513e8ec113261374a
WORKDIR /
COPY --from=builder /app/frontend/aro-hcp-frontend .
ENTRYPOINT ["/aro-hcp-frontend"]
