SHELL = /bin/bash
TAG ?= $(shell git describe --exact-match 2>/dev/null)
COMMIT = $(shell git rev-parse --short=7 HEAD)$(shell [[ $$(git status --porcelain) = "" ]] || echo -dirty)
ARO_HCP_FRONTEND_IMAGE_BASE = ${RP_IMAGE_ACR}.azurecr.io/aro

ifeq ($(TAG),)
	VERSION = $(COMMIT)
else
	VERSION = $(TAG)
endif

ARO_HCP_FRONTEND_IMAGE ?= $(FRONTEND_IMAGE_BASE):$(VERSION)

frontend:
	go build -ldflags "-X main.Version=$(VERSION)" -o aro-hcp-frontend .

image-frontend-multistage:
	docker build --platform=linux/amd64 --network=host --no-cache -f Dockerfile.aro-multistage -t $(ARO_HCP_FRONTEND_IMAGE) --build-arg REGISTRY=$(REGISTRY) .

clean:
	rm --force aro-hcp-frontend
