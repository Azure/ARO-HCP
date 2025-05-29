SHELL = /bin/bash
DEPLOY_ENV ?= pers
$(shell ../../templatize.sh $(DEPLOY_ENV) config.tmpl.mk config.mk)
include config.mk
COMMIT = $(shell git rev-parse --short=7 HEAD)$(shell [[ $$(git status --porcelain) = "" ]] || echo -dirty)

image-sync:
	go build -tags=containers_image_openpgp,requirefips .

run:
	go run ./... -c example.yml

clean:
	rm -f aro-hcp-frontend

build-push: image push

image:
	docker build --platform="linux/amd64" -f "./Dockerfile" -t ${ARO_HCP_IMAGE_SYNC_IMAGE}:${COMMIT} .

push:
	az acr login --name ${ARO_HCP_IMAGE_ACR}
	docker tag ${ARO_HCP_IMAGE_SYNC_IMAGE}:${COMMIT} ${ARO_HCP_IMAGE_SYNC_IMAGE}:latest
	docker push ${ARO_HCP_IMAGE_SYNC_IMAGE}:${COMMIT}
	docker push ${ARO_HCP_IMAGE_SYNC_IMAGE}:latest

.PHONY: image-sync clean image run deploy
