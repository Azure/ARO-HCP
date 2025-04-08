FROM --platform=linux/amd64 mcr.microsoft.com/oss/go/microsoft/golang:1.24-fips-cbl-mariner2.0@sha256:a6a9c72282cc8e689c79b393bf875aa8e02e224e6d8928d7b366dfe62389d072 as builder

WORKDIR /app
ADD . .
# https://github.com/microsoft/go/tree/microsoft/main/eng/doc/fips#build-option-to-require-fips-mode
RUN CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -tags=containers_image_openpgp,requirefips .

FROM --platform=linux/amd64 mcr.microsoft.com/cbl-mariner/distroless/base:2.0-nonroot@sha256:ce44fc29db88c9aba8041a50c1abcd19a54f997c2b99a8c513e8ec113261374a
WORKDIR /

COPY --from=builder /app/image-sync .

CMD ["/image-sync"]
