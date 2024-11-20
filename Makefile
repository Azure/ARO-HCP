include ./.bingo/Variables.mk
SHELL = /bin/bash

# This build tag is currently leveraged by tooling/image-sync
# https://github.com/containers/image?tab=readme-ov-file#building
GOTAGS?='containers_image_openpgp'
TOOLS_BIN_DIR := tooling/bin
DEPLOY_ENV ?= personal-dev

.DEFAULT_GOAL := all

all: test lint
.PHONY: all

# There is currently no convenient way to run tests against a whole Go workspace
# https://github.com/golang/go/issues/50745
test:
	go list -f '{{.Dir}}/...' -m | xargs go test -tags=$(GOTAGS) -cover
.PHONY: test

# There is currently no convenient way to run golangci-lint against a whole Go workspace
# https://github.com/golang/go/issues/50745
MODULES := $(shell go list -f '{{.Dir}}/...' -m | xargs)
lint: $(GOLANGCI_LINT)
	$(GOLANGCI_LINT) run -v --build-tags=$(GOTAGS) $(MODULES)
.PHONY: lint

fmt: $(GOIMPORTS)
	$(GOIMPORTS) -w -local github.com/Azure/ARO-HCP $(shell go list -f '{{.Dir}}' -m | xargs)
.PHONY: fmt

#
# Infra
#

infra.region:
	@cd dev-infrastructure && DEPLOY_ENV=$(DEPLOY_ENV) make region
.PHONY: infra.region

infra.svc:
	@cd dev-infrastructure && DEPLOY_ENV=$(DEPLOY_ENV) make svc.init
.PHONY: infra.svc

infra.svc.aks.kubeconfigfile:
	@cd dev-infrastructure && DEPLOY_ENV=$(DEPLOY_ENV) make -s svc.aks.kubeconfigfile
.PHONY: infra.svc.aks.kubeconfigfile

infra.mgmt:
	@cd dev-infrastructure && DEPLOY_ENV=$(DEPLOY_ENV) make mgmt.init
.PHONY: infra.mgmt

infra.mgmt.aks.kubeconfigfile:
	@cd dev-infrastructure && DEPLOY_ENV=$(DEPLOY_ENV) make -s mgmt.aks.kubeconfigfile
.PHONY: infra.mgmt.aks.kubeconfigfile

infra.imagesync:
	@cd dev-infrastructure && DEPLOY_ENV=$(DEPLOY_ENV) make imagesync
.PHONY: infra.imagesync

infra.all:
	@cd dev-infrastructure && DEPLOY_ENV=$(DEPLOY_ENV) make infra
.PHONY: infra.all

infra.svc.clean:
	@cd dev-infrastructure && DEPLOY_ENV=$(DEPLOY_ENV) make svc.clean
.PHONY: infra.svc.clean

infra.mgmt.clean:
	@cd dev-infrastructure && DEPLOY_ENV=$(DEPLOY_ENV) make mgmt.clean
.PHONY: infra.mgmt.clean

infra.region.clean:
	@cd dev-infrastructure && DEPLOY_ENV=$(DEPLOY_ENV) make region.clean
.PHONY: infra.region.clean

infra.imagesync.clean:
	@cd dev-infrastructure && DEPLOY_ENV=$(DEPLOY_ENV) make imagesync.clean
.PHONY: infra.imagesync.clean

infra.clean:
	@cd dev-infrastructure && DEPLOY_ENV=$(DEPLOY_ENV) make clean
.PHONY: infra.clean

#
# Istio
#

isto.deploy:
	@./svc-deploy.sh $(DEPLOY_ENV) istio svc
.PHONY: isto.deploy

#
# Metrics
#

metrics.deploy:
	@./svc-deploy.sh $(DEPLOY_ENV) metrics svc
.PHONY: metrics.deploy

#
# Cluster Service
#

cs.deploy:
	@./svc-deploy.sh $(DEPLOY_ENV) cluster-service svc
.PHONY: cs.deploy

#
# Maestro
#

maestro.server.deploy:
	@./svc-deploy.sh $(DEPLOY_ENV) maestro/server svc
.PHONY: maestro.server.deploy

maestro.agent.deploy:
	@./svc-deploy.sh $(DEPLOY_ENV) maestro/agent mgmt
.PHONY: maestro.agent.deploy

maestro.registration.deploy:
	@./svc-deploy.sh $(DEPLOY_ENV) maestro/registration svc
.PHONY: maestro.registration.deploy

maestro: maestro.server.deploy maestro.agent.deploy maestro.registration.deploy
.PHONY: maestro

#
# Resource Provider
#

rp.frontend.deploy:
	@./svc-deploy.sh $(DEPLOY_ENV) frontend svc
.PHONY: rp.frontend.deploy

rp.backend.deploy:
	@./svc-deploy.sh $(DEPLOY_ENV) backend svc
.PHONY: rp.backend.deploy

#
# PKO
#

pko.deploy:
	@./svc-deploy.sh $(DEPLOY_ENV) pko mgmt
.PHONY: pko.deploy

#
# ACM
#

acm.deploy:
	@./svc-deploy.sh $(DEPLOY_ENV) acm mgmt
.PHONY: acm.deploy

#
# Hypershift
#

hypershift.deploy:
	@./svc-deploy.sh $(DEPLOY_ENV) hypershiftoperator mgmt
.PHONY: hypershift.deploy

#
# Deploy ALL components
#

deploy.svc.all: isto.deploy metrics.deploy maestro.server.deploy maestro.registration.deploy cs.deploy rp.frontend.deploy rp.backend.deploy
.PHONY: deploy.svc.all

deploy.mgmt.all: acm.deploy maestro.agent.deploy hypershift.deploy
.PHONY: deploy.mgmt.all

deploy.all: deploy.svc.all deploy.mgmt.all
.PHONY: deploy.all

list:
	@grep '^[^#[:space:]].*:' Makefile
.PHONY: list
