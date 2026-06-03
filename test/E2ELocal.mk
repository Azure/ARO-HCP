SHELL = /bin/bash
THIS := $(lastword $(MAKEFILE_LIST))
DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))

-include ../setup-templatize-env.mk
-include ../tooling/hcpctl/Variables.mk
include ../tooling/templatize/Makefile
include Makefile

DEPLOY_ENV ?= pers
CONFIG_FILE ?= ../config/config.yaml
DEV_SETTINGS_FILE ?= ../tooling/templatize/settings.yaml
ARO_HCP_CLOUD ?= dev
LOCAL_PORT ?= 8443
AROHCP_ENV ?= development
CUSTOMER_SUBSCRIPTION ?= $$(az account show --output tsv --query 'name')
ifndef E2E_ARTIFACT_DIR
  E2E_ARTIFACT_DIR := $(shell mktemp -d)
endif
SNAPSHOT_RENDERED_CONFIG := $(shell mktemp)

e2e-local/run-test: $(ARO_HCP_TESTS)
	$(MAKE) -C $(DIR) -f $(THIS) .e2e-local/setup
	export LOCATION="$${LOCATION:-${REGION}}"; \
	export AROHCP_ENV="development"; \
	export CUSTOMER_SUBSCRIPTION="$$(az account show --output tsv --query 'name')"; \
	export SKIP_CERT_VERIFICATION=$${SKIP_CERT_VERIFICATION:-false}; \
	export FRONTEND_ADDRESS=$${FRONTEND_ADDRESS:-http://localhost:8443}; \
	export ARTIFACT_DIR="$(E2E_ARTIFACT_DIR)"; \
	$(ARO_HCP_TESTS) run-test "$$TEST_NAME"
.PHONY: e2e-local/run-test

e2e-local/pf/run-test: $(HCPCTL)
	HCPCTL=$(HCPCTL) ../hack/run-with-port-forward.sh "${SVC_CLUSTER}" "aro-hcp/aro-hcp-frontend/$(LOCAL_PORT)/8443" \
		$(MAKE) -C $(DIR) -f $(THIS) e2e-local/run-test SKIP_CERT_VERIFICATION=true FRONTEND_ADDRESS=http://localhost:$(LOCAL_PORT)
.PHONY: e2e-local/pf/run-test

e2e-local/gather-snapshot: $(ARO_HCP_TESTS) $(TEMPLATIZE)
	$(TEMPLATIZE) configuration render \
	  --service-config-file $(CONFIG_FILE) \
	  --skip-schema-validation \
	  --cloud $(ARO_HCP_CLOUD) \
	  --environment $(DEPLOY_ENV) \
	  --dev-settings-file $(DEV_SETTINGS_FILE) \
	  --output "$(SNAPSHOT_RENDERED_CONFIG)"
	AZURE_TOKEN_CREDENTIALS=dev $(ARO_HCP_TESTS) gather-snapshot \
		--rendered-config $(SNAPSHOT_RENDERED_CONFIG) \
        --timing-input $(E2E_ARTIFACT_DIR)/test-timing \
        --output $(E2E_ARTIFACT_DIR)/snapshot
	@echo "Kusto snapshot artifacts written to $(E2E_ARTIFACT_DIR)/snapshot"
.PHONY: e2e-local/gather-snapshot

OBSERVABILITY_OUTPUT ?= $(shell mktemp -d)
OBSERVABILITY_RENDERED_CONFIG := $(shell mktemp)

e2e-local/gather-observability: $(ARO_HCP_TESTS) $(TEMPLATIZE)
	$(TEMPLATIZE) configuration render \
	  --service-config-file $(CONFIG_FILE) \
	  --skip-schema-validation \
	  --cloud $(ARO_HCP_CLOUD) \
	  --environment $(DEPLOY_ENV) \
	  --dev-settings-file $(DEV_SETTINGS_FILE) \
	  --output "$(OBSERVABILITY_RENDERED_CONFIG)"
	mkdir -p $(OBSERVABILITY_OUTPUT)
	AZURE_TOKEN_CREDENTIALS=dev $(ARO_HCP_TESTS) gather-observability \
		--rendered-config $(OBSERVABILITY_RENDERED_CONFIG) \
		--subscription-id "$$(az account show --query id -o tsv)" \
		--output $(OBSERVABILITY_OUTPUT) \
		--severity-threshold Sev3 \
		--start-time-fallback "$$(date -u -v-1H +%Y-%m-%dT%H:%M:%SZ)"
	@echo "Observability artifacts written to $(OBSERVABILITY_OUTPUT)"
.PHONY: e2e-local/gather-observability

.e2e-local/setup:
	SUBSCRIPTION_ID="$$(az account show --query id --output tsv)"; \
	TENANT_ID="$$(az account show --query tenantId --output tsv)"; \
	curl --silent --show-error --include \
		--insecure \
		--request PUT \
		--header "Content-Type: application/json" \
		--data '{"state":"Registered", "registrationDate": "now", "properties": { "tenantId": "'$${TENANT_ID}'", "registeredFeatures": [{"name": "Microsoft.RedHatOpenShift/ExperimentalReleaseFeatures", "state": "Registered"}]}}' \
		"http://localhost:${LOCAL_PORT}/subscriptions/$${SUBSCRIPTION_ID}?api-version=2.0"
.PHONY: .e2e-local/setup
