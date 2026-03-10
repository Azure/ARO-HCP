SHELL = /bin/bash

REPOSITORY = $(shell git rev-parse --show-toplevel)
ARO_HCP_E2ETESTS_IMAGE = "aro-hcp-e2e-tests"
PLATFORM ?= "linux/amd64"

image:
	podman build --rm --platform=$(PLATFORM) \
	-f ../Containerfile.e2e \
	-t $(ARO_HCP_E2ETESTS_IMAGE):latest \
	$(REPOSITORY)
.PHONY: image

image-run:
	podman run --rm \
	-e CUSTOMER_SUBSCRIPTION="$(CUSTOMER_SUBSCRIPTION)" \
	-e AZURE_TENANT_ID=$(AZURE_TENANT_ID) \
	-e AZURE_CLIENT_ID=$(AZURE_CLIENT_ID) \
	-e AZURE_CLIENT_SECRET=$(AZURE_CLIENT_SECRET) \
	-e SKIP_CERT_VERIFICATION=true \
	$(ARO_HCP_E2ETESTS_IMAGE):latest \
	make e2e-local/run -o test/aro-hcp-tests
.PHONY: image-run
