# Base and builder image will need to be replaced by Fips compliant one
FROM --platform=linux/amd64 mcr.microsoft.com/oss/go/microsoft/golang:1.24.1-fips-cbl-mariner2.0@sha256:95edccac127e5f5da836e6fa6a122e73ec7caaf69324ff42f438f78f0a280724 as builder

WORKDIR /app/admin
COPY . . 
# https://github.com/microsoft/go/tree/microsoft/main/eng/doc/fips#build-option-to-require-fips-mode
ENV CGO_ENABLED=1 GOFLAGS='-tags=requirefips'
RUN go build -o aro-hcp-admin

FROM --platform=linux/amd64 mcr.microsoft.com/cbl-mariner/distroless/base:2.0-nonroot@sha256:ce44fc29db88c9aba8041a50c1abcd19a54f997c2b99a8c513e8ec113261374a
WORKDIR /
COPY --from=builder /app/admin/aro-hcp-admin .
ENTRYPOINT ["/aro-hcp-admin"]
