FROM --platform=linux/amd64 mcr.microsoft.com/oss/go/microsoft/golang:1.24-fips-cbl-mariner2.0@sha256:374281fe968134a44bcc7bd4b9e2d55dda61dcaa942fee5dda28cd38d54619cd as builder

WORKDIR /app
ADD . .
# https://github.com/microsoft/go/tree/microsoft/main/eng/doc/fips#build-option-to-require-fips-mode
RUN CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -tags=containers_image_openpgp,requirefips .

FROM --platform=linux/amd64 mcr.microsoft.com/cbl-mariner/distroless/base:2.0-nonroot@sha256:a5d5928601847a40c81fa397df33136c285866c974bf91ed94d688390fcc33f3
WORKDIR /

COPY --from=builder /app/image-sync .

CMD ["/image-sync"]
