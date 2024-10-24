include ./.bingo/Variables.mk
SHELL = /bin/bash

# This build tag is currently leveraged by tooling/image-sync
# https://github.com/containers/image?tab=readme-ov-file#building
GOTAGS?='containers_image_openpgp'
TOOLS_BIN_DIR := tooling/bin
DEPLOY_ENV ?= personal-dev

.DEFAULT_GOAL := all

all: test lint

# There is currently no convenient way to run tests against a whole Go workspace
# https://github.com/golang/go/issues/50745
test:
	go list -f '{{.Dir}}/...' -m | xargs go test -tags=$(GOTAGS) -cover

# There is currently no convenient way to run golangci-lint against a whole Go workspace
# https://github.com/golang/go/issues/50745
MODULES := $(shell go list -f '{{.Dir}}/...' -m | xargs)
lint: $(GOLANGCI_LINT)
	$(GOLANGCI_LINT) run -v --build-tags=$(GOTAGS) $(MODULES)

fmt: $(GOIMPORTS)
	$(GOIMPORTS) -w -local github.com/Azure/ARO-HCP $(shell go list -f '{{.Dir}}' -m | xargs)

#
# Infra
#

infra.svc:
	dev-infrastructure/make $(DEPLOY_ENV) svc.init

infra.mgmt:
	dev-infrastructure/make $(DEPLOY_ENV) mgmt.init

infra.imagesync:
	dev-infrastructure/make $(DEPLOY_ENV) imagesync

infra:
	dev-infrastructure/make $(DEPLOY_ENV) infra

#
# Cluster Service
#

cs.deploy:
	cluster-service/rollout $(DEPLOY_ENV)

#
# Maestro
#

maestro.server.deploy:
	maestro/server/rollout $(DEPLOY_ENV)

maestro.agent.deploy:
	maestro/agent/rollout $(DEPLOY_ENV)

maestro.registration.deploy:
	maestro/registration/rollout $(DEPLOY_ENV)

maestro: maestro.server.deploy maestro.agent.deploy maestro.registration.deploy

.PHONY: all clean lint test fmt maestro.server.deploy maestro.agent.deploy maestro.registration.deploy maestro infra.svc infra.mgmt infra.imagesync infra
