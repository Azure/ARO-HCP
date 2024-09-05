SHELL := /bin/bash

AKSCONFIG ?= mgmt-cluster
CONFIG_PROFILE ?= dev
include ../dev-infrastructure/configurations/$(CONFIG_PROFILE).mk

EXTERNAL_DNS_OPERATOR_MI_CLIENT_ID ?= $(shell az identity show -g ${RESOURCEGROUP} -n external-dns --query clientId -o tsv)
ZONE_NAME ?= $(shell az network dns zone list -g ${REGIONAL_RESOURCEGROUP} --query "[?zoneType=='Public'].name" -o tsv)
AZURE_TENANT_ID ?= $(shell az account show --query tenantId --output tsv)
AZURE_SUBSCRIPTION_ID ?= $(shell az account show --query id --output tsv)

HO_IMAGE ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io/acm-d/rhtap-hypershift-operator:99a256f
EDO_IMAGE ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io/external-dns/external-dns:v0.14.2

create-edo-azure-creds:
	@echo '{' > deploy/overlays/dev/edo-azure-credentials.json
	@echo '  "tenantId": "${AZURE_TENANT_ID}",' >> deploy/overlays/dev/edo-azure-credentials.json
	@echo '  "subscriptionId": "${AZURE_SUBSCRIPTION_ID}",' >> deploy/overlays/dev/edo-azure-credentials.json
	@echo '  "resourceGroup": "${REGIONAL_RESOURCEGROUP}",' >> deploy/overlays/dev/edo-azure-credentials.json
	@echo '  "useWorkloadIdentityExtension": true,' >> deploy/overlays/dev/edo-azure-credentials.json
	@echo '  "userAssignedIdentityID": "${EXTERNAL_DNS_OPERATOR_MI_CLIENT_ID}"' >> deploy/overlays/dev/edo-azure-credentials.json
	@echo '}' >> deploy/overlays/dev/edo-azure-credentials.json

create-edo-sa-patch:
	@echo '[{"op": "add", "path": "/metadata/annotations/azure.workload.identity~1client-id", "value": "${EXTERNAL_DNS_OPERATOR_MI_CLIENT_ID}"}]' > deploy/overlays/dev/patch-serviceaccount-external-dns.json

create-domain-file:
	@echo "${ZONE_NAME}" > deploy/overlays/dev/domain.txt

create-txt-owner-id-file:
	@echo "${RESOURCEGROUP}" > deploy/overlays/dev/txt_owner_id.txt

deploy: create-edo-azure-creds create-edo-sa-patch create-domain-file create-txt-owner-id-file
	kubectl apply --server-side --force-conflicts -k deploy/crds
	kubectl apply --server-side --force-conflicts -k deploy/overlays/dev

# run this task whenever HO_IMAGE or EDO_IMAGE is updated
# commit the changes to deploy/base to the repo since we don't have podman or docker in CI
prepare-ho-manifests:
	@curl -sfLo - "https://github.com/patrickdappollonio/kubectl-slice/releases/download/v1.3.1/kubectl-slice_${shell uname -s | tr '[:upper:]' '[:lower:]'}_${shell uname -m}.tar.gz" | tar xzf - kubectl-slice
	@rm deploy/base/*
	curl https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_servicemonitors.yaml -o deploy/base/customresourcedefinition-monitoring.coreos.com_servicemonitors.yaml
	curl https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_prometheusrules.yaml -o deploy/base/customresourcedefinition-monitoring.coreos.com_prometheusrules.yaml
	curl https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_podmonitors.yaml -o deploy/base/customresourcedefinition-monitoring.coreos.com_podmonitors.yaml
	curl https://raw.githubusercontent.com/openshift/api/master/route/v1/zz_generated.crd-manifests/routes-Default.crd.yaml -o deploy/base/customresourcedefinition-routes-default.crd.yaml
	@podman run -it --rm ${HO_IMAGE} install render \
		--hypershift-image ${HO_IMAGE} \
		--enable-conversion-webhook=false \
		--external-dns-provider azure \
		--external-dns-secret external-dns-azure \
		--external-dns-domain-filter \$$\(DOMAIN\) \
		--external-dns-image ${EDO_IMAGE} \
		--external-dns-txt-owner-id \$$\(TXT_OWNER_ID\) \
		--managed-service ARO-HCP | ./kubectl-slice -f - -o deploy/base
	@rm deploy/crds/*
	@mkdir -p deploy/crds
	@mv deploy/base/customresourcedefinition*.yaml deploy/crds

	@echo "apiVersion: kustomize.config.k8s.io/v1beta1" > deploy/crds/kustomization.yml
	@echo "kind: Kustomization" >> deploy/crds/kustomization.yml
	@echo "resources:" >> deploy/crds/kustomization.yml
	@find deploy/crds -type f -name "customresourcedefinition*.yaml" ! -name "kustomization.yml" | sed 's/^deploy\/crds\//  - /' >> deploy/crds/kustomization.yml

	@echo "apiVersion: kustomize.config.k8s.io/v1beta1" > deploy/base/kustomization.yml
	@echo "kind: Kustomization" >> deploy/base/kustomization.yml
	@echo "resources:" >> deploy/base/kustomization.yml
	@find deploy/base -type f -name "*.yaml" ! -name "kustomization.yml" | sed 's/^deploy\/base\//  - /' >> deploy/base/kustomization.yml

.PHONY: create-edo-azure-creds create-edo-sa-patch create-domain-file create-txt-owner-id-file deploy unpack-ho-manifests
