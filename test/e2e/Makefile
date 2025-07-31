SHELL = /bin/bash

REPOSITORY=$(shell git rev-parse --show-toplevel)
COMMIT_SHA=$(shell git rev-parse HEAD)
ARO_HCP_E2ETESTS_IMAGE="aro-hcp-e2e-tests"
LABEL_FILTER?="Positivity:Positive||Positivity:Negative"
SETUP_FILEPATH?=e2e-setup.json
ARO_HCP_E2ETESTS_ACR ?= arohcpsvcdev
ARO_HCP_E2ETESTS_IMAGE_REGISTRY = $(ARO_HCP_E2ETESTS_ACR).azurecr.io
ARO_HCP_E2ETESTS_IMAGE_ACR = $(ARO_HCP_E2ETESTS_IMAGE_REGISTRY)/$(ARO_HCP_E2ETESTS_IMAGE)

run:
	go run github.com/onsi/ginkgo/v2/ginkgo run --tags E2Etests --junit-report ./report-$(shell date +'%s').xml ./
.PHONY: run

e2etest:
	go run github.com/onsi/ginkgo/v2/ginkgo build --tags E2Etests ./
.PHONY: e2etest

image:
	podman build --rm \
	-f ../Containerfile.e2e \
	-t $(ARO_HCP_E2ETESTS_IMAGE):$(COMMIT_SHA) \
	$(REPOSITORY)
.PHONY: image

push:
	DOCKER_COMMAND=podman az acr login --name $(ARO_HCP_E2ETESTS_ACR)
	podman push $(ARO_HCP_E2ETESTS_IMAGE):$(COMMIT_SHA) $(ARO_HCP_E2ETESTS_IMAGE_ACR):$(COMMIT_SHA)
	podman push $(ARO_HCP_E2ETESTS_IMAGE):$(COMMIT_SHA) $(ARO_HCP_E2ETESTS_IMAGE_ACR):latest
.PHONY: push

e2etest-run:
	podman run --rm \
	-v $(REPOSITORY)/test/e2e/test-artifacts:/test-artifacts:U,Z \
	-e CUSTOMER_SUBSCRIPTION=$(CUSTOMER_SUBSCRIPTION) \
	-e AZURE_TENANT_ID=$(AZURE_TENANT_ID) \
	-e AZURE_CLIENT_ID=$(AZURE_CLIENT_ID) \
	-e AZURE_CLIENT_SECRET=$(AZURE_CLIENT_SECRET) \
	$(ARO_HCP_E2ETESTS_IMAGE):$(COMMIT_SHA) --ginkgo.label-filter=$(LABEL_FILTER)
.PHONY: e2etest-run

bicep-build:
	az bicep build --file ../e2e-setup/bicep/demo.bicep --outdir test-artifacts/generated-test-artifacts/
	az bicep build --file ../e2e-setup/bicep/infra-only.bicep --outdir test-artifacts/generated-test-artifacts/
.PHONY: bicep-build
