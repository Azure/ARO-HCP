SHELL := /bin/bash

AKSCONFIG ?= mgmt-cluster
CONFIG_PROFILE ?= dev
include ../dev-infrastructure/configurations/$(CONFIG_PROFILE).mk

EXTERNAL_DNS_OPERATOR_MI_CLIENT_ID ?= $(shell az identity show -g ${RESOURCEGROUP} -n external-dns --query clientId -o tsv)
ZONE_NAME ?= $(shell az network dns zone list -g ${REGIONAL_RESOURCEGROUP} --query "[?zoneType=='Public'].name" -o tsv)
AZURE_TENANT_ID ?= $(shell az account show --query tenantId --output tsv)
AZURE_SUBSCRIPTION_ID ?= $(shell az account show --query id --output tsv)
CSI_SECRET_STORE_CLIENT_ID ?= $(shell az aks list -g ${RESOURCEGROUP} --query '[].addonProfiles.azureKeyvaultSecretsProvider.identity.clientId' -o tsv)

HO_IMAGE ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io/acm-d/rhtap-hypershift-operator:99a256f
ED_IMAGE ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io/external-dns/external-dns:v0.14.2
HO_CHART_DIR = deploy/helm/charts/hypershift-operator

# run this task whenever HO_IMAGE or EDO_IMAGE is updated
# commit the changes to deploy/base to the repo since we don't have podman or docker in CI
helm-chart:
	@rm -rf ${HO_CHART_DIR}
	@mkdir -p ${HO_CHART_DIR}/crds
	@curl -s https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_servicemonitors.yaml -o ${HO_CHART_DIR}/crds/customresourcedefinition-monitoring.coreos.com_servicemonitors.yaml
	@curl -s https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_prometheusrules.yaml -o ${HO_CHART_DIR}/crds/customresourcedefinition-monitoring.coreos.com_prometheusrules.yaml
	@curl -s https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_podmonitors.yaml -o ${HO_CHART_DIR}/crds/customresourcedefinition-monitoring.coreos.com_podmonitors.yaml
	@curl -s https://raw.githubusercontent.com/openshift/api/master/route/v1/zz_generated.crd-manifests/routes-Default.crd.yaml -o ${HO_CHART_DIR}/crds/customresourcedefinition-routes-default.crd.yaml
	@podman run -it --rm ${HO_IMAGE} install helm \
		--enable-conversion-webhook=false \
		--managed-service ARO-HCP \
		--pull-secret pull-secret.json \
		--output-dir=${HO_CHART_DIR}

deploy:
	helm upgrade --install hypershift deploy/helm \
		--create-namespace --namespace hypershift \
		--set hypershift-operator.image=${HO_IMAGE} \
		--set hypershift-operator.registryOverrides="quay.io/openshift-release-dev/ocp-v4.0-art-dev=${ARO_HCP_IMAGE_ACR}.azurecr.io/openshift/release\,quay.io/openshift-release-dev/ocp-release=${ARO_HCP_IMAGE_ACR}.azurecr.io/openshift/release-images\,${ARO_HCP_IMAGE_ACR}.redhat.io/redhat=arohcpdev.azurecr.io/redhat" \
		--set hypershift-operator.azure.keyVault.clientId=${CSI_SECRET_STORE_CLIENT_ID} \
		--set external-dns.image=${ED_IMAGE} \
		--set external-dns.txtOwnerId=${RESOURCEGROUP} \
		--set external-dns.domain=${ZONE_NAME} \
		--set external-dns.credentials.tenantId=${AZURE_TENANT_ID} \
		--set external-dns.credentials.subscriptionId=${AZURE_SUBSCRIPTION_ID} \
		--set external-dns.credentials.resourceGroup=${REGIONAL_RESOURCEGROUP} \
		--set external-dns.credentials.userAssignedIdentityID=${EXTERNAL_DNS_OPERATOR_MI_CLIENT_ID}

.PHONY: helm-chart deploy
