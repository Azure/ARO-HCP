TENANT_QUOTA_COLLECTOR_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))

-include $(TENANT_QUOTA_COLLECTOR_DIR)/../../../setup-templatize-env.mk
-include $(TENANT_QUOTA_COLLECTOR_DIR)/../../../.bingo/Variables.mk

ARO_HCP_REVISION = $(shell git rev-parse HEAD)
ARO_HCP_IMAGE_TAG ?= $(shell DEPLOY_ENV=${DEPLOY_ENV} $(TENANT_QUOTA_COLLECTOR_DIR)/../../../generate-tag.sh)
ARO_HCP_IMAGE_ACR ?= arohcpsvcdev
ARO_HCP_IMAGE_REGISTRY ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io
# Default repository name for tenant-quota (can be overridden via CUSTOM_METRICS_COLLECTOR_IMAGE_REPOSITORY)
TENANT_QUOTA_COLLECTOR_IMAGE_REPOSITORY ?= tenant-quota-collector
ARO_HCP_IMAGE_REPOSITORY ?= ${TENANT_QUOTA_COLLECTOR_IMAGE_REPOSITORY}
TENANT_QUOTA_COLLECTOR_GENERATED_IMAGE_REPOSITORY = $(shell DEPLOY_ENV=${DEPLOY_ENV} BASELINE_REPO=${ARO_HCP_IMAGE_REPOSITORY} $(TENANT_QUOTA_COLLECTOR_DIR)/../../../generate-repo.sh)
TENANT_QUOTA_COLLECTOR_TAGGED_IMAGE ?= $(ARO_HCP_IMAGE_REGISTRY)/$(TENANT_QUOTA_COLLECTOR_GENERATED_IMAGE_REPOSITORY):$(ARO_HCP_IMAGE_TAG)

ifeq ($(ARO_HCP_IMAGE_REGISTRY:%.azurecr.io=azurecr.io),azurecr.io)
ARO_HCP_IMAGE_REGISTRY_IS_ACR = 1
endif

.DEFAULT_GOAL := tenant-quota-collector

tenant-quota-collector:
	go build -ldflags="-X github.com/Azure/ARO-HCP/internal/version.CommitSHA=${ARO_HCP_IMAGE_TAG}" -o tenant-quota-collector .

run:
	CONFIG_PATH=./deploy/templates/configmap.yaml ./tenant-quota-collector

clean:
	rm -f tenant-quota-collector

image: Dockerfile $(shell find . -name '*.go' -o -name 'go.mod' -o -name 'go.sum') Makefile
	cd $(TENANT_QUOTA_COLLECTOR_DIR)/../../.. && \
	docker build . --file dev-infrastructure/ops-tools/tenant-quota/Dockerfile \
	               --build-arg PLATFORM=linux/amd64 \
	               --build-arg REVISION=${ARO_HCP_REVISION} \
	               --build-arg TAG=${ARO_HCP_IMAGE_TAG} \
	               --tag ${TENANT_QUOTA_COLLECTOR_TAGGED_IMAGE}
.PHONY: image

build-and-push: image $(ORAS)
	az acr login --name ${ARO_HCP_IMAGE_ACR}
	docker push ${TENANT_QUOTA_COLLECTOR_TAGGED_IMAGE}
.PHONY: build-and-push

# Required environment variables for opstool deployment
# OPSTOOL_RG: Resource group where opstool cluster is deployed (e.g., hcp-underlay-opstool-uksouth-svc)
# OPSTOOL_SUBSCRIPTION_ID: Subscription ID (opstool subscription)
# OPSTOOL_CLUSTER_NAME: AKS cluster name (e.g., opstool-uksouth-svc-1)
# OPSTOOL_KEYVAULT_NAME: Service Key Vault name (e.g., aro-hcp-dev-svc-kv)
# OPSTOOL_KEYVAULT_RG: Service Key Vault resource group (e.g., global)
#
# Optional: OPSTOOL_VALUES_FILE - Path to custom values.yaml file for tenant configuration
#   If not set, uses default values.yaml with RedHat0 tenant
#   To add multiple tenants, create a custom values.yaml or use --set flags

# Pipeline targets - use environment variables from pipeline system
tenant-quota-create-identity:
	@[ "${OPSTOOL_RG}" ] || ( echo ">> OPSTOOL_RG is not set"; exit 1 )
	@[ "${OPSTOOL_SUBSCRIPTION_ID}" ] || ( echo ">> OPSTOOL_SUBSCRIPTION_ID is not set"; exit 1 )
	@echo "Creating tenant-quota-collector managed identity..."
	@if az identity show --name tenant-quota-collector --resource-group ${OPSTOOL_RG} --subscription ${OPSTOOL_SUBSCRIPTION_ID} >/dev/null 2>&1; then \
		echo "✅ Identity already exists"; \
	else \
		az identity create \
			--name tenant-quota-collector \
			--resource-group ${OPSTOOL_RG} \
			--subscription ${OPSTOOL_SUBSCRIPTION_ID} && \
		echo "✅ Identity created successfully"; \
	fi
.PHONY: tenant-quota-create-identity

tenant-quota-kv-permissions:
	@[ "${OPSTOOL_RG}" ] || ( echo ">> OPSTOOL_RG is not set"; exit 1 )
	@[ "${OPSTOOL_SUBSCRIPTION_ID}" ] || ( echo ">> OPSTOOL_SUBSCRIPTION_ID is not set"; exit 1 )
	@[ "${OPSTOOL_KEYVAULT_NAME}" ] || ( echo ">> OPSTOOL_KEYVAULT_NAME is not set"; exit 1 )
	@[ "${OPSTOOL_KEYVAULT_RG}" ] || ( echo ">> OPSTOOL_KEYVAULT_RG is not set"; exit 1 )
	@echo "Granting Key Vault permissions..."
	@PRINCIPAL_ID=$$(az identity show --name tenant-quota-collector --resource-group ${OPSTOOL_RG} --subscription ${OPSTOOL_SUBSCRIPTION_ID} --query principalId -o tsv); \
	KV_RESOURCE_ID=$$(az keyvault show --name ${OPSTOOL_KEYVAULT_NAME} --resource-group ${OPSTOOL_KEYVAULT_RG} --subscription ${OPSTOOL_SUBSCRIPTION_ID} --query id -o tsv); \
	az role assignment create \
		--role "Key Vault Secrets User" \
		--assignee $$PRINCIPAL_ID \
		--scope $$KV_RESOURCE_ID \
		--only-show-errors >/dev/null 2>&1 || true; \
	echo "✅ Key Vault permissions granted"
.PHONY: tenant-quota-kv-permissions

tenant-quota-federate-identity:
	@[ "${OPSTOOL_RG}" ] || ( echo ">> OPSTOOL_RG is not set"; exit 1 )
	@[ "${OPSTOOL_SUBSCRIPTION_ID}" ] || ( echo ">> OPSTOOL_SUBSCRIPTION_ID is not set"; exit 1 )
	@[ "${OPSTOOL_CLUSTER_NAME}" ] || ( echo ">> OPSTOOL_CLUSTER_NAME is not set"; exit 1 )
	@echo "Creating federated identity credential..."
	@KUBECONFIG=$$(mktemp); \
	az aks get-credentials --name ${OPSTOOL_CLUSTER_NAME} --resource-group ${OPSTOOL_RG} --subscription ${OPSTOOL_SUBSCRIPTION_ID} --file $$KUBECONFIG --overwrite-existing >/dev/null 2>&1; \
	kubelogin convert-kubeconfig -l azurecli --kubeconfig $$KUBECONFIG >/dev/null 2>&1; \
	ISSUER_URL=$$(kubectl get --raw /.well-known/openid-configuration --kubeconfig $$KUBECONFIG | grep -o '"issuer":"[^"]*' | cut -d'"' -f4); \
	if [ -z "$$ISSUER_URL" ]; then \
		echo "ERROR: Failed to fetch OIDC issuer URL from cluster"; \
		rm -f $$KUBECONFIG; \
		exit 1; \
	fi; \
	SUBJECT="system:serviceaccount:tenant-quota:tenant-quota-collector"; \
	if az identity federated-credential show \
		--name tenant-quota-collector-fedcred \
		--identity-name tenant-quota-collector \
		--resource-group ${OPSTOOL_RG} \
		--subscription ${OPSTOOL_SUBSCRIPTION_ID} >/dev/null 2>&1; then \
		echo "✅ Federated credential already exists"; \
	else \
		az identity federated-credential create \
			--name tenant-quota-collector-fedcred \
			--identity-name tenant-quota-collector \
			--resource-group ${OPSTOOL_RG} \
			--subscription ${OPSTOOL_SUBSCRIPTION_ID} \
			--issuer "$$ISSUER_URL" \
			--subject "$$SUBJECT" \
			--audience "api://AzureADTokenExchange" && \
		echo "✅ Federated credential created"; \
	fi; \
	rm -f $$KUBECONFIG
.PHONY: tenant-quota-federate-identity

tenant-quota-deploy-helm: build-and-push
	@[ "${OPSTOOL_RG}" ] || ( echo ">> OPSTOOL_RG is not set"; exit 1 )
	@[ "${OPSTOOL_SUBSCRIPTION_ID}" ] || ( echo ">> OPSTOOL_SUBSCRIPTION_ID is not set"; exit 1 )
	@[ "${OPSTOOL_CLUSTER_NAME}" ] || ( echo ">> OPSTOOL_CLUSTER_NAME is not set"; exit 1 )
	@[ "${OPSTOOL_KEYVAULT_NAME}" ] || ( echo ">> OPSTOOL_KEYVAULT_NAME is not set"; exit 1 )
	@echo "Deploying tenant-quota-collector..."
	@CLIENT_ID=$$(az identity show --name tenant-quota-collector --resource-group ${OPSTOOL_RG} --subscription ${OPSTOOL_SUBSCRIPTION_ID} --query clientId -o tsv); \
	TENANT_ID=$$(az account show --subscription ${OPSTOOL_SUBSCRIPTION_ID} --query tenantId -o tsv); \
	echo "✅ Using image: ${TENANT_QUOTA_COLLECTOR_TAGGED_IMAGE}"; \
	KUBECONFIG=$$(mktemp); \
	az aks get-credentials --name ${OPSTOOL_CLUSTER_NAME} --resource-group ${OPSTOOL_RG} --subscription ${OPSTOOL_SUBSCRIPTION_ID} --file $$KUBECONFIG --overwrite-existing >/dev/null 2>&1; \
	kubelogin convert-kubeconfig -l azurecli --kubeconfig $$KUBECONFIG >/dev/null 2>&1; \
	if [ -n "${OPSTOOL_VALUES_FILE}" ]; then \
		echo "Using custom values file: ${OPSTOOL_VALUES_FILE}"; \
		helm upgrade --install tenant-quota $(TENANT_QUOTA_COLLECTOR_DIR)/deploy \
			--namespace tenant-quota \
			--create-namespace \
			--kubeconfig $$KUBECONFIG \
			--values ${OPSTOOL_VALUES_FILE} \
			--set imageRegistry=arohcpsvcdev.azurecr.io \
			--set imageRepository=${TENANT_QUOTA_COLLECTOR_GENERATED_IMAGE_REPOSITORY} \
			--set imageTag=${ARO_HCP_IMAGE_TAG} \
			--set msiClientId=$$CLIENT_ID \
			--set msiTenantId=$$TENANT_ID \
			--set secretProvider.keyVault=${OPSTOOL_KEYVAULT_NAME} \
			--set secretProvider.msiClientId=$$CLIENT_ID \
			--wait; \
	else \
		echo "Using default values.yaml (RedHat0 tenant)"; \
		helm upgrade --install tenant-quota $(TENANT_QUOTA_COLLECTOR_DIR)/deploy \
			--namespace tenant-quota \
			--create-namespace \
			--kubeconfig $$KUBECONFIG \
			--set imageRegistry=arohcpsvcdev.azurecr.io \
			--set imageRepository=${TENANT_QUOTA_COLLECTOR_GENERATED_IMAGE_REPOSITORY} \
			--set imageTag=${ARO_HCP_IMAGE_TAG} \
			--set msiClientId=$$CLIENT_ID \
			--set msiTenantId=$$TENANT_ID \
			--set secretProvider.keyVault=${OPSTOOL_KEYVAULT_NAME} \
			--set secretProvider.msiClientId=$$CLIENT_ID \
			--set tenants[0].tenantId=64dc69e4-d083-49fc-9569-ebece1dd1408 \
			--set tenants[0].tenantName=RedHat0 \
			--set tenants[0].servicePrincipalClientId=1ef710d1-afd7-4bf3-8095-e8126650607f \
			--set tenants[0].keyVaultSecretName=custom-metrics-collector-redhat0-client-secret \
			--wait; \
	fi; \
	rm -f $$KUBECONFIG; \
	echo "✅ Deployment complete!"
	@echo ""
	@echo "📝 Note: To add additional tenants, create a custom values.yaml file"
	@echo "   with all tenant configurations and set OPSTOOL_VALUES_FILE=<path>"
	@echo "   See DEPLOY.md for detailed instructions."
.PHONY: opstool-deploy

# Environment variables are provided by the pipeline system from config.yaml
tenant-quota-deploy: tenant-quota-create-identity tenant-quota-federate-identity tenant-quota-kv-permissions tenant-quota-deploy-helm
	@echo "✅ Tenant quota collector deployment complete!"
	@echo ""
	@echo "⚠️  IMPORTANT: Ensure service principal secrets exist in Key Vault before deployment!"
	@echo "   If secrets don't exist, the pod will fail to authenticate."
.PHONY: tenant-quota-deploy

# Manual deployment target - requires environment variables to be set manually
opstool-deploy: tenant-quota-create-identity tenant-quota-federate-identity tenant-quota-kv-permissions tenant-quota-deploy-helm
	@echo "✅ Opstool deployment complete!"
	@echo ""
	@echo "⚠️  IMPORTANT: Ensure service principal secrets exist in Key Vault before deployment!"
	@echo "   If secrets don't exist, the pod will fail to authenticate."
	@echo "   Use the existing script to store secrets:"
	@echo "   ../../scripts/kv-add-secret.sh <kv-name> <rg> <secret-name> <secret-value>"
	@echo "   Example: ../../scripts/kv-add-secret.sh aro-hcp-dev-svc-kv global custom-metrics-collector-redhat0-client-secret \"<sp-client-secret>\""
.PHONY: opstool-deploy
