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
AUTOMATION_ACCOUNT_ENVS = dev stage int e2e

list:
	@grep '^[^#[:space:]].*:' Makefile
.PHONY: list

ci-export-provision-vars:
	@if [ -z "$${SHARED_DIR}" ]; then \
		echo "Error: SHARED_DIR is not set" >&2; \
		exit 1; \
	fi
	@{ \
		echo "export SVC_RESOURCEGROUP='$(SVC_RESOURCEGROUP)'"; \
		echo "export MGMT_RESOURCEGROUP='$(MGMT_RESOURCEGROUP)'"; \
		echo "export REGIONAL_RESOURCEGROUP='$(REGIONAL_RESOURCEGROUP)'"; \
		echo "export GLOBAL_RESOURCEGROUP='$(GLOBAL_RESOURCEGROUP)'"; \
		echo "export REGION='$(REGION)'"; \
	} > "$${SHARED_DIR}/provision.env"
.PHONY: ci-export-provision-vars

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

# Create mock identities for the INT environment
# This is done within the subscription the customer MRG / nodepools would be created
# Manually lift/shift the cert/keys to the MSIT environment keyvault
create-int-mock-identities: configurations/mock-identities.bicepparam
	MSI_MOCK_ROLE_NAME=int-msi-mock && \
	FIRST_PARTY_ROLE_NAME=int-first-party && \
	FIRST_PARTY_CERT_NAME=intFirstPartyCert && \
	FIRST_PARTY_CERT_DNS=intfirstparty.hcp.osadev.cloud && \
	MSI_MOCK_CERT_NAME=intMsiMockCert && \
	MSI_MOCK_CERT_DNS=intmsimock.hcp.osadev.cloud && \
	ARM_HELPER_CERT_NAME=intArmHelperCert && \
	ARM_HELPER_CERT_DNS=intarmhelper.hcp.osadev.cloud && \
	az deployment group create \
		--name "aro-hcp-int-mock-certificates" \
		--resource-group $(GLOBAL_RESOURCEGROUP) \
		--template-file templates/mock-identities.bicep \
		--parameters configurations/mock-identities.bicepparam \
		keyVaultName=aro-hcp-int-kv \
		msiMockRoleName=$${MSI_MOCK_ROLE_NAME} \
		firstPartyRoleName=$${FIRST_PARTY_ROLE_NAME} \
		firstPartyCertName=$${FIRST_PARTY_CERT_NAME} \
		firstPartyCertDns=$${FIRST_PARTY_CERT_DNS} \
		msiMockCertName=$${MSI_MOCK_CERT_NAME} \
		msiMockCertDns=$${MSI_MOCK_CERT_DNS} \
		armHelperCertName=$${ARM_HELPER_CERT_NAME} \
		armHelperCertDns=$${ARM_HELPER_CERT_DNS} && \
	APPLICATION_NAME=aro-hcp-int-fp \
	KEY_VAULT_NAME=aro-hcp-int-kv \
	CERTIFICATE_NAME=$${FIRST_PARTY_CERT_NAME} \
	ROLE_DEFINITION_NAME=$${FIRST_PARTY_ROLE_NAME} \
	SUBSCRIPTION_ID=$(shell az account show --query id --output tsv) \
	./scripts/create-sp-for-rbac.sh && \
	APPLICATION_NAME=aro-hcp-int-arm-helper \
	KEY_VAULT_NAME=aro-hcp-int-kv \
	CERTIFICATE_NAME=$${ARM_HELPER_CERT_NAME} \
	ROLE_DEFINITION_NAME='Role Based Access Control Administrator' \
	SUBSCRIPTION_ID=$(shell az account show --query id --output tsv) \
	./scripts/create-sp-for-rbac.sh && \
	az role assignment create \
		--role "Contributor" \
		--assignee "$(shell az ad app list --display-name aro-hcp-int-arm-helper --query '[*]'.appId -o tsv)" \
		--scope "/subscriptions/$(shell az account show --query id --output tsv)" \
		--only-show-errors
	APPLICATION_NAME=aro-hcp-int-msi-mock \
	KEY_VAULT_NAME=aro-hcp-int-kv \
	CERTIFICATE_NAME=$${MSI_MOCK_CERT_NAME} \
	ROLE_DEFINITION_NAME=$${MSI_MOCK_ROLE_NAME} \
	SUBSCRIPTION_ID=$(shell az account show --query id --output tsv) \
	./scripts/create-sp-for-rbac.sh && \
	az role assignment create \
		--role "Key Vault Crypto User" \
		--assignee "$(shell az ad app list --display-name aro-hcp-int-msi-mock --query '[*]'.appId -o tsv)" \
		--scope "/subscriptions/$(shell az account show --query id --output tsv)" \
		--only-show-errors

.PHONY: create-mock-identities

create-mock-identities: configurations/mock-identities.bicepparam
	az deployment group wait --created --name "aro-hcp-dev-mock-certificates" --resource-group $(GLOBAL_RESOURCEGROUP) --interval 10
	az deployment group create \
		--name "aro-hcp-dev-mock-certificates" \
		--resource-group $(GLOBAL_RESOURCEGROUP) \
		--template-file templates/mock-identities.bicep $(PROMPT_TO_CONFIRM) \
		--parameters configurations/mock-identities.bicepparam \
		--parameters e2eTestSubscription=974ebd46-8ad3-41e3-afef-7ef25fd5c371

	APPLICATION_NAME=aro-dev-first-party2 \
	KEY_VAULT_NAME=aro-hcp-dev-svc-kv \
	CERTIFICATE_NAME=firstPartyCert2 \
	ROLE_DEFINITION_NAME=dev-first-party-mock \
	SUBSCRIPTION_ID=$(shell az account show --query id --output tsv) \
	./scripts/create-sp-for-rbac.sh

	APPLICATION_NAME=aro-dev-arm-helper2 \
	KEY_VAULT_NAME=aro-hcp-dev-svc-kv \
	CERTIFICATE_NAME=armHelperCert2 \
	ROLE_DEFINITION_NAME='Role Based Access Control Administrator' \
	SUBSCRIPTION_ID=$(shell az account show --query id --output tsv) \
	./scripts/create-sp-for-rbac.sh

	az role assignment create \
		--role "Contributor" \
		--assignee "$(shell az ad app list --display-name aro-dev-arm-helper2 --query '[*]'.appId -o tsv)" \
		--scope "/subscriptions/$(shell az account show --query id --output tsv)" \
		--only-show-errors

	APPLICATION_NAME=aro-dev-msi-mock2 \
	KEY_VAULT_NAME=aro-hcp-dev-svc-kv \
	CERTIFICATE_NAME=msiMockCert2 \
	ROLE_DEFINITION_NAME=dev-msi-mock \
	SUBSCRIPTION_ID=$(shell az account show --query id --output tsv) \
	./scripts/create-sp-for-rbac.sh

	az role assignment create \
		--role "Azure Red Hat OpenShift KMS Plugin - Dev" \
		--assignee "$(shell az ad app list --display-name aro-dev-msi-mock2 --query '[*]'.appId -o tsv)" \
		--scope "/subscriptions/$(shell az account show --query id --output tsv)" \
		--only-show-errors

.PHONY: create-mock-identities

grant-e2e-access:
	az role assignment create \
		--role "dev-first-party-mock" \
		--assignee "$(shell az ad app list --display-name aro-dev-first-party2 --query '[*]'.appId -o tsv)" \
		--scope "/subscriptions/$(E2E_TESTING_SUBSCRIPTION_ID)" \
		--only-show-errors

	az role assignment create \
		--role "Contributor" \
		--assignee "$(shell az ad app list --display-name aro-dev-arm-helper2 --query '[*]'.appId -o tsv)" \
		--scope "/subscriptions/$(E2E_TESTING_SUBSCRIPTION_ID)" \
		--only-show-errors

	az role assignment create \
		--role "Role Based Access Control Administrator" \
		--assignee "$(shell az ad app list --display-name aro-dev-arm-helper2 --query '[*]'.appId -o tsv)" \
		--scope "/subscriptions/974ebd46-8ad3-41e3-afef-7ef25fd5c371" \
		--only-show-errors

	az role assignment create \
		--role "dev-msi-mock" \
		--assignee "$(shell az ad app list --display-name aro-dev-msi-mock2 --query '[*]'.appId -o tsv)" \
		--scope "/subscriptions/$(E2E_TESTING_SUBSCRIPTION_ID)" \
		--only-show-errors

	az role assignment create \
		--role "Azure Red Hat OpenShift KMS Plugin - Dev" \
		--assignee "$(shell az ad app list --display-name aro-dev-msi-mock2 --query '[*]'.appId -o tsv)" \
		--scope "/subscriptions/$(E2E_TESTING_SUBSCRIPTION_ID)" \
		--only-show-errors
.PHONY: grant-e2e-access

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
	make -C .. pipeline/Log.Infra DEPLOY_ENV=$(DEPLOY_ENV) EXTRA_ARGS="--region eastus2"
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
# Operator roles for platform workload identity
#

operator-roles:
	az deployment sub create \
	    --location westus3 \
		--name operator-roles \
		--template-file templates/dev-operator-roles.bicep \
		$(PROMPT_TO_CONFIRM) \
		--parameters configurations/dev-operator-roles.bicepparam \
		--parameters e2eTestSubscription=$(E2E_TESTING_SUBSCRIPTION_ID)
.PHONY: operator-roles

operator-roles.what-if:
	az deployment sub what-if \
	    --location westus3 \
		--name operator-roles \
		--template-file templates/dev-operator-roles.bicep \
		--parameters configurations/dev-operator-roles.bicepparam \
		--parameters e2eTestSubscription=$(E2E_TESTING_SUBSCRIPTION_ID)
.PHONY: operator-roles.what-if

#
# Common
#

what-if: global.what-if acr.what-if region.what-if svc.what-if mgmt.what-if monitoring.what-if operator-roles.what-if automation-account.what-if
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

#
# Automation Account creation
#
automation-account:
	@for env in $(AUTOMATION_ACCOUNT_ENVS); do \
		$(MAKE) automation-account-$$env; \
	done
.PHONY: automation-account

automation-account.what-if:
	@for env in $(AUTOMATION_ACCOUNT_ENVS); do \
		$(MAKE) automation-account-$$env.what-if; \
	done
.PHONY: automation-account.what-if

$(addprefix automation-account-,$(AUTOMATION_ACCOUNT_ENVS)):automation-account-%: configurations/automation-account.bicepparam
	$(eval _aa_env = $(subst automation-account-,,$@))
	$(eval _aa_resource_group = hcp-$(_aa_env)-automation-account)
	$(eval _aa_subscription_id = $($(shell tr '[:lower:]' '[:upper:]' <<< $(_aa_env))_TESTING_SUBSCRIPTION_ID))
	az group create \
		--resource-group $(_aa_resource_group) --subscription $(_aa_subscription_id) \
		--location eastus --tags persist=true && \
	az deployment group create \
		--name $(_aa_env)-automation-account \
		--resource-group $(_aa_resource_group) \
		--mode complete \
		--subscription $(_aa_subscription_id) \
		--template-file templates/automation-account.bicep \
		$(PROMPT_TO_CONFIRM) \
		--parameters configurations/automation-account.bicepparam \
		--parameters environment=$(_aa_env)
.PHONY: $(addprefix automation-account-,$(AUTOMATION_ACCOUNT_ENVS))

$(addsuffix .what-if,$(addprefix automation-account-,$(AUTOMATION_ACCOUNT_ENVS))):automation-account-%.what-if: configurations/automation-account.bicepparam
	$(eval _aa_env = $(subst .what-if,,$(subst automation-account-,,$@)))
	$(eval _aa_resource_group = hcp-$(_aa_env)-automation-account)
	$(eval _aa_subscription_id = $($(shell tr '[:lower:]' '[:upper:]' <<< $(_aa_env))_TESTING_SUBSCRIPTION_ID))
	az deployment group what-if \
		--name $(_aa_env)-automation-account \
		--resource-group $(_aa_resource_group) \
		--subscription $(_aa_subscription_id) \
		--template-file templates/automation-account.bicep \
		--parameters configurations/automation-account.bicepparam \
		--parameters environment=$(_aa_env)
.PHONY: $(addsuffix .what-if,$(addprefix automation-account-,$(AUTOMATION_ACCOUNT_ENVS)))
