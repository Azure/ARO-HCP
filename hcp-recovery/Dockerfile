ARG PLATFORM

# Builder image installs tools needed to build hcp-recovery
FROM --platform=${PLATFORM} mcr.microsoft.com/oss/go/microsoft/golang:1.25-azurelinux3.0 AS builder
COPY hcp-recovery/go.mod hcp-recovery/go.sum hcp-recovery/
RUN cd hcp-recovery && go mod download
WORKDIR /app
COPY . .

# https://github.com/microsoft/go/tree/microsoft/main/eng/doc/fips#build-option-to-require-fips-mode
ENV CGO_ENABLED=1 GOFLAGS='-tags=requirefips'
ARG ARO_HCP_IMAGE_TAG
ARG ARO_HCP_REVISION
RUN make --directory hcp-recovery build ARO_HCP_IMAGE_TAG=${ARO_HCP_IMAGE_TAG} ARO_HCP_REVISION=${ARO_HCP_REVISION}

FROM --platform=${PLATFORM} mcr.microsoft.com/azurelinux/distroless/base:3.0
USER 65532:65532
WORKDIR /
COPY --from=builder /app/hcp-recovery/bin/hcp-recovery .
USER 65532:65532

ENTRYPOINT ["/hcp-recovery", "controller"]
