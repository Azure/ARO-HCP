# Builder image installs tools needed to build aro-hcp-backend
FROM --platform=${TARGETPLATFORM:-linux/amd64} mcr.microsoft.com/oss/go/microsoft/golang:1.23-fips-cbl-mariner2.0@sha256:c5685a5169d83829545f22c87e0fec84cfc075cbcfc57e0d744ef27b05923093 as builder
WORKDIR /app
ADD archive.tar.gz .
# https://github.com/microsoft/go/tree/microsoft/main/eng/doc/fips#build-option-to-require-fips-mode
ENV CGO_ENABLED=1 GOFLAGS='-tags=requirefips'
RUN cd backend && make backend


# Deployment image copies aro-hcp-backend from builder image
FROM --platform=${TARGETPLATFORM:-linux/amd64} mcr.microsoft.com/cbl-mariner/distroless/base:2.0-nonroot@sha256:92275882d3b3f18da5ed51ab0b3bf0c5e5255390fe86f00414fca5007c377a11
WORKDIR /
COPY --from=builder /app/backend/aro-hcp-backend .
ENTRYPOINT ["/aro-hcp-backend"]
