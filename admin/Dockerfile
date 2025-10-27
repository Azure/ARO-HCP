# Base and builder image will need to be replaced by Fips compliant one
FROM --platform=linux/amd64 mcr.microsoft.com/oss/go/microsoft/golang:1.24-fips-azurelinux3.0 as builder

WORKDIR /app/admin
COPY . .
# https://github.com/microsoft/go/tree/microsoft/main/eng/doc/fips#build-option-to-require-fips-mode
ENV CGO_ENABLED=1 GOFLAGS='-tags=requirefips'
RUN go build -o aro-hcp-admin

FROM --platform=linux/amd64 mcr.microsoft.com/azurelinux/distroless/base:3.0
WORKDIR /
COPY --from=builder /app/admin/aro-hcp-admin .
ENTRYPOINT ["/aro-hcp-admin"]
