# Base and builder image will need to be replaced by Fips compliant one
FROM --platform=${TARGETPLATFORM:-linux/amd64} mcr.microsoft.com/oss/go/microsoft/golang:1.22-fips-cbl-mariner2.0@sha256:826e7accc0df1fd6e4494c49cba6a577e42d1fffe6db05925b3473831b60d774 as builder

WORKDIR /app
ADD archive.tar.gz .
# https://github.com/microsoft/go/tree/microsoft/main/eng/doc/fips#build-option-to-require-fips-mode
ENV CGO_ENABLED=1 GOFLAGS='-tags=requirefips'
RUN cd frontend && make frontend

FROM --platform=${TARGETPLATFORM:-linux/amd64} mcr.microsoft.com/cbl-mariner/distroless/base:2.0-nonroot@sha256:92275882d3b3f18da5ed51ab0b3bf0c5e5255390fe86f00414fca5007c377a11
WORKDIR /
COPY --from=builder /app/frontend/aro-hcp-frontend .
ENTRYPOINT ["/aro-hcp-frontend"]
