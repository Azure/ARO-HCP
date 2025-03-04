FROM --platform=linux/amd64 mcr.microsoft.com/oss/go/microsoft/golang:1.23-fips-cbl-mariner2.0@sha256:e6951a34cc0cbdc62b5110c4301dfe9c8daf8ee89c1b8616082a6e0b89cd820f as builder

WORKDIR /app
ADD . .
# https://github.com/microsoft/go/tree/microsoft/main/eng/doc/fips#build-option-to-require-fips-mode
RUN CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -tags=containers_image_openpgp,requirefips .

FROM --platform=linux/amd64 mcr.microsoft.com/cbl-mariner/distroless/base:2.0-nonroot@sha256:c83be1ce06e4f27f664db7b23551a7a6fef5d0bc49d6781a0f5a61815e682239
WORKDIR /

COPY --from=builder /app/image-sync .

CMD ["/image-sync"]
