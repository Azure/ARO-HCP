SHELL = /bin/bash
COMMIT = $(shell git rev-parse --short=7 HEAD)$(shell [[ $$(git status --porcelain) = "" ]] || echo -dirty)
CONFIG_PROFILE ?= dev
include ../../dev-infrastructure/configurations/$(CONFIG_PROFILE).mk
ARO_HCP_BASE_IMAGE ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io
ARO_HCP_IMAGE_SYNC_IMAGE ?= $(ARO_HCP_BASE_IMAGE)/image-sync/component-sync

image-sync:
	go build -tags=containers_image_openpgp,requirefips .

run:
	go run ./... -c example.yml

clean:
	rm -f aro-hcp-frontend

build-push: image push

image:
	cp ../../image-sync/configuration/mvp-image-sync.yml config.yml
	docker build --platform="linux/amd64" -f "./Dockerfile" -t ${ARO_HCP_IMAGE_SYNC_IMAGE}:${COMMIT} .

push:
	docker tag ${ARO_HCP_IMAGE_SYNC_IMAGE}:${COMMIT} ${ARO_HCP_IMAGE_SYNC_IMAGE}:latest
	docker push ${ARO_HCP_IMAGE_SYNC_IMAGE}:${COMMIT}
	docker push ${ARO_HCP_IMAGE_SYNC_IMAGE}:latest

.PHONY: image-sync clean image run deploy
