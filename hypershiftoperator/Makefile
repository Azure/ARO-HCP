SHELL := /bin/bash

RESOURCEGROUP ?= aro-hcp-${AKSCONFIG}-$(USER)
EXTERNAL_DNS_OPERATOR_MI_CLIENT_ID ?= $(shell az identity show -g ${RESOURCEGROUP} -n external-dns --query clientId -o tsv)
ZONE_RESOURCE_GROUP ?= $(shell az role assignment list --assignee ${EXTERNAL_DNS_OPERATOR_MI_CLIENT_ID} --all --query "[?roleDefinitionName == 'DNS Zone Contributor'].resourceGroup | [0]" -o tsv)
ZONE_NAME ?= $(shell az network dns zone list -g ${ZONE_RESOURCE_GROUP} --query "[?zoneType=='Public'].name" -o tsv)
AZURE_TENANT_ID ?= $(shell az account show --query tenantId --output tsv)
AZURE_SUBSCRIPTION_ID ?= $(shell az account show --query id --output tsv)

HO_IMAGE ?= devarohcp.azurecr.io/acm-d/rhtap-hypershift-operator:84136f3
EDO_IMAGE ?= devarohcp.azurecr.io/external-dns/external-dns:v0.14.2

create-edo-azure-creds:
	@echo '{' > deploy/overlays/dev/edo-azure-credentials.json
	@echo '  "tenantId": "${AZURE_TENANT_ID}",' >> deploy/overlays/dev/edo-azure-credentials.json
	@echo '  "subscriptionId": "${AZURE_SUBSCRIPTION_ID}",' >> deploy/overlays/dev/edo-azure-credentials.json
	@echo '  "resourceGroup": "${ZONE_RESOURCE_GROUP}",' >> deploy/overlays/dev/edo-azure-credentials.json
	@echo '  "useWorkloadIdentityExtension": true,' >> deploy/overlays/dev/edo-azure-credentials.json
	@echo '  "userAssignedIdentityID": "${EXTERNAL_DNS_OPERATOR_MI_CLIENT_ID}"' >> deploy/overlays/dev/edo-azure-credentials.json
	@echo '}' >> deploy/overlays/dev/edo-azure-credentials.json

create-edo-sa-patch:
	@echo '[{"op": "add", "path": "/metadata/annotations/azure.workload.identity~1client-id", "value": "${EXTERNAL_DNS_OPERATOR_MI_CLIENT_ID}"}]' > deploy/overlays/dev/patch-serviceaccount-external-dns.json

create-domain-file:
	@echo "${ZONE_NAME}" > deploy/overlays/dev/domain.txt

deploy: create-edo-azure-creds create-edo-sa-patch create-domain-file
	pushd deploy/overlays/dev && \
	kubectl apply --server-side --force-conflicts -k .

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
		--external-dns-txt-owner-id ARO-HCP \
		--managed-service ARO-HCP | ./kubectl-slice -f - -o deploy/base
	@echo "apiVersion: kustomize.config.k8s.io/v1beta1" > deploy/base/kustomization.yml
	@echo "kind: Kustomization" >> deploy/base/kustomization.yml
	@echo "resources:" >> deploy/base/kustomization.yml
	@find deploy/base -type f \( -name "*.yml" -o -name "*.yaml" \) ! -name "kustomization.yml" | sed 's/^deploy\/base\//  - /' >> deploy/base/kustomization.yml

.PHONY: create-edo-azure-creds deploy unpack-ho-manifests
