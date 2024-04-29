SHELL = /bin/bash
COMMIT = $(shell git rev-parse --short=7 HEAD)$(shell [[ $$(git status --porcelain) = "" ]] || echo -dirty)
# There is currently no ACR for ARO HCP components. Variable will be defined later
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

.PHONY: frontend
frontend:
	go build -o aro-hcp-frontend ./frontend

frontend-multistage:
	docker build --platform=linux/amd64 -f Dockerfile.frontend -t ${ARO_HCP_FRONTEND_IMAGE} .

clean:
	rm aro-hcp-frontend

deploy-frontend:
	oc process -f ./deploy/aro-hcp-frontend.yml -p ARO_HCP_FRONTEND_IMAGE=${ARO_HCP_FRONTEND_IMAGE} | oc apply -f -

undeploy-frontend:
	oc process -f ./deploy/aro-hcp-frontend.yml -p ARO_HCP_FRONTEND_IMAGE=${ARO_HCP_FRONTEND_IMAGE} | oc delete -f -
