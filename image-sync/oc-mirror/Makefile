SHELL = /bin/bash
COMMIT = $(shell git rev-parse --short=7 HEAD)$(shell [[ $$(git status --porcelain) = "" ]])
CONFIG_PROFILE ?= dev
include ../../dev-infrastructure/configurations/$(CONFIG_PROFILE).mk
ARO_HCP_FULL ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io
OC_MIRROR_IMAGE ?= $(ARO_HCP_FULL)/image-sync/ocmirror
OC_MIRROR_IMAGE_TAGGED ?= $(OC_MIRROR_IMAGE):$(COMMIT)

build-push: image push

image:
	docker build --platform="linux/amd64" -f "./Dockerfile" -t ${OC_MIRROR_IMAGE_TAGGED} .

push: image
	docker tag ${OC_MIRROR_IMAGE_TAGGED} ${OC_MIRROR_IMAGE}:latest
	docker push ${OC_MIRROR_IMAGE_TAGGED}
	docker push ${OC_MIRROR_IMAGE}:latest

.PHONY: image push build-push
