SHELL = /bin/bash
COMMIT = $(shell git rev-parse --short=7 HEAD)$(shell [[ $$(git status --porcelain) = "" ]] || echo -dirty)
ARO_HCP_BASE_IMAGE ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io
ARO_HCP_FRONTEND_IMAGE ?= $(ARO_HCP_BASE_IMAGE)/arohcpfrontend:$(COMMIT)

all: test lint

# There is currently no convenient way to run tests against a whole Go workspace
# https://github.com/golang/go/issues/50745
test:
	go list -f '{{.Dir}}/...' -m | xargs go test -cover

# There is currently no convenient way to run golangci-lint against a whole Go workspace
# https://github.com/golang/go/issues/50745
MODULES := $(shell go list -f '{{.Dir}}/...' -m | xargs)
lint:
	golangci-lint run -v $(MODULES)

frontend-build-container:
	docker build --platform="linux/amd64" -f "frontend/Dockerfile" -t ${ARO_HCP_FRONTEND_IMAGE} .

frontend-deploy:
	oc process -f ./deploy/aro-hcp-frontend.yml -p ARO_HCP_FRONTEND_IMAGE=${ARO_HCP_FRONTEND_IMAGE} | oc apply -f -

frontend-undeploy:
	oc process -f ./deploy/aro-hcp-frontend.yml -p ARO_HCP_FRONTEND_IMAGE=${ARO_HCP_FRONTEND_IMAGE} | oc delete -f -

.PHONY: frontend-build frontend-build-container frontend-deploy frontend-undeploy test lint clean
