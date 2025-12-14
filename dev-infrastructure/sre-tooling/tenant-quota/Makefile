CUSTOM_METRICS_COLLECTOR_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))

-include $(CUSTOM_METRICS_COLLECTOR_DIR)/../../setup-templatize-env.mk
-include $(CUSTOM_METRICS_COLLECTOR_DIR)/../../.bingo/Variables.mk

ARO_HCP_REVISION = $(shell git rev-parse HEAD)
ARO_HCP_IMAGE_TAG ?= $(shell DEPLOY_ENV=${DEPLOY_ENV} $(CUSTOM_METRICS_COLLECTOR_DIR)/../../generate-tag.sh)
ARO_HCP_IMAGE_REGISTRY ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io
ARO_HCP_IMAGE_REPOSITORY ?= ${CUSTOM_METRICS_COLLECTOR_IMAGE_REPOSITORY}
CUSTOM_METRICS_COLLECTOR_GENERATED_IMAGE_REPOSITORY = $(shell DEPLOY_ENV=${DEPLOY_ENV} BASELINE_REPO=${ARO_HCP_IMAGE_REPOSITORY} $(CUSTOM_METRICS_COLLECTOR_DIR)/../../generate-repo.sh)
CUSTOM_METRICS_COLLECTOR_TAGGED_IMAGE ?= $(ARO_HCP_IMAGE_REGISTRY)/$(CUSTOM_METRICS_COLLECTOR_GENERATED_IMAGE_REPOSITORY):$(ARO_HCP_IMAGE_TAG)

ifeq ($(ARO_HCP_IMAGE_REGISTRY:%.azurecr.io=azurecr.io),azurecr.io)
ARO_HCP_IMAGE_REGISTRY_IS_ACR = 1
endif

.DEFAULT_GOAL := custom-metrics-collector

custom-metrics-collector:
	go build -ldflags="-X github.com/Azure/ARO-HCP/internal/version.CommitSHA=${ARO_HCP_IMAGE_TAG}" -o custom-metrics-collector .

run:
	CONFIG_PATH=./deploy/templates/configmap.yaml ./custom-metrics-collector

clean:
	rm -f custom-metrics-collector

image: Dockerfile $(shell find . -name '*.go' -o -name 'go.mod' -o -name 'go.sum') Makefile
	cd $(CUSTOM_METRICS_COLLECTOR_DIR)/../.. && \
	docker build . --file dev-infrastructure/sre-tooling/tenant-quota/Dockerfile \
	               --build-arg PLATFORM=linux/amd64 \
	               --build-arg REVISION=${ARO_HCP_REVISION} \
	               --build-arg TAG=${ARO_HCP_IMAGE_TAG} \
	               --tag ${CUSTOM_METRICS_COLLECTOR_TAGGED_IMAGE}
.PHONY: image

build-and-push: image $(ORAS)
	az acr login --name ${ARO_HCP_IMAGE_ACR}
	docker push ${CUSTOM_METRICS_COLLECTOR_TAGGED_IMAGE}
.PHONY: build-and-push

OVERRIDE_CONFIG_FILE ?= /tmp/custom-metrics-collector-override-config-$(shell date +%s).yaml

record-override: $(YQ) $(ORAS)
	@az acr login --name ${ARO_HCP_IMAGE_ACR} --expose-token --output tsv --query accessToken 2>/dev/null | \
    		$(ORAS) login ${ARO_HCP_IMAGE_REGISTRY} \
    			--username 00000000-0000-0000-0000-000000000000 \
    			--password-stdin >/dev/null 2>&1

	@DIGEST=$$($(ORAS) manifest fetch --descriptor ${CUSTOM_METRICS_COLLECTOR_TAGGED_IMAGE} | $(YQ) .digest); \
	$(YQ) eval -n "\
		.clouds.dev.environments.$(DEPLOY_ENV).defaults.customMetricsCollector.image.repository = \"$(CUSTOM_METRICS_COLLECTOR_GENERATED_IMAGE_REPOSITORY)\" | \
		.clouds.dev.environments.$(DEPLOY_ENV).defaults.customMetricsCollector.image.digest = \"$$DIGEST\" \
	" > $(OVERRIDE_CONFIG_FILE); \
	echo "Override config written to: $(OVERRIDE_CONFIG_FILE)" >&2; \
	echo "$(OVERRIDE_CONFIG_FILE)"
.PHONY: record-override

# Note: Key Vault secrets are managed separately using dev-infrastructure/scripts/kv-add-secret.sh
# or Azure CLI. The deployment uses SecretProviderClass to mount secrets from Key Vault.
# This target is only used as a fallback for Kubernetes secrets (when secretProvider.keyVault is not set).
update-values-with-secret:
	@if [ -n "$${MSI_CLIENT_SECRET:-}" ]; then \
		echo "Adding client secret to values.yaml..."; \
		echo "Note: This is for Kubernetes secrets fallback only. Key Vault secrets are managed separately."; \
		VALUES_FILE=$(CUSTOM_METRICS_COLLECTOR_DIR)/values.yaml; \
		cp $$VALUES_FILE $$VALUES_FILE.bak; \
		if grep -q "^# msiClientSecret:" $$VALUES_FILE; then \
			sed -i '' "s|^# msiClientSecret:.*|msiClientSecret: \"$$MSI_CLIENT_SECRET\"|" $$VALUES_FILE; \
		else \
			echo "msiClientSecret: \"$$MSI_CLIENT_SECRET\"" >> $$VALUES_FILE; \
		fi; \
		echo "Updated values.yaml with client secret (msiClientId/msiTenantId set by pipeline)"; \
	fi
.PHONY: update-values-with-secret

restore-values:
	@if [ -f $(CUSTOM_METRICS_COLLECTOR_DIR)/values.yaml.bak ]; then \
		mv $(CUSTOM_METRICS_COLLECTOR_DIR)/values.yaml.bak $(CUSTOM_METRICS_COLLECTOR_DIR)/values.yaml; \
		echo "Restored values.yaml"; \
	fi
.PHONY: restore-values

# Deploy the custom metrics collector
# Note: Key Vault secrets must be managed separately before deployment.
# Use dev-infrastructure/scripts/kv-add-secret.sh to add secrets to Key Vault.
# The deployment uses SecretProviderClass to automatically mount secrets from Key Vault.
deploy: build-and-push update-values-with-secret
	@OVERRIDE_FILE=$$(make record-override DEPLOY_ENV=$(DEPLOY_ENV)); \
	echo "Deploying with override config: $$OVERRIDE_FILE"; \
	cd $(CUSTOM_METRICS_COLLECTOR_DIR)/../.. && \
		make -s -C tooling/templatize templatize && \
		./tooling/templatize/templatize pipeline run \
			--config-file=config/config.yaml \
			--config-file-override=$$OVERRIDE_FILE \
			--dev-settings-file=tooling/templatize/settings.yaml \
			--dev-environment=$(DEPLOY_ENV) \
			--topology-file=topology.yaml \
			--service-group=Microsoft.Azure.ARO.HCP.CustomMetricsCollector \
			-v 5; \
	cd $(CUSTOM_METRICS_COLLECTOR_DIR) && $(MAKE) restore-values
.PHONY: deploy

helm-chart:
	$(eval BUNDLE_TMP_PATH := $(shell mktemp -d tmp-custom-metrics-collector-bundle.XXX))
	@echo "Building Helm chart in $(BUNDLE_TMP_PATH)"
	@echo "This is a placeholder for future Helm chart generation"
.PHONY: helm-chart

create-pers-sp:
	@echo "Creating service principal for custom-metrics-collector in personal dev..."
	@SP_NAME="custom-metrics-collector-redhat0-ms-graph-ro" && \
	SP_OUTPUT=$$(az ad sp create-for-rbac --name "$$SP_NAME" --skip-assignment --output json) && \
	APP_ID=$$(echo "$$SP_OUTPUT" | jq -r '.appId') && \
	CLIENT_SECRET=$$(echo "$$SP_OUTPUT" | jq -r '.password') && \
	TENANT_ID=$$(echo "$$SP_OUTPUT" | jq -r '.tenant') && \
	echo "" && \
	echo "Service Principal created successfully!" && \
	echo "==========================================" && \
	echo "Client ID (App ID): $$APP_ID" && \
	echo "Client Secret: $$CLIENT_SECRET" && \
	echo "Tenant ID: $$TENANT_ID" && \
	echo "==========================================" && \
	echo "" && \
	echo "Granting Microsoft Graph API permissions..." && \
	echo "  Adding Organization.Read.All (required for /organization endpoint and directorySizeQuota)..." && \
	echo "  Adding Directory.Read.All..." && \
	CLEAN_PERMS=$$(mktemp) && \
	cat > $$CLEAN_PERMS <<'PERMS_EOF' && \
	[ \
	  { \
	    "resourceAppId": "00000003-0000-0000-c000-000000000000", \
	    "resourceAccess": [ \
	      { \
	        "id": "498476ce-e0fe-48b0-b801-37ba7e2685c6", \
	        "type": "Role" \
	      }, \
	      { \
	        "id": "df021288-bdef-4463-88db-98f22de89214", \
	        "type": "Role" \
	      } \
	    ] \
	  } \
	] \
	PERMS_EOF
	az ad app update --id "$$APP_ID" --required-resource-access @$$CLEAN_PERMS && \
	rm -f $$CLEAN_PERMS && \
	az ad app permission admin-consent --id "$$APP_ID" 2>/dev/null || echo "Note: Admin consent may require approval from tenant admin" && \
	echo "" && \
	echo "Next steps:" && \
	echo "  1. Add the client secret to Key Vault using:" && \
	echo "     ../../dev-infrastructure/scripts/kv-add-secret.sh <keyvault-name> <resource-group> custom-metrics-collector-redhat0-client-secret \"$$CLIENT_SECRET\"" && \
	echo "" && \
	echo "  2. The deployment will automatically use the secret from Key Vault via SecretProviderClass."
.PHONY: create-pers-sp

update-pers-sp-permissions:
	@if [ -z "$${APP_ID:-}" ]; then \
		echo "Error: APP_ID environment variable is required."; \
		echo "Usage: APP_ID=your-app-id make update-pers-sp-permissions"; \
		echo "Example: APP_ID=1ef710d1-afd7-4bf3-8095-e8126650607f make update-pers-sp-permissions"; \
		exit 1; \
	fi && \
	echo "Updating permissions for service principal: $$APP_ID" && \
	echo "" && \
	echo "Creating clean permissions list (no duplicates)..." && \
	CLEAN_PERMS=$$(mktemp) && \
	cat > $$CLEAN_PERMS <<'PERMS_EOF' && \
	[ \
	  { \
	    "resourceAppId": "00000003-0000-0000-c000-000000000000", \
	    "resourceAccess": [ \
	      { \
	        "id": "498476ce-e0fe-48b0-b801-37ba7e2685c6", \
	        "type": "Role" \
	      }, \
	      { \
	        "id": "df021288-bdef-4463-88db-98f22de89214", \
	        "type": "Role" \
	      } \
	    ] \
	  } \
	] \
	PERMS_EOF
	az ad app update --id "$$APP_ID" --required-resource-access @$$CLEAN_PERMS && \
	rm -f $$CLEAN_PERMS && \
	echo "" && \
	echo "Requesting admin consent..." && \
	az ad app permission admin-consent --id "$$APP_ID" 2>/dev/null || echo "  Note: Admin consent may require approval from tenant admin" && \
	echo "" && \
	echo "Final permissions:" && \
	sleep 2 && \
	az ad app permission list --id "$$APP_ID" --output json | jq -r '.[] | .resourceAccess[] | "\(.id) - \(.type)"' 2>/dev/null
.PHONY: update-pers-sp-permissions

regenerate-pers-sp-secret:
	@if [ -z "$${APP_ID:-}" ]; then \
		echo "Error: APP_ID environment variable is required."; \
		echo "Usage: APP_ID=your-app-id make regenerate-pers-sp-secret"; \
		echo "Example: APP_ID=1ef710d1-afd7-4bf3-8095-e8126650607f make regenerate-pers-sp-secret"; \
		exit 1; \
	fi && \
	echo "Regenerating client secret for service principal: $$APP_ID" && \
	echo "Note: This will invalidate the old secret." && \
	echo "" && \
	SP_OUTPUT=$$(az ad sp credential reset --id "$$APP_ID" --output json) && \
	NEW_SECRET=$$(echo "$$SP_OUTPUT" | jq -r '.password') && \
	echo "" && \
	echo "==========================================" && \
	echo "NEW CLIENT SECRET:" && \
	echo "$$NEW_SECRET" && \
	echo "==========================================" && \
	echo "" && \
	echo "⚠️  IMPORTANT: Save this secret now - it won't be shown again!" && \
	echo "" && \
	echo "To update the Key Vault secret (recommended), run:" && \
	echo "  ../../dev-infrastructure/scripts/kv-add-secret.sh \\" && \
	echo "    <keyvault-name> \\" && \
	echo "    <resource-group> \\" && \
	echo "    custom-metrics-collector-redhat0-client-secret \\" && \
	echo "    \"$$NEW_SECRET\"" && \
	echo "" && \
	echo "Or using Azure CLI:" && \
	echo "  az keyvault secret set \\" && \
	echo "    --vault-name <keyvault-name> \\" && \
	echo "    --name custom-metrics-collector-redhat0-client-secret \\" && \
	echo "    --value \"$$NEW_SECRET\""
.PHONY: regenerate-pers-sp-secret


