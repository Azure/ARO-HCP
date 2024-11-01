SHELL = /bin/bash
DEPLOY_ENV ?= personal-dev
$(shell ../../templatize.sh $(DEPLOY_ENV) config.tmpl.mk config.mk)
include config.mk
COMMIT = $(shell git rev-parse --short=7 HEAD)$(shell [[ $$(git status --porcelain) = "" ]])

build-push: image push

image:
	docker build --platform="linux/amd64" -f "./Dockerfile" -t ${OC_MIRROR_IMAGE_TAGGED} .

push: image
	az acr login --name ${ARO_HCP_IMAGE_ACR}
	docker tag ${OC_MIRROR_IMAGE_TAGGED} ${OC_MIRROR_IMAGE}:latest
	docker push ${OC_MIRROR_IMAGE_TAGGED}
	docker push ${OC_MIRROR_IMAGE}:latest

.PHONY: image push build-push
