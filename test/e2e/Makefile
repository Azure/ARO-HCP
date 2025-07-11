SHELL = /bin/bash

REPOSITORY=$(shell git rev-parse --show-toplevel)
COMMIT_SHA=$(shell git rev-parse HEAD)
ARO_HCP_E2ETESTS_IMAGE="aro-hcp-e2e-tests"
LABEL_FILTER?="Positive||Negative"
SETUP_FILEPATH?=e2e-setup.json

run:
	go run github.com/onsi/ginkgo/v2/ginkgo run --tags E2Etests --junit-report ./report-$(shell date +'%s').xml ./
.PHONY: run

e2etest:
	go run github.com/onsi/ginkgo/v2/ginkgo build --tags E2Etests ./
.PHONY: e2etest

image:
	podman build --rm \
	--build-context ARO-HCP=$(REPOSITORY) \
	--env SETUP_FILEPATH=e2e-setup.json \
	-f ../Containerfile.e2e \
	-t $(ARO_HCP_E2ETESTS_IMAGE):$(COMMIT_SHA)
.PHONY: image

e2etest-run:
	podman run --rm \
	-e CUSTOMER_SUBSCRIPTION=$(CUSTOMER_SUBSCRIPTION) \
	-e AZURE_TENANT_ID=$(AZURE_TENANT_ID) \
	-e AZURE_CLIENT_ID=$(AZURE_CLIENT_ID) \
	-e AZURE_CLIENT_SECRET=$(AZURE_CLIENT_SECRET) \
	$(ARO_HCP_E2ETESTS_IMAGE):$(COMMIT_SHA) --ginkgo.label-filter=$(LABEL_FILTER)
.PHONY: e2etest-run
