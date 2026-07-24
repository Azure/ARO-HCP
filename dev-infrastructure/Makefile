SHELL = /bin/bash
DEPLOY_ENV ?= pers
CLOUD ?= dev
PRINCIPAL_ID ?= $(shell az ad signed-in-user show -o json | jq -r '.id')

-include ../setup-templatize-env.mk
-include ../.bingo/Variables.mk

# Set SKIP_CONFIRM to a non-empty value to skip "what-if" confirmation prompts.
ifndef SKIP_CONFIRM
PROMPT_TO_CONFIRM = "--confirm-with-what-if"
endif

PERSIST ?= false
NPROC ?= $(shell command -v nproc > /dev/null 2>&1 && nproc || echo 4)

MGMT_KUBECONFIG_FILE ?= ${HOME}/.kube/${MGMT_RESOURCEGROUP}.kubeconfig
SVC_KUBECONFIG_FILE ?= ${HOME}/.kube/${SVC_RESOURCEGROUP}.kubeconfig

DEPLOYMENT_NAME_SUFFIX ?=
GLOBAL_RG_DEPLOYMENT_NAME ?= global${DEPLOYMENT_NAME_SUFFIX}
MGMG_RG_DEPLOYMENT_NAME ?= mgmt${DEPLOYMENT_NAME_SUFFIX}
SVC_RG_DEPLOYMENT_NAME ?= svc${DEPLOYMENT_NAME_SUFFIX}
REGIONAL_RG_DEPLOYMENT_NAME ?= region${DEPLOYMENT_NAME_SUFFIX}
ROLE_ASSIGNMENTS_DEPLOYMENT_NAME ?= role-assignments${DEPLOYMENT_NAME_SUFFIX}

# Subscription IDs
DEV_TESTING_SUBSCRIPTION_ID = 1d3378d3-5a3f-4712-85a1-2485495dfc4b
INT_TESTING_SUBSCRIPTION_ID = 64f0619f-ebc2-4156-9d91-c4c781de7e54
STAGE_TESTING_SUBSCRIPTION_ID = b23756f7-4594-40a3-980f-10bb6168fc20
E2E_TESTING_SUBSCRIPTION_ID = 974ebd46-8ad3-41e3-afef-7ef25fd5c371

# Environments where automation accounts are deployed

# MSI mock pool size for boskos leasing in the E2E testing subscription
MSI_MOCK_POOL_SIZE ?= 20

list:
	@grep '^[^#[:space:]].*:' Makefile
.PHONY: list

templates := $(wildcard ./templates/*.bicep)
modules := $(shell find ./modules -name "*.bicep")
parameters := $(filter-out $(wildcard ./templates/*.tmpl.bicepparam),$(wildcard ./templates/*.bicepparam))

_fmt: $(templates:.bicep=.bicep.fmt) $(modules:.bicep=.bicep.fmt) $(parameters:.bicepparam=.biceparam.fmt)
fmt:
	@echo "Running make fmt in parallel across $(NPROC) cores"
	@$(MAKE) -j$(NPROC) _fmt

_lint: $(templates:.bicep=.bicep.lint) $(modules:.bicep=.bicep.lint) $(parameters:.bicepparam=.biceparam.lint)
lint:
	@echo "Running make lint in parallel across $(NPROC) cores"
	@$(MAKE) -j$(NPROC) _lint

.deploy-env-ctx: FORCE
	@echo "$(DEPLOY_ENV):$(USER):$(REGION)" | cmp -s - $@ || echo "$(DEPLOY_ENV):$(USER):$(REGION)" > $@

FORCE:
.PHONY: FORCE

%.bicepparam: $(TEMPLATIZE) $(CONFIG_FILE) $(DEV_SETTINGS_FILE) .deploy-env-ctx
	$(TEMPLATIZE) generate \
		--config-file $(CONFIG_FILE) \
		--dev-settings-file $(DEV_SETTINGS_FILE) \
		--dev-environment $(DEPLOY_ENV) \
		--input "$(basename $@).tmpl.bicepparam" \
		--region $(REGION) \
		--output "$@"

%.bicep.fmt %.bicepparam.fmt:
	@AZURE_BICEP_CHECK_VERSION=False az bicep format --file $(basename $@) 2>&1 | awk 'NF'

%.bicep.lint %.bicepparam.lint:
	@AZURE_BICEP_CHECK_VERSION=False az bicep lint --file $(basename $@) 2>&1 | awk 'NF'

feature-registration: # hardcoded to eastus as this is a subscription deployment, not a resource group
	@az deployment sub create \
		--name "feature-registration" \
		--location eastus \
		--template-file templates/dev-feature-registration.bicep \
		$(PROMPT_TO_CONFIRM)
.PHONY: feature-registration

deploy-mock-identity-apps: configurations/mock-identity-apps.bicepparam
	az deployment group create \
		--name "mock-identity-apps" \
		--resource-group $(GLOBAL_RESOURCEGROUP) \
		--template-file templates/mock-identity-apps.bicep $(PROMPT_TO_CONFIRM) \
		--parameters configurations/mock-identity-apps.bicepparam

.PHONY: deploy-mock-identity-apps

# Create the self-signed Key Vault certificates the mock identity apps trust via
# SNI. mock-identity-apps.bicep only declares the trusted subject name/issuer; it
# does not create certs (Bicep cannot create Key Vault certificates). Idempotent:
# create-kv-cert.sh leaves an existing cert untouched unless FORCE=true, so this
# is safe to re-run during bootstrap. Run once before the mock identities are
# used. The cert subject DNS names mirror config/config-dev-ci.yaml
# .ci.dev.mockIdentities.*.certDns; the cert (object) names are the Key Vault
# certificate names the services load and are fixed here (not in config).
create-mock-identity-certs:
	VAULT_NAME=$(SVC_KV_NAME) CERT_NAME=firstPartyCert2 \
		SUBJECT=CN=firstparty.hcp.osadev.cloud DNS_NAMES=firstparty.hcp.osadev.cloud \
		./scripts/create-kv-cert.sh
	VAULT_NAME=$(SVC_KV_NAME) CERT_NAME=armHelperCert2 \
		SUBJECT=CN=armhelper.hcp.osadev.cloud DNS_NAMES=armhelper.hcp.osadev.cloud \
		./scripts/create-kv-cert.sh
	VAULT_NAME=$(SVC_KV_NAME) CERT_NAME=msiMockCert2 \
		SUBJECT=CN=msimock.hcp.osadev.cloud DNS_NAMES=msimock.hcp.osadev.cloud \
		./scripts/create-kv-cert.sh
	@for i in $$(seq 0 $$(($(MSI_MOCK_POOL_SIZE) - 1))); do \
		echo "Creating/updating MSI mock pool cert $$i ..."; \
		VAULT_NAME=$(SVC_KV_NAME) \
		CERT_NAME=msiMockPoolCert-$$i \
		SUBJECT=CN=$$i.msimockpool.hcp.osadev.cloud \
		DNS_NAMES=$$i.msimockpool.hcp.osadev.cloud \
		./scripts/create-kv-cert.sh; \
	done
.PHONY: create-mock-identity-certs

# INT variant: certificates live in the INT global Key Vault. See
# docs/sops/msit-int-credential-setup.md for the surrounding manual lift/shift.
create-int-mock-identity-certs:
	VAULT_NAME=aro-hcp-int-kv CERT_NAME=intFirstPartyCert \
		SUBJECT=CN=intfirstparty.hcp.osadev.cloud DNS_NAMES=intfirstparty.hcp.osadev.cloud \
		./scripts/create-kv-cert.sh
	VAULT_NAME=aro-hcp-int-kv CERT_NAME=intArmHelperCert \
		SUBJECT=CN=intarmhelper.hcp.osadev.cloud DNS_NAMES=intarmhelper.hcp.osadev.cloud \
		./scripts/create-kv-cert.sh
	VAULT_NAME=aro-hcp-int-kv CERT_NAME=intMsiMockCert \
		SUBJECT=CN=intmsimock.hcp.osadev.cloud DNS_NAMES=intmsimock.hcp.osadev.cloud \
		./scripts/create-kv-cert.sh
.PHONY: create-int-mock-identity-certs

populate-msi-mock-pool:
	cd openshift-ci && \
	MSI_MOCK_POOL_SIZE=$(MSI_MOCK_POOL_SIZE) \
	./populate-msi-mock-pool.sh

.PHONY: populate-msi-mock-pool

frontend-grant-ingress:
	./scripts/frontend-grant-ingress-from-ip.sh "${SVC_RESOURCEGROUP}"
.PHONY: frontend-grant-ingress

#
# Global
#

global:
	make -C .. pipeline/Global DEPLOY_ENV=$(DEPLOY_ENV)
.PHONY: global

global.what-if:
	make -C .. pipeline/Global DRY_RUN=true DEPLOY_ENV=$(DEPLOY_ENV)
.PHONY: global.what-if

#
# Region
#

region:
	make -C .. pipeline/Region DEPLOY_ENV=$(DEPLOY_ENV)
.PHONY: region

region.clean:
	@if [ "$$(az group exists --name $(REGIONAL_RESOURCEGROUP) --output json)" = "true" ]; then \
		echo "Will delete Azure resource group $(REGIONAL_RESOURCEGROUP)"; \
		az group delete -g $(REGIONAL_RESOURCEGROUP); \
	fi
.PHONY: region.clean

region.what-if:
	make -C .. pipeline/Region DRY_RUN=true DEPLOY_ENV=$(DEPLOY_ENV)
.PHONY: region.what-if

#
# Service Cluster
#

svc:
	make -C .. pipeline/Service.Infra DEPLOY_ENV=$(DEPLOY_ENV)
.PHONY: svc

svc.cs-pr-check-msi: configurations/cs-integ-msi.bicepparam
	az deployment group create \
		--name cs-integ-msi \
		--resource-group $(SVC_RESOURCEGROUP) \
		--template-file templates/dev-cs-integration-msi.bicep \
		$(PROMPT_TO_CONFIRM) \
		--parameters \
			configurations/cs-integ-msi.bicepparam
.PHONY: svc.cs-pr-check-msi

svc.aks.admin-access:
	@scripts/aks-admin-access.sh $(SVC_RESOURCEGROUP) $(PRINCIPAL_ID)
.PHONY: svc.aks.admin-access

svc.aks.kubeconfig:
	@az aks get-credentials --overwrite-existing --only-show-errors -n ${SVC_AKS_NAME} -g $(SVC_RESOURCEGROUP) -f "${SVC_KUBECONFIG_FILE}"
	@kubelogin convert-kubeconfig -l azurecli --kubeconfig "${SVC_KUBECONFIG_FILE}"
.PHONY: svc.aks.kubeconfig

svc.aks.kubeconfig.pipeline:
	make -C .. pipeline/Service.Kubeconfig DEPLOY_ENV=$(DEPLOY_ENV)
.PHONY: svc.aks.kubeconfig.pipeline

svc.aks.kubeconfigfile:
	@echo ${SVC_KUBECONFIG_FILE}
.PHONY: svc.aks.kubeconfigfile

svc.oidc.storage.permissions:
	@USER_TYPE=$(shell az account show -o json | jq -r '.user.type') && \
	if [ "$${USER_TYPE}" = "user" ]; then \
		STORAGEACCOUNTID=$(shell az storage account show -n ${OIDC_STORAGE_ACCOUNT} -g ${REGIONAL_RESOURCEGROUP} --query id -o tsv) && \
		az role assignment create \
		--role "Storage Blob Data Contributor" \
		--assignee ${PRINCIPAL_ID} \
		--scope "$${STORAGEACCOUNTID}" \
		--only-show-errors; \
	fi
.PHONY: svc.oidc.storage.permissions

svc.kv.permission:
	@scripts/kv-permissions.sh $(PRINCIPAL_ID) $(SVC_KV_RESOURCEGROUP) $(SVC_KV_NAME)
.PHONY: svc.kv.permission

svc.dev.permissions: svc.oidc.storage.permissions svc.kv.permission
.PHONY: svc.dev.permissions

svc.init: region svc svc.aks.admin-access svc.aks.kubeconfig svc.dev.permissions
.PHONY: svc.init

svc.what-if:
	make -C .. pipeline/Service.Infra DRY_RUN=true DEPLOY_ENV=$(DEPLOY_ENV)
.PHONY: svc.what-if

svc.dev-role-assignments: configurations/dev-role-assignments.bicepparam
	az deployment group create \
		--name ${ROLE_ASSIGNMENTS_DEPLOYMENT_NAME} \
		--resource-group "${REGIONAL_RESOURCEGROUP}" \
		--template-file templates/dev-roleassignments.bicep \
		--parameters configurations/dev-role-assignments.bicepparam \
		--parameters principalID=${PRINCIPAL_ID}
.PHONY: svc.dev-role-assignments

svc.clean-nsp-associations:
	./scripts/delete-nsp.sh $(SVC_RESOURCEGROUP)

svc.clean: svc.clean-nsp-associations
	@if [ "$$(az group exists --name $(SVC_RESOURCEGROUP) --output json)" = "true" ]; then \
		echo "Will delete Azure resource group $(SVC_RESOURCEGROUP)"; \
		az group delete -g $(SVC_RESOURCEGROUP); \
	fi
.PHONY: svc.clean

#
# Management Cluster
#

mgmt:
	make -C .. pipeline/Management.Infra DEPLOY_ENV=$(DEPLOY_ENV)
.PHONY: mgmt

mgmt.aks.admin-access:
	@scripts/aks-admin-access.sh $(MGMT_RESOURCEGROUP) $(PRINCIPAL_ID)
.PHONY: mgmt.aks.admin-access

mgmt.aks.kubeconfig:
	@az aks get-credentials --overwrite-existing --only-show-errors -n ${MGMT_AKS_NAME} -g $(MGMT_RESOURCEGROUP) -f "${MGMT_KUBECONFIG_FILE}"
	@kubelogin convert-kubeconfig -l azurecli --kubeconfig "${MGMT_KUBECONFIG_FILE}"
.PHONY: mgmt.aks.kubeconfig

mgmt.aks.kubeconfig.pipeline:
	make -C .. pipeline/Management.Kubeconfig DEPLOY_ENV=$(DEPLOY_ENV)
.PHONY: mgmt.aks.kubeconfig.pipeline

mgmt.aks.kubeconfigfile:
	@echo ${MGMT_KUBECONFIG_FILE}
.PHONY: mgmt.aks.kubeconfigfile

mgmt.init: region mgmt mgmt.aks.admin-access mgmt.aks.kubeconfig mgmt.dev.permissions
.PHONY: mgmt.init

mgmt.what-if:
	make -C .. pipeline/Management.Infra DRY_RUN=true DEPLOY_ENV=$(DEPLOY_ENV)
.PHONY: mgmt.what-if

mgmt.clean-nsp-associations:
	./scripts/delete-nsp.sh $(MGMT_RESOURCEGROUP)

mgmt.clean: mgmt.clean-nsp-associations
	@if [ "$$(az group exists --name $(MGMT_RESOURCEGROUP) --output json)" = "true" ]; then \
		echo "Will delete Azure resource group $(MGMT_RESOURCEGROUP)"; \
		az group delete -g $(MGMT_RESOURCEGROUP); \
	fi
.PHONY: mgmt.clean

mgmt.kv.permission:
	@scripts/kv-permissions.sh $(PRINCIPAL_ID) $(MGMT_RESOURCEGROUP) $(CX_KV_NAME)
	@scripts/kv-permissions.sh $(PRINCIPAL_ID) $(MGMT_RESOURCEGROUP) $(MSI_KV_NAME)
	@scripts/kv-permissions.sh $(PRINCIPAL_ID) $(MGMT_RESOURCEGROUP) $(MGMT_KV_NAME)
.PHONY: mgmt.kv.permission

mgmt.dev.permissions: mgmt.kv.permission
.PHONY: mgmt.dev.permissions

#
# Monitoring
#
kusto: configurations/kusto.bicepparam
	make -C .. pipeline/Geography DEPLOY_ENV=$(DEPLOY_ENV) EXTRA_ARGS="--region eastus2"
	az tag create --resource-id /subscriptions/$(DEV_TESTING_SUBSCRIPTION_ID)/resourceGroups/hcp-kusto-us --tags persist=true
.PHONY: kusto

monitoring:
	make -C .. pipeline/Monitoring DEPLOY_ENV=$(DEPLOY_ENV)
.PHONY: monitoring

monitoring.what-if:
	make -C .. pipeline/Monitoring DRY_RUN=true DEPLOY_ENV=$(DEPLOY_ENV)
.PHONY: monitoring.what-if

# Release Information

release-information: configurations/release-information.bicepparam
	az deployment group create \
		--name release-information \
 		--resource-group $(GLOBAL_RESOURCEGROUP) \
		--subscription $(DEV_TESTING_SUBSCRIPTION_ID) \
		--template-file templates/release-information.bicep \
		$(PROMPT_TO_CONFIRM) \
		--parameters configurations/release-information.bicepparam
.PHONY: release-information

# ACR DEV customizations

acr: acr-svc-cfg
.PHONY: acr

acr.what-if: acr-svc-cfg.what-if
.PHONY: acr.what-if

acr-svc-cfg: configurations/acr-svc.bicepparam # DEV only setup of caching rules in OCP ACR
	az deployment group create \
		--name ${GLOBAL_RG_DEPLOYMENT_NAME}-acr-svc \
		--resource-group $(GLOBAL_RESOURCEGROUP) \
		--template-file templates/dev-acr.bicep \
		$(PROMPT_TO_CONFIRM) \
		--parameters \
			configurations/acr-svc.bicepparam
.PHONY: acr-svc-cfg

acr-svc-cfg.what-if: configurations/acr-svc.bicepparam
	az deployment group what-if \
		--name ${GLOBAL_RG_DEPLOYMENT_NAME}-acr-svc \
		--resource-group $(GLOBAL_RESOURCEGROUP) \
		--template-file templates/dev-acr.bicep \
		--parameters \
			configurations/acr-svc.bicepparam
.PHONY: acr-svc-cfg.what-if

#
# Cosmos Helpers
#

cosmos.access:
    # grants "Cosmos DB Built-in Data Contributor" role to current user
	az cosmosdb sql role assignment create \
    --resource-group $(REGIONAL_RESOURCEGROUP) \
    --account-name $(RP_COSMOS_DB_NAME) \
    --role-definition-id "00000000-0000-0000-0000-000000000002" \
    --principal-id $(PRINCIPAL_ID) \
    --scope "/"
.PHONY: cosmos.access

#
# Postgres Authentication Helpers
#

cs-current-user-pg-connect:
	@scripts/cs-current-user-pg-connect.sh $(REGIONAL_RESOURCEGROUP) $(CS_PG_NAME)
.PHONY: cs-current-user-pg-connect

cs-miwi-pg-connect:
	@scripts/cs-miwi-pg-connect.sh $(REGIONAL_RESOURCEGROUP) $(CS_PG_NAME) $(CS_MI_NAME) $(CS_NS_NAME) $(CS_SA_NAME) $(SVC_RESOURCEGROUP)
.PHONY: cs-miwi-pg-connect

maestro-current-user-pg-connect:
	@scripts/cs-current-user-pg-connect.sh $(REGIONAL_RESOURCEGROUP) $(MAESTRO_PG_NAME)
.PHONY: maestro-current-user-pg-connect

maestro-miwi-pg-connect:
	@scripts/cs-miwi-pg-connect.sh $(REGIONAL_RESOURCEGROUP) $(MAESTRO_PG_NAME) "maestro-server" "maestro" "maestro" $(SVC_RESOURCEGROUP)
.PHONY: maestro-miwi-pg-connect

#
# Secrets
#

secrets-download:
	@[ "${SECRET_SA_ACCOUNT_NAME}" ] || ( echo ">> SECRET_SA_ACCOUNT_NAME is not set"; exit 1 )
	rm -rf secrets
	az storage blob download -n secrets.tar.gz -c secrets -f secrets.tar.gz --account-name ${SECRET_SA_ACCOUNT_NAME} --auth-mode login >/dev/null
	tar -xzf secrets.tar.gz
	rm secrets.tar.gz
.PHONY: secrets-download

secrets-yes-i-want-to-update-the-shared-secrets:
	@[ "${SECRET_SA_ACCOUNT_NAME}" ] || ( echo ">> SECRET_SA_ACCOUNT_NAME is not set"; exit 1 )
	tar -czf secrets.tar.gz secrets
	az storage blob upload -n secrets.tar.gz -c secrets -f secrets.tar.gz --overwrite --auth-mode login --account-name ${SECRET_SA_ACCOUNT_NAME} >/dev/null
	rm secrets.tar.gz
.PHONY: secrets-yes-i-want-to-update-the-shared-secrets

#
# Common
#

what-if: global.what-if acr.what-if region.what-if svc.what-if mgmt.what-if monitoring.what-if
.PHONY: what-if

infra: region svc.init mgmt.init monitoring
.PHONY: infra

clean: svc.clean mgmt.clean region.clean
.PHONY: clean

#
# Local CS Development
#

local-cs-permissions: svc.dev.permissions mgmt.dev.permissions
.PHONY: local-cs-permissions
