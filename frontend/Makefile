SHELL = /bin/bash
TAG ?= $(shell git describe --exact-match 2>/dev/null)
COMMIT = $(shell git rev-parse --short=7 HEAD)$(shell [[ $$(git status --porcelain) = "" ]] || echo -dirty)
REGISTRY = registry.access.redhat.com
## ARO_HCP_BASE_IMAGE = {} --> tbd

ifeq ($(TAG),)
	VERSION = $(COMMIT)
else
	VERSION = $(TAG)
endif

ARO_HCP_FRONTEND_IMAGE ?= $(ARO_HCP_BASE_IMAGE)/arohcpfrontend:$(VERSION)

frontend:
	go build -ldflags "-X main.Version=$(VERSION)" -o aro-hcp-frontend .

image-frontend-multistage:
	docker build --platform=linux/amd64 --network=host -f Dockerfile.frontend -t $(ARO_HCP_FRONTEND_IMAGE) --build-arg REGISTRY=$(REGISTRY) .

clean:
	rm --force aro-hcp-frontend
