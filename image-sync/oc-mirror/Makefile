SHELL = /bin/bash
COMMIT = $(shell git rev-parse --short=7 HEAD)$(shell [[ $$(git status --porcelain) = "" ]])
CONFIG_PROFILE ?= dev
include ../../dev-infrastructure/configurations/$(CONFIG_PROFILE).mk
ARO_HCP_BASE_IMAGE ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io
OC_MIRROR_IMAGE ?= $(ARO_HCP_BASE_IMAGE)/ocmirror:$(COMMIT)

build-push: image push

image:
	docker build --platform="linux/amd64" -f "./Dockerfile" -t ${OC_MIRROR_IMAGE} .

push: image
	docker push ${OC_MIRROR_IMAGE}

.PHONY: image push build-push
