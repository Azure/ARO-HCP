include ./.bingo/Variables.mk
include ./.bingo/Symlinks.mk
include ./tooling/templatize/Makefile
include ./tooling/yamlwrap/Makefile
include ./test/Makefile
SHELL = /bin/bash
PATH := $(GOBIN):$(PATH)

LINT_GOTAGS?='E2Etests'
TOOLS_BIN_DIR := tooling/bin
DEPLOY_ENV ?= pers
CONFIG_FILE ?= config/config.yaml

.DEFAULT_GOAL := all

# There is currently no convenient way to run commands against a whole Go workspace
# https://github.com/golang/go/issues/50745
MODULES := $(shell go list -f '{{.Dir}}/...' -m | xargs)

all: test lint
.PHONY: all

# There is currently no convenient way to run tests against a whole Go workspace
# https://github.com/golang/go/issues/50745
test:
	go list -f '{{.Dir}}/...' -m |RUN_TEMPLATIZE_E2E=true xargs go test -timeout 1200s -cover
.PHONY: test

test-unit:
	go list -f '{{.Dir}}/...' -m | xargs go test -timeout 1200s -cover
.PHONY: test-unit

test-compile:
	go list -f '{{.Dir}}/...' -m |xargs go test -c -o /dev/null
.PHONY: test-compile

generate: deepcopy mocks fmt record-nonlocal-e2e all-tidy

verify-generate: generate
	./hack/verify.sh generate
.PHONY: verify-generate

deepcopy: $(DEEPCOPY_GEN) $(GOIMPORTS)
	DEEPCOPY_GEN=$(DEEPCOPY_GEN) hack/update-deepcopy.sh
	$(GOIMPORTS) -w -local github.com/Azure/ARO-HCP internal/api/zz_generated.deepcopy.go internal/api/arm/zz_generated.deepcopy.go
	$(MAKE) all-tidy
.PHONY: deepcopy

verify-deepcopy: deepcopy
	./hack/verify.sh deepcopy
.PHONY: verify-deepcopy

verify: verify-deepcopy
.PHONY: verify

verify-yamlfmt: yamlfmt
	./hack/verify.sh yamlfmt
.PHONY: verify-yamlfmt

mocks: $(MOCKGEN) $(GOIMPORTS)
	MOCKGEN=${MOCKGEN} go generate -run '\$$MOCKGEN\b' $(MODULES)
	$(GOIMPORTS) -w -local github.com/Azure/ARO-HCP $$(find . -name "mock_*.go" -not -path "./.git/*" -not -path "./.bingo/*")
.PHONY: mocks

install-tools: $(BINGO) $(HELM_LINK) $(YQ_LINK) $(JQ_LINK) $(ORAS_LINK)
	$(BINGO) get
.PHONY: install-tools

licenses: $(ADDLICENSE)
	$(shell find . -type f -name '*.go' | xargs -I {} $(ADDLICENSE) -c 'Microsoft Corporation' -l apache {})

lint: $(GOLANGCI_LINT)
	$(GOLANGCI_LINT) run -v --build-tags=$(LINT_GOTAGS) $(MODULES)
.PHONY: lint

lint-fix: $(GOLANGCI_LINT)
	$(GOLANGCI_LINT) run -v --build-tags=$(LINT_GOTAGS) $(MODULES) --fix
.PHONY: lint-fix

fmt: $(GOIMPORTS)
	$(GOIMPORTS) -w -local github.com/Azure/ARO-HCP $(shell go list -f '{{.Dir}}' -m | xargs)
.PHONY: fmt

yamlfmt: $(YAMLFMT) $(YAMLWRAP)
	# first, wrap all templated values in quotes, so they are correct YAML
	$(YAMLWRAP) wrap --dir . --no-validate-result
	# run the formatter
	$(YAMLFMT) -dstar -exclude './api/**' '**/*.{yaml,yml}'
	# "fix" any non-string fields we cast to strings for the formatting
	$(YAMLWRAP) unwrap --dir .
.PHONY: yamlfmt

tidy: $(MODULES:/...=.tidy)

%.tidy:
	cd $(basename $@) && go mod tidy

all-tidy: tidy fmt licenses
	go work sync

frontend-grant-ingress:
	make -C dev-infrastructure frontend-grant-ingress
.PHONY: frontend-grant-ingress

record-nonlocal-e2e: $(GOJQ)
	go run github.com/onsi/ginkgo/v2/ginkgo run \
		--no-color --tags E2Etests --label-filter='!ARO-HCP-RP-API-Compatible' --dry-run --output-dir=test/e2e --json-report=report.json test/e2e && \
		$(GOJQ) '[.[] | .SpecReports[]? | select(.State == "passed") | .LeafNodeText] | sort' test/e2e/report.json > ./nonlocal-e2e-specs.txt
.PHONY: record-nonlocal-e2e

e2e/local: e2e-local/setup
	$(MAKE) e2e-local/run 
.PHONY: e2e/local

e2e-local/setup:
	@SUBSCRIPTION_ID="$$(az account show --query id --output tsv)"; \
	TENANT_ID="$$(az account show --query tenantId --output tsv)"; \
	ADDRESS="$${FRONTEND_ADDRESS:-http://localhost:8443}"; \
	curl --silent --show-error --include \
		--insecure \
		--request PUT \
		--header "Content-Type: application/json" \
		--data '{"state":"Registered", "registrationDate": "now", "properties": { "tenantId": "'$${TENANT_ID}'"}}' \
		"$${ADDRESS}/subscriptions/$${SUBSCRIPTION_ID}?api-version=2.0"
.PHONY: e2e-local/setup

e2e-local/run: $(ARO_HCP_TESTS)
	export LOCATION="westus3"; \
	export AROHCP_ENV="development"; \
	export CUSTOMER_SUBSCRIPTION="$$(az account show --output tsv --query 'name')"; \
	export ARTIFACT_DIR=$${ARTIFACT_DIR:-_artifacts}; \
	export JUNIT_PATH=$${JUNIT_PATH:-$$ARTIFACT_DIR/junit.xml}; \
	export HTML_PATH=$${HTML_PATH:-$$ARTIFACT_DIR/extension-test-result-summary.html}; \
	export SKIP_CERT_VERIFICATION=$${SKIP_CERT_VERIFICATION:-false}; \
	export FRONTEND_ADDRESS=$${FRONTEND_ADDRESS:-http://localhost:8443}; \
	export ADMIN_API_ADDRESS=$${ADMIN_API_ADDRESS:-http://localhost:8444}; \
	mkdir -p "$$ARTIFACT_DIR"; \
	$(ARO_HCP_TESTS) run-suite "rp-api-compat-all/parallel" --junit-path="$$JUNIT_PATH" --html-path="$$HTML_PATH" --max-concurrency 100
.PHONY: e2e-local/run

CONTAINER_RUNTIME ?= docker

mega-lint:
	$(CONTAINER_RUNTIME) run --rm \
		-e FILTER_REGEX_EXCLUDE='hypershiftoperator/deploy/crds/|maestro/server/deploy/templates/allow-cluster-service.authorizationpolicy.yaml|acm/deploy/helm/multicluster-engine-config/charts/policy/charts|dev-infrastructure/global-pipeline.yaml|tooling/templatize/testdata/pipeline.yaml|hypershiftoperator/deploy/templates/cluster.clustersizingconfiguration.yaml' \
		-e REPORT_OUTPUT_FOLDER=/tmp/report \
		-v $${PWD}:/tmp/lint:Z \
		docker.io/oxsecurity/megalinter-ci_light:v9
.PHONY: mega-lint

#
# Infra
#

infra.region:
	@cd dev-infrastructure && DEPLOY_ENV=$(DEPLOY_ENV) make region
.PHONY: infra.region

infra.svc:
	@cd dev-infrastructure && DEPLOY_ENV=$(DEPLOY_ENV) make svc.init
.PHONY: infra.svc

infra.svc.aks.kubeconfig:
	@cd dev-infrastructure && DEPLOY_ENV=$(DEPLOY_ENV) make -s svc.aks.kubeconfig
.PHONY: infra.svc.aks.kubeconfig

infra.svc.aks.kubeconfigfile:
	@cd dev-infrastructure && DEPLOY_ENV=$(DEPLOY_ENV) make -s svc.aks.kubeconfigfile
.PHONY: infra.svc.aks.kubeconfigfile

infra.mgmt:
	@cd dev-infrastructure && DEPLOY_ENV=$(DEPLOY_ENV) make mgmt.init
.PHONY: infra.mgmt

infra.mgmt.solo:
	@cd dev-infrastructure && DEPLOY_ENV=$(DEPLOY_ENV) make mgmt.solo.init
.PHONY: infra.mgmt.solo

infra.mgmt.aks.kubeconfig:
	@cd dev-infrastructure && DEPLOY_ENV=$(DEPLOY_ENV) make -s mgmt.aks.kubeconfig
.PHONY: infra.mgmt.aks.kubeconfig

infra.mgmt.aks.kubeconfigfile:
	@cd dev-infrastructure && DEPLOY_ENV=$(DEPLOY_ENV) make -s mgmt.aks.kubeconfigfile
.PHONY: infra.mgmt.aks.kubeconfigfile

infra.kusto:
	@cd dev-infrastructure && DEPLOY_ENV=$(DEPLOY_ENV) make kusto
.PHONY: infra.kusto

infra.monitoring:
	@cd dev-infrastructure && DEPLOY_ENV=$(DEPLOY_ENV) make monitoring
.PHONY: infra.monitoring

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

infra.clean:
	@cd dev-infrastructure && DEPLOY_ENV=$(DEPLOY_ENV) make clean
.PHONY: infra.clean

infra.tracing:
	cd observability/tracing && KUBECONFIG="$$(cd ../../dev-infrastructure && make -s svc.aks.kubeconfigfile)" make
.PHONY: infra.tracing

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
services_svc =
# Services deployed on "mgmt" aks cluster(s)
services_mgmt =
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
# the usage of `svc-deploy.sh` script in the future.
services_svc_pipelines = backend frontend cluster-service maestro.server observability.tracing
services_mgmt_pipelines = secret-sync-controller acm hypershiftoperator maestro.agent observability.tracing route-monitor-operator
%.deploy_pipeline: $(ORAS_LINK) $(YQ)
	$(eval export dirname=$(subst .,/,$(basename $@)))
	./templatize.sh $(DEPLOY_ENV) -p $(shell $(YQ) .serviceGroup ./$(dirname)/pipeline.yaml) -P run

%.dry_run: $(ORAS_LINK) $(YQ)
	$(eval export dirname=$(subst .,/,$(basename $@)))
	./templatize.sh $(DEPLOY_ENV) -p $(shell $(YQ) .serviceGroup ./$(dirname)/pipeline.yaml) -P run -d

svc.deployall: $(ORAS_LINK) $(addsuffix .deploy_pipeline, $(services_svc_pipelines)) $(addsuffix .deploy, $(services_svc))
mgmt.deployall: $(ORAS_LINK) $(addsuffix .deploy, $(services_mgmt)) $(addsuffix .deploy_pipeline, $(services_mgmt_pipelines))
deployall: $(ORAS_LINK) svc.deployall mgmt.deployall

listall:
	@echo svc: ${services_svc}
	@echo mgmt: ${services_mgmt}

list:
	@grep '^[^#[:space:]].*:' Makefile

rebase:
	hack/rebase-n-materialize.sh
.PHONY: rebase

validate-config-pipelines: $(YQ)
	$(MAKE) -C tooling/templatize templatize
	tooling/templatize/templatize pipeline validate --topology-config-file topology.yaml --service-config-file "$(CONFIG_FILE)" --dev-mode --dev-region $(shell $(YQ) '.environments[] | select(.name == "dev") | .defaults.region' <tooling/templatize/settings.yaml) $(ONLY_CHANGED)

validate-changed-config-pipelines:
	$(MAKE) validate-config-pipelines DEV_MODE="--dev-mode --dev-region uksouth" ONLY_CHANGED="--only-changed"

validate-config:
	$(MAKE) -C config/ validate

ARO-Tools:
	pushd tooling/templatize/; GOPROXY=direct go get github.com/Azure/ARO-Tools@main; popd; go work sync && make all-tidy
.PHONY: ARO-Tools

update-helm-fixtures:
	find * -name 'zz_fixture_TestHelmTemplate*' | xargs rm -rf
	$(MAKE) -C tooling/helmtest update
.PHONY: update-helm-fixtures

test-helm-fixtures:
	$(MAKE) -C tooling/helmtest test
.PHONY: test-helmcharts

#
# Generated SDKs
#
generate-kiota:
	@tooling/kiota/generate.sh
	$(MAKE) licenses
	$(MAKE) fmt
.PHONY: generate-kiota

#
# One-Step Personal Dev Environment
#
ifeq ($(DEPLOY_ENV),$(filter $(DEPLOY_ENV),pers swft))
personal-dev-env: install-tools entrypoint/Region infra.svc.aks.kubeconfig infra.mgmt.aks.kubeconfig infra.tracing
else
personal-dev-env:
	$(error personal-dev-env: DEPLOY_ENV must be set to "pers" or "swft", not "$(DEPLOY_ENV)")
endif
.PHONY: personal-dev-env

#
# Local Cluster Service Development Environment
#
ifeq ($(DEPLOY_ENV),$(filter $(DEPLOY_ENV),pers swft))
local-pers-dev-env: personal-dev-env
	@echo ""
	@echo "===================================================================="
	@echo "Personal dev environment setup complete"
	@echo "===================================================================="
	@echo ""
	@echo "Granting local development permissions..."
	@$(MAKE) -C dev-infrastructure local-cs-permissions
	@echo ""
	@echo "===================================================================="
	@echo "Local CS permissions granted successfully"
	@echo "===================================================================="
	@echo "Generating local provision-shard config..."
	@echo ""
	@cd cluster-service && $(MAKE) local-deploy-provision-shard && $(MAKE) personal-runtime-config && $(MAKE) local-aro-hcp-ocp-versions-config && $(MAKE) local-azure-operators-managed-identities-config
	@echo ""
	@echo "===================================================================="
	@echo "Cluster service configuration files generated at:"
	@echo "cluster-service/local/"
	@echo "===================================================================="
else
local-pers-dev-env:
	$(error local-pers-dev-env: DEPLOY_ENV must be set to "pers" or "swft", not "$(DEPLOY_ENV)")
endif
.PHONY: local-pers-dev-env

ifeq ($(wildcard $(YQ)),$(YQ))
entrypoints = $(shell $(YQ) '.entrypoints[] | .identifier | sub("Microsoft.Azure.ARO.HCP.", "")' topology.yaml )
pipelines = $(shell $(YQ) '.services[] | .. | select(key == "serviceGroup") | sub("Microsoft.Azure.ARO.HCP.", "")' topology.yaml )
endif

ifeq ($(wildcard $(YQ)),$(YQ))
$(addprefix entrypoint/,$(entrypoints)):
endif
entrypoint/%:
	$(MAKE) local-run WHAT="--entrypoint Microsoft.Azure.ARO.HCP.$(notdir $@)"

ifeq ($(wildcard $(YQ)),$(YQ))
$(addprefix pipeline/,$(pipelines)):
endif
pipeline/%:
	$(MAKE) local-run WHAT="--service-group Microsoft.Azure.ARO.HCP.$(notdir $@)"

LOG_LEVEL ?= 3
DRY_RUN ?= "false"
PERSIST ?= "false"
TIMING_OUTPUT ?= timing/steps.yaml
ENTRYPOINT_JUNIT_OUTPUT ?= _artifacts/junit_entrypoint.xml
CONFIG_OUTPUT ?= _artifacts/config.yaml

local-run: $(TEMPLATIZE)
	$(TEMPLATIZE) entrypoint run --config-file "${CONFIG_FILE}" \
								     --config-file-override "${OVERRIDE_CONFIG_FILE}" \
	                                 --topology-config topology.yaml \
	                                 --dev-settings-file tooling/templatize/settings.yaml \
	                                 --dev-environment $(DEPLOY_ENV) \
	                                 $(WHAT) $(EXTRA_ARGS) \
	                                 --dry-run=$(DRY_RUN) \
	                                 --verbosity=$(LOG_LEVEL) \
	                                 --timing-output=$(TIMING_OUTPUT) \
	                                 --junit-output=$(ENTRYPOINT_JUNIT_OUTPUT) \
	                                 --config-output=$(CONFIG_OUTPUT)


ifeq ($(wildcard $(YQ)),$(YQ))
$(addprefix graph/entrypoint/,$(entrypoints)):
endif
graph/entrypoint/%:
	$(MAKE) graph WHAT="--entrypoint Microsoft.Azure.ARO.HCP.$(notdir $@)"

ifeq ($(wildcard $(YQ)),$(YQ))
$(addprefix graph/pipeline/,$(pipelines)):
endif
graph/pipeline/%:
	$(MAKE) graph WHAT="--service-group Microsoft.Azure.ARO.HCP.$(notdir $@)"

graph: $(TEMPLATIZE)
	$(TEMPLATIZE) entrypoint graph --config-file "${CONFIG_FILE}" \
	                               --topology-config topology.yaml \
	                               --dev-settings-file tooling/templatize/settings.yaml \
	                               --dev-environment $(DEPLOY_ENV) \
	                               $(WHAT) \
	                               --output-dot .graph.dot \
	                               --output-html .graph.html

VISUALIZATION_OUTPUT ?= timing/

visualize: $(TEMPLATIZE)
	$(TEMPLATIZE) entrypoint visualize --timing-input $(TIMING_OUTPUT) --output $(VISUALIZATION_OUTPUT)

ifeq ($(wildcard $(YQ)),$(YQ))
$(addprefix cleanup-entrypoint/,$(entrypoints)):
endif
cleanup-entrypoint/%:
	$(MAKE) cleanup WHAT="--entrypoint Microsoft.Azure.ARO.HCP.$(notdir $@)"

ifeq ($(wildcard $(YQ)),$(YQ))
$(addprefix cleanup-pipeline/,$(pipelines)):
endif
cleanup-pipeline/%:
	$(MAKE) cleanup WHAT="--service-group Microsoft.Azure.ARO.HCP.$(notdir $@)"

CLEANUP_DRY_RUN ?= true
CLEANUP_WAIT ?= true

cleanup: $(TEMPLATIZE)
	$(TEMPLATIZE) entrypoint cleanup --config-file "${CONFIG_FILE}" \
								     --config-file-override "${OVERRIDE_CONFIG_FILE}" \
								     --topology-config topology.yaml \
								     --dev-settings-file tooling/templatize/settings.yaml \
								     --dev-environment $(DEPLOY_ENV) \
								     $(WHAT) \
								     --dry-run=$(CLEANUP_DRY_RUN) \
									 --ignore=global --ignore=kusto \
								     --wait=$(CLEANUP_WAIT) \
								     --verbosity=$(LOG_LEVEL)

# Image Updater
image-updater:
	@$(MAKE) -C tooling/image-updater update
.PHONY: image-updater