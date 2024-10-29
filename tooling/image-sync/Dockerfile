FROM --platform=linux/amd64 mcr.microsoft.com/oss/go/microsoft/golang:1.23-fips-cbl-mariner2.0@sha256:6c1b07df15c152fb6a7f4eeece5f50824d83b8bf672709cc951aaa0d5c29887f as builder

WORKDIR /app
ADD . .
# https://github.com/microsoft/go/tree/microsoft/main/eng/doc/fips#build-option-to-require-fips-mode
RUN CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -tags=containers_image_openpgp,requirefips .

FROM --platform=linux/amd64 mcr.microsoft.com/cbl-mariner/distroless/base:2.0-nonroot@sha256:acb1ab4d2162ecebbe67888bf679f26dcaef29c153954a09396e020e9639862d
WORKDIR /

COPY --from=builder /app/image-sync .

CMD ["/image-sync"]
