SHELL := /bin/bash

RESOURCEGROUP ?= aro-hcp-${AKSCONFIG}-$(USER)
LOCATION ?= $(shell az group show -n ${RESOURCEGROUP} --query location)
EXTERNAL_DNS_OPERATOR_MI_CLIENT_ID := $(shell az identity show -g ${RESOURCEGROUP} -n external-dns-${LOCATION} --query clientId -o tsv)
ZONE_RESOURCE_GROUP ?= $(shell az role assignment list --assignee ${EXTERNAL_DNS_OPERATOR_MI_CLIENT_ID} --all --query "[?roleDefinitionName == 'DNS Zone Contributor'].resourceGroup | [0]" -o tsv)
ZONE_NAME ?= $(shell az network dns zone list -g ${ZONE_RESOURCE_GROUP} --query "[?zoneType=='Public'].name" -o tsv)

deploy-crds:
	oc apply -f https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_servicemonitors.yaml
	oc apply -f https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_prometheusrules.yaml
	oc apply -f https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_podmonitors.yaml
	oc apply -f https://raw.githubusercontent.com/openshift/api/master/route/v1/zz_generated.crd-manifests/routes-Default.crd.yaml

deploy: deploy-crds unpack-ho-manifests
	AZURE_TENANT_ID=$(shell az account show --query tenantId --output tsv) && \
	AZURE_SUBSCRIPTION_ID=$(shell az account show --query id --output tsv) && \
	oc process --local -f deploy/openshift-templates/external-dns-azure-secret.yml \
	  -p AZURE_TENANT_ID=$${AZURE_TENANT_ID} \
	  -p AZURE_SUBSCRIPTION_ID=$${AZURE_SUBSCRIPTION_ID} \
	  -p EXTERNAL_DNS_OPERATOR_MI_CLIENT_ID=${EXTERNAL_DNS_OPERATOR_MI_CLIENT_ID} \
	  -p ZONE_RESOURCE_GROUP=${ZONE_RESOURCE_GROUP} | oc apply -n hypershift -f - && \
	pushd deploy/overlays/dev && \
	jq --arg mi_client_id "${EXTERNAL_DNS_OPERATOR_MI_CLIENT_ID}" '.[0].value = $$mi_client_id' patch-templates/patch-serviceaccount-external-dns.json > tmp/patch-serviceaccount-external-dns.json && \
	kubectl apply --server-side --force-conflicts -k .

unpack-ho-manifests:
	@curl -sfLo - "https://github.com/patrickdappollonio/kubectl-slice/releases/download/v1.3.1/kubectl-slice_${shell uname -s | tr '[:upper:]' '[:lower:]'}_${shell uname -m}.tar.gz" | tar xzf - kubectl-slice
	@rm deploy/base/*
	@podman run -it --rm quay.io/acm-d/rhtap-hypershift-operator:0f9f686 install render \
		--enable-conversion-webhook false \
		--external-dns-provider azure \
		--external-dns-secret external-dns-azure \
		--external-dns-domain-filter ${ZONE_NAME} \
		--external-dns-image devarohcp.azurecr.io/edo/external-dns-rhel8:1.0.1-4 \
		--managed-service ARO-HCP | ./kubectl-slice -f - -o deploy/base
	@echo "resources:" > deploy/base/kustomization.yml
	@find deploy/base -type f \( -name "*.yml" -o -name "*.yaml" \) ! -name "kustomization.yml" | sed 's/^deploy\/base\//  - /' >> deploy/base/kustomization.yml

.PHONY: deploy-crds deploy unpack-ho-manifests
