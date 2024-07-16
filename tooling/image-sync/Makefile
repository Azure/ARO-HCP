SHELL = /bin/bash
COMMIT = $(shell git rev-parse --short=7 HEAD)$(shell [[ $$(git status --porcelain) = "" ]] || echo -dirty)
ARO_HCP_BASE_IMAGE ?= devarohcp.azurecr.io
ARO_HCP_IMAGE_SYNC_IMAGE ?= $(ARO_HCP_BASE_IMAGE)/image-sync/image-sync:$(COMMIT)

image-sync:
	go build -o image-sync .

run:
	go run ./... -c example.yml

clean:
	rm -f aro-hcp-frontend

build-push: image push

image:
	docker build --platform="linux/amd64" -f "./Dockerfile" -t ${ARO_HCP_IMAGE_SYNC_IMAGE} .

push: image
	docker push ${ARO_HCP_IMAGE_SYNC_IMAGE}

.PHONY: image-sync clean image run
