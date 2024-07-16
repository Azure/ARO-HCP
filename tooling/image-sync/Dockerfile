# Base and builder image will need to be replaced by Fips compliant one
FROM --platform=${TARGETPLATFORM:-linux/amd64} mcr.microsoft.com/oss/go/microsoft/golang:1.22-fips-cbl-mariner2.0@sha256:d024591e53909d3f5d9f27cde4e1fea45f32963d97711042db36a499779630f8 as builder

WORKDIR /app
ADD . .
# https://github.com/microsoft/go/tree/microsoft/main/eng/doc/fips#build-option-to-require-fips-mode
ENV CGO_ENABLED=1 GOFLAGS='-tags=requirefips'

RUN tdnf install -y gpgme-devel; \
    tdnf clean all; \
    make image-sync

FROM --platform=${TARGETPLATFORM:-linux/amd64} mcr.microsoft.com/cbl-mariner/base/core:2.0-nonroot@sha256:fac93debd6c88c9935fdb85e08c3a57e601124018b87015635f50549b6527f5a
WORKDIR /

COPY --from=builder /app/image-sync .
ENTRYPOINT ["/image-sync"]