SHELL = /bin/bash

TENANT_ID=$(shell az account show --query tenantId --output tsv)
ZONE_RESOURCE_ID ?= $(az network dns zone show -n {{ .regionalDNSSubdomain }}.{{ .baseDnsZoneName }} -g {{ .regionRG }} --query id -o tsv)

FPA_CERT_NAME ?= firstPartyCert

deploy:
	sed -e "s#ZONE_RESOURCE_ID#${ZONE_RESOURCE_ID}#g" -e "s/REGION/{{ .region }}/g" -e "s/CONSUMER_NAME/{{ .maestroConsumerName }}/g" deploy/mvp-provisioning-shards.yml > deploy/tmp-provisioning-shard.yml

	ISTO_VERSION=$(shell az aks show -n {{ .aksName }} -g {{ .serviceClusterRG }} --query serviceMeshProfile.istio.revisions[-1] -o tsv) && \
	oc process --local -f deploy/openshift-templates/arohcp-namespace-template.yml \
	  -p ISTIO_VERSION=$${ISTO_VERSION} | oc apply -f -
	kubectl apply -f deploy/istio.yml

	oc process --local -f deploy/openshift-templates/arohcp-db-template.yml | oc apply -f -
	oc process --local -f deploy/openshift-templates/arohcp-secrets-template.yml \
	  -p PROVISION_SHARDS_CONFIG="$$( base64 -i deploy/tmp-provisioning-shard.yml)" | oc apply -f -

	CS_MI_CLIENT_ID=$(shell az identity show -g "{{ .serviceClusterRG }}" -n clusters-service --query clientId -o tsv) && \
	CS_SERVICE_PRINCIPAL_CREDS_BASE64='$(shell az keyvault secret show --vault-name "{{ .serviceKeyVaultName }}" --name "aro-hcp-dev-sp-cs" | jq .value -r | base64 | tr -d '\n')' && \
	OIDC_BLOB_SERVICE_ENDPOINT=$(shell az storage account show -n {{ .oidcStorageAccountName }} -g {{ .serviceClusterRG }} --query primaryEndpoints.blob -o tsv) && \
	OIDC_WEB_SERVICE_ENDPOINT=$(shell az storage account show -n {{ .oidcStorageAccountName }} -g {{ .serviceClusterRG }} --query primaryEndpoints.web -o tsv) && \
	oc process --local -f deploy/openshift-templates/arohcp-service-template.yml \
	  -p AZURE_CS_MI_CLIENT_ID=$${CS_MI_CLIENT_ID} \
	  -p TENANT_ID=${TENANT_ID} \
	  -p REGION={{ .region }} \
	  -p SERVICE_KEYVAULT_NAME={{ .serviceKeyVaultName }} \
	  -p CS_SERVICE_PRINCIPAL_CREDS_BASE64=$${CS_SERVICE_PRINCIPAL_CREDS_BASE64} \
	  -p IMAGE_REGISTRY={{ .acrName }}.azurecr.io \
	  -p IMAGE_REPOSITORY={{ .clusterServiceImageRepo }} \
	  -p AZURE_FIRST_PARTY_APPLICATION_CLIENT_ID={{ .firstPartyAppClientId }} \
	  -p FPA_CERT_NAME=${FPA_CERT_NAME} \
	  -p IMAGE_TAG={{ .clusterServiceImageTag }} | oc apply -f -

deploy-pr-env-deps:
	oc process --local -f deploy/integration/cluster-service-namespace.yaml \
		-p CLIENT_ID=${CS_MI_CLIENT_ID} | oc apply -f -

# for local development
provision-shard:
	sed -e "s#ZONE_RESOURCE_ID#${ZONE_RESOURCE_ID}#g" -e "s/REGION/{{ .region }}/g" -e "s/CONSUMER_NAME/{{ .maestroConsumerName }}/g" deploy/dev-provisioning-shards.yml

.PHONY: deploy deploy-integ provision-shard
