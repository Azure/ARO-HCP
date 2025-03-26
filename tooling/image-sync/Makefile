TAG = $(shell git rev-parse --short=7 HEAD)$(shell [[ $$(git status --porcelain) = "" ]] || echo -dirty)
-include ../setup-env.mk
-include ../helm-cmd.mk

ARO_HCP_IMAGE_SYNC_IMAGE ?= ${ACR_NAME}.azurecr.io/{{ .imageSync.componentSync.image.repository }}

image-sync:
	go build -tags=containers_image_openpgp,requirefips .

run:
	go run ./... -c example.yml

clean:
	rm -f aro-hcp-frontend

build-push: image push

image:
	docker build --platform="linux/amd64" -f "./Dockerfile" -t ${ARO_HCP_IMAGE_SYNC_IMAGE}:${TAG} .

push:
	@if [ "$(DRY_RUN)" = "true" ]; then \
		echo "DRY_RUN is set to true, not pushing image..."; \
		exit 0; \
	fi
	az acr login --name ${ACR_NAME}
	docker tag ${ARO_HCP_IMAGE_SYNC_IMAGE}:${TAG} ${ARO_HCP_IMAGE_SYNC_IMAGE}:latest
	docker push ${ARO_HCP_IMAGE_SYNC_IMAGE}:${TAG}
	docker push ${ARO_HCP_IMAGE_SYNC_IMAGE}:latest

.PHONY: image-sync clean image run deploy
