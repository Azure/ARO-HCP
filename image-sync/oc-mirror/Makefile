SHELL = /bin/bash
CONTAINER_ENGINE ?= $(shell command -v podman 2>/dev/null || echo docker)
DEPLOY_ENV ?= pers
COMMIT = $(shell git rev-parse --short=7 HEAD)$(shell [[ $$(git status --porcelain) = "" ]])

include ../../setup-templatize-env.mk

ifeq ($(shell uname), Darwin)
	AUTH_FILE = ${HOME}/.config/containers/auth.json
else
	AUTH_FILE = ${HOME}/.docker/config.json
endif

image:
	$(CONTAINER_ENGINE) build --platform="linux/amd64" -f "./Dockerfile" -t ${OC_MIRROR_IMAGE_TAGGED} .
.PHONY: image

build-and-push: image
	az acr login --name ${ARO_HCP_IMAGE_ACR}
	$(CONTAINER_ENGINE) tag ${OC_MIRROR_IMAGE_TAGGED} ${OC_MIRROR_IMAGE}:latest
	$(CONTAINER_ENGINE) push ${OC_MIRROR_IMAGE_TAGGED}
	$(CONTAINER_ENGINE) push ${OC_MIRROR_IMAGE}:latest
.PHONY: build-and-push

acm-dry-run: image
	@$(CONTAINER_ENGINE) run -it --rm --tmpfs /oc-mirror-workspace \
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
	@$(CONTAINER_ENGINE) run -it --rm --tmpfs /oc-mirror-workspace \
		-e XDG_RUNTIME_DIR=/ \
		-v ${AUTH_FILE}:/containers/auth.json:z \
		-v ${HOME}/.azure:/root/.azure:z \
		-e IMAGE_SET_CONFIG=$(shell cat ${PWD}/test/ocp-image-set-config.yml | base64) \
		-e REGISTRY=${ARO_HCP_OCP_IMAGE_ACR} \
		-e REGISTRY_URL=${ARO_HCP_OCP_IMAGE_ACR_URL} \
		-e OC_MIRROR_COMPATIBILITY="LATEST" \
		${OC_MIRROR_IMAGE_TAGGED} --dry-run
.PHONY: ocp-dry-run
