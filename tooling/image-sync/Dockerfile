FROM --platform=linux/amd64 mcr.microsoft.com/oss/go/microsoft/golang:1.23-fips-cbl-mariner2.0@sha256:28a743b14a9d4e9ff19c522dfaa97b38cb603badf69181f983f5033708552564 as builder

WORKDIR /app
ADD . .
# https://github.com/microsoft/go/tree/microsoft/main/eng/doc/fips#build-option-to-require-fips-mode
RUN CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -tags=containers_image_openpgp,requirefips .

FROM --platform=linux/amd64 mcr.microsoft.com/cbl-mariner/distroless/base:2.0-nonroot@sha256:ef0dc582fc2a8dd34fbb41341a3a9a1aaa70d4542ff04ce4e33a641e52e4807e
WORKDIR /

COPY --from=builder /app/image-sync .

CMD ["/image-sync"]
