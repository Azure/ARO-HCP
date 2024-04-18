SHELL = /bin/bash
TAG ?= $(shell git describe --exact-match 2>/dev/null)
COMMIT = $(shell git rev-parse --short=7 HEAD)$(shell [[ $$(git status --porcelain) = "" ]] || echo -dirty)
# There is currently no ACR for ARO HCP components. Variable will be defined later
ARO_HCP_BASE_IMAGE ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io

ifeq ($(TAG),)
	VERSION = $(COMMIT)
else
	VERSION = $(TAG)
endif

ARO_HCP_FRONTEND_IMAGE ?= $(ARO_HCP_BASE_IMAGE)/arohcpfrontend:$(VERSION)

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

.PHONY: generate
generate:
	tsp compile ./api/redhatopenshift/HcpCluster --warn-as-error
	oav generate-examples ./api/redhatopenshift/resource-manager/Microsoft.RedHatOpenshift/preview/2024-06-10-preview/openapi.json --logLevel warn
	autorest api/autorest-config.yaml

.PHONY: frontend
frontend:
	go build -ldflags "-X main.Version=$(VERSION)" -o aro-hcp-frontend ./frontend

frontend-multistage:
	docker build --platform=linux/amd64 -f Dockerfile.frontend -t ${ARO_HCP_FRONTEND_IMAGE} --build-arg VERSION=$(VERSION) .

clean:
	rm aro-hcp-frontend

deploy-frontend:
	oc process -f ./deploy/aro-hcp-frontend.yml -p ARO_HCP_FRONTEND_IMAGE=${ARO_HCP_FRONTEND_IMAGE} | oc replace -f -

undeploy-frontend:
	oc process -f ./deploy/aro-hcp-frontend.yml -p ARO_HCP_FRONTEND_IMAGE=${ARO_HCP_FRONTEND_IMAGE} | oc delete -f -
