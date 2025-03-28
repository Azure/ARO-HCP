SHELL = /bin/bash
DEPLOY_ENV ?= personal-dev
$(shell ../../templatize.sh $(DEPLOY_ENV) config.tmpl.mk config.mk)
include config.mk
COMMIT = $(shell git rev-parse --short=7 HEAD)$(shell [[ $$(git status --porcelain) = "" ]])

ifeq ($(shell uname), Darwin)
	AUTH_FILE = ${HOME}/.config/containers/auth.json
else
	AUTH_FILE = ${HOME}/.docker/config.json
endif

build-push: image push
.PHONY: build-push

image:
	docker build --platform="linux/amd64" -f "./Dockerfile" -t ${OC_MIRROR_IMAGE_TAGGED} .
.PHONY: image

push: image
	az acr login --name ${ARO_HCP_IMAGE_ACR}
	docker tag ${OC_MIRROR_IMAGE_TAGGED} ${OC_MIRROR_IMAGE}:latest
	docker push ${OC_MIRROR_IMAGE_TAGGED}
	docker push ${OC_MIRROR_IMAGE}:latest
.PHONY: push

acm-dry-run: image
	@docker run -it --rm --tmpfs /oc-mirror-workspace \
		-e XDG_RUNTIME_DIR=/ \
		-v ${AUTH_FILE}:/containers/auth.json:z \
		-v ${HOME}/.azure:/root/.azure:z \
		-e IMAGE_SET_CONFIG=$(shell cat ${PWD}/test/acm-image-set-config.yml | base64) \
		-e REGISTRY=${ARO_HCP_IMAGE_ACR} \
		-e REGISTRY_URL=${ARO_HCP_IMAGE_ACR_URL} \
		-e OC_MIRROR_COMPATIBILITY="NOCATALOG" \
		${OC_MIRROR_IMAGE_TAGGED} --dry-run
.PHONY: acm-dry-run

ocp-dry-run: image
	@docker run -it --rm --tmpfs /oc-mirror-workspace \
		-e XDG_RUNTIME_DIR=/ \
		-v ${AUTH_FILE}:/containers/auth.json:z \
		-v ${HOME}/.azure:/root/.azure:z \
		-e IMAGE_SET_CONFIG=$(shell cat ${PWD}/test/ocp-image-set-config.yml | base64) \
		-e REGISTRY=${ARO_HCP_OCP_IMAGE_ACR} \
		-e REGISTRY_URL=${ARO_HCP_OCP_IMAGE_ACR_URL} \
		-e OC_MIRROR_COMPATIBILITY="LATEST" \
		${OC_MIRROR_IMAGE_TAGGED} --dry-run
.PHONY: ocp-dry-run
