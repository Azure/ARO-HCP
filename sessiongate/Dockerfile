ARG PLATFORM

# Builder image installs tools needed to build aro-hcp-backend
FROM --platform=${PLATFORM} mcr.microsoft.com/oss/go/microsoft/golang:1.25-fips-azurelinux3.0 AS builder
COPY sessiongate/go.mod sessiongate/go.sum sessiongate/
RUN cd sessiongate && go mod download
WORKDIR /app
COPY . .

# https://github.com/microsoft/go/tree/microsoft/main/eng/doc/fips#build-option-to-require-fips-mode
ENV CGO_ENABLED=1 GOFLAGS='-tags=requirefips'
ARG ARO_HCP_IMAGE_TAG
ARG ARO_HCP_REVISION
RUN make --directory sessiongate build ARO_HCP_IMAGE_TAG=${ARO_HCP_IMAGE_TAG} ARO_HCP_REVISION=${ARO_HCP_REVISION}

FROM --platform=${PLATFORM} mcr.microsoft.com/azurelinux/distroless/base:3.0
USER 65532:65532
WORKDIR /
COPY --from=builder /app/sessiongate/bin/sessiongate .
USER 65532:65532

ENTRYPOINT ["/sessiongate", "controller"]
