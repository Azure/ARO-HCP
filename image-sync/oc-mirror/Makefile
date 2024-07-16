SHELL = /bin/bash
COMMIT = $(shell git rev-parse --short=7 HEAD)$(shell [[ $$(git status --porcelain) = "" ]])
ARO_HCP_BASE_IMAGE ?= devarohcp.azurecr.io
OC_MIRROR_IMAGE ?= $(ARO_HCP_BASE_IMAGE)/image-sync/ocmirror:$(COMMIT)

build-push: image push

image:
	docker build -f "./Dockerfile" -t ${OC_MIRROR_IMAGE} .

push: image
	docker push ${OC_MIRROR_IMAGE}

.PHONY: image deploy
