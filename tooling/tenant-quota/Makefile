TENANT_QUOTA_COLLECTOR_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))

DEPLOY_ENV ?= opstool

-include $(TENANT_QUOTA_COLLECTOR_DIR)/../../setup-templatize-env.mk
-include $(TENANT_QUOTA_COLLECTOR_DIR)/../../.bingo/Variables.mk

# Opstool uses its own config file, not the main config.yaml
OPSTOOL_CONFIG_FILE := $(TENANT_QUOTA_COLLECTOR_DIR)/../../config/config-opstool.yaml
DEV_SETTINGS_FILE := $(TENANT_QUOTA_COLLECTOR_DIR)/../../tooling/templatize/settings.yaml

ARO_HCP_REVISION ?= $(shell git rev-parse HEAD)
ARO_HCP_IMAGE_ACR ?= arohcpsvcdev
ARO_HCP_IMAGE_REGISTRY ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io
TENANT_QUOTA_COLLECTOR_IMAGE_REPOSITORY ?= tenant-quota-collector

# Tag defaults to 'latest' for opstool dev environment (matches config-opstool.yaml)
TENANT_QUOTA_IMAGE_REPOSITORY ?= $(shell DEPLOY_ENV=${DEPLOY_ENV} BASELINE_REPO=${TENANT_QUOTA_COLLECTOR_IMAGE_REPOSITORY} $(TENANT_QUOTA_COLLECTOR_DIR)/../../generate-repo.sh)
TENANT_QUOTA_IMAGE_TAG ?= latest
TENANT_QUOTA_TAGGED_IMAGE ?= $(ARO_HCP_IMAGE_REGISTRY)/$(TENANT_QUOTA_IMAGE_REPOSITORY):$(TENANT_QUOTA_IMAGE_TAG)

ifeq ($(ARO_HCP_IMAGE_REGISTRY:%.azurecr.io=azurecr.io),azurecr.io)
ARO_HCP_IMAGE_REGISTRY_IS_ACR = 1
endif

.DEFAULT_GOAL := tenant-quota-collector

tenant-quota-collector:
	go build -ldflags="-X github.com/Azure/ARO-HCP/internal/version.CommitSHA=${TENANT_QUOTA_IMAGE_TAG}" -o tenant-quota-collector .

# ---------------------------------------------------------------------------
# Local config rendering
# ---------------------------------------------------------------------------
# Renders config-opstool.yaml through templatize to produce the config.yaml
# that the tool expects. This is the same content that ends up in the
# ConfigMap data["config.yaml"] during a real deployment.
_output/config.yaml: $(TEMPLATIZE) deploy/config.yaml.tmpl $(OPSTOOL_CONFIG_FILE)
	$(TEMPLATIZE) generate \
		--config-file $(OPSTOOL_CONFIG_FILE) \
		--dev-settings-file $(DEV_SETTINGS_FILE) \
		--dev-environment $(DEPLOY_ENV) \
		--input deploy/config.yaml.tmpl \
		--output $@

render-config: _output/config.yaml
.PHONY: render-config

# ---------------------------------------------------------------------------
# Fetch secrets from Key Vault for local development
# ---------------------------------------------------------------------------
# Downloads SP client secrets from the opstool Key Vault into _output/secrets/.
# Requires: az login (into the dev tenant where the Key Vault lives)
KEYVAULT_NAME ?= opstool-kv-usw3
SECRETS_DIR := _output/secrets

fetch-secrets: _output/config.yaml
	@mkdir -p $(SECRETS_DIR)
	@for secret_name in $$(grep 'keyVaultSecretName:' _output/config.yaml | sed 's/.*keyVaultSecretName: *"\{0,1\}\([^"]*\)"\{0,1\}/\1/'); do \
		echo "Fetching secret: $$secret_name"; \
		az keyvault secret show \
			--vault-name $(KEYVAULT_NAME) \
			--name "$$secret_name" \
			--query value -o tsv > $(SECRETS_DIR)/$$secret_name || \
			{ echo "ERROR: Failed to fetch $$secret_name"; exit 1; }; \
	done
	@echo "Secrets stored in $(SECRETS_DIR)/"
.PHONY: fetch-secrets

# ---------------------------------------------------------------------------
# Run locally
# ---------------------------------------------------------------------------
run: _output/config.yaml fetch-secrets
	SECRETS_STORE_PATH=$(SECRETS_DIR) CONFIG_PATH=_output/config.yaml go run main.go

run-image: image _output/config.yaml fetch-secrets
	docker run --rm -p 8080:8080 \
		-e CONFIG_PATH=/config.yaml \
		-e SECRETS_STORE_PATH=/secrets \
		-v "$(abspath _output/config.yaml):/config.yaml:ro" \
		-v "$(abspath $(SECRETS_DIR)):/secrets:ro" \
		${TENANT_QUOTA_TAGGED_IMAGE}
.PHONY: run-image

clean:
	rm -rf tenant-quota-collector _output/
.PHONY: clean

image: Dockerfile $(shell find . -name '*.go' -o -name 'go.mod' -o -name 'go.sum') Makefile
	cd $(TENANT_QUOTA_COLLECTOR_DIR)/../.. && \
	docker build . --file tooling/tenant-quota/Dockerfile \
	               --build-arg PLATFORM=linux/amd64 \
	               --build-arg REVISION=${ARO_HCP_REVISION} \
	               --build-arg TAG=${TENANT_QUOTA_IMAGE_TAG} \
	               --tag ${TENANT_QUOTA_TAGGED_IMAGE}
.PHONY: image

build-and-push: image
	@TOKEN=$$(az acr login --name ${ARO_HCP_IMAGE_ACR} --expose-token --query accessToken -o tsv) && \
	$(CONTAINER_ENGINE) login ${ARO_HCP_IMAGE_REGISTRY} -u 00000000-0000-0000-0000-000000000000 -p "$$TOKEN"
	$(CONTAINER_ENGINE) push ${TENANT_QUOTA_TAGGED_IMAGE}
	@echo ""
	@echo "Pushed: ${TENANT_QUOTA_TAGGED_IMAGE}"
.PHONY: build-and-push

show-image:
	@echo "Image: ${TENANT_QUOTA_TAGGED_IMAGE}"
	@echo "Repository: ${TENANT_QUOTA_IMAGE_REPOSITORY}"
	@echo "Tag: ${TENANT_QUOTA_IMAGE_TAG}"
.PHONY: show-image
