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
	go list -f '{{.Dir}}/...' -m |RUN_TEMPLATIZE_E2E=true xargs go test -timeout 1200s -tags=$(GOTAGS) -cover
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
# Services
#

# Service Deployment Conventions:
#
# - Services are deployed in aks clusters (either svc or mgmt), which are
#   provisioned via infra section above
# - Makefile targets to deploy services ends with ".deploy" suffix
# - To deploy all services on svc or mgmt cluster, we have special targets
#   `svc.deployall` and `mgmt.deployall`, and `deployall` deploys everithing.
# - Placement of a service is controlled via services_svc and services_mgmt
#   variables
# - If the name of the service contains a dot, it's interpreted as directory
#   separator "/" (used for maestro only).

# Services deployed on "svc" aks cluster
services_svc = istio metrics maestro.server maestro.registration cluster-service backend
# Services deployed on "mgmt" aks cluster(s)
services_mgmt = acm maestro.agent pko hypershiftoperator
# List of all services
services_all = $(join services_svc,services_mgmt)

.PHONY: $(addsuffix .deploy, $(services_all)) deployall svc.deployall mgmt.deployall listall list clean

# Service deployment on either svc or mgmt aks cluster, a service name
# needs to be listed either in services_svc or services_mgmt variable (wich
# defines where it will be deployed).
%.deploy:
	$(eval export dirname=$(subst .,/,$(basename $@)))
	@if [ $(words $(filter $(basename $@), $(services_svc))) = 1 ]; then\
	    ./svc-deploy.sh $(DEPLOY_ENV) $(dirname) svc;\
	elif [ $(words $(filter $(basename $@), $(services_mgmt))) = 1 ]; then\
	    ./svc-deploy.sh $(DEPLOY_ENV) $(dirname) mgmt;\
	else\
	    echo "'$(basename $@)' is not to be deployed on neither svc nor mgmt cluster";\
	    exit 1;\
	fi


# Pipelines section
# This sections is used to reference pipeline runs and should replace 
# the usage of `svc-deploh.sh` script in the future.
services_svc_pipelines = frontend
%.deploy:
	$(eval export dirname=$(subst .,/,$(basename $@)))
	./templatize.sh $(DEPLOY_ENV) -p ./$(dirname)/pipeline.yaml -s deploy -P run -c public

services_svc_pipelines = frontend
%.dry_run:
	$(eval export dirname=$(subst .,/,$(basename $@)))
	./templatize.sh $(DEPLOY_ENV) -p ./$(dirname)/pipeline.yaml -s deploy -P run -c public -d

services_svc_all = $(join services_svc, services_svc_pipelines)

svc.deployall:  $(addsuffix .deploy, $(services_svc_all))
mgmt.deployall: $(addsuffix .deploy, $(services_mgmt))
deployall: svc.deployall mgmt.deployall

listall:
	@echo svc: ${services_svc}
	@echo mgmt: ${services_mgmt}

list:
	@grep '^[^#[:space:]].*:' Makefile
