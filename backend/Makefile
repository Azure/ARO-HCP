SHELL = /bin/bash

# for deploying backend into private aks cluster via invoke command
# these values must be set
AKSCONFIG ?= svc-cluster
CONFIG_PROFILE ?= dev
include ../dev-infrastructure/configurations/$(CONFIG_PROFILE).mk

COMMIT = $(shell git rev-parse --short=7 HEAD)
ARO_HCP_BASE_IMAGE ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io
ARO_HCP_BACKEND_IMAGE ?= $(ARO_HCP_BASE_IMAGE)/arohcpbackend:$(COMMIT)
CLUSTER_NAME ?=
DEPLOYMENTNAME=$(RESOURCEGROUP)

# dev-infrastructure defines this as REGION
LOCATION ?= ${REGION}

backend:
	go build -o aro-hcp-backend .

run:
	./aro-hcp-backend --location ${LOCATION} \
		--clusters-service-url http://localhost:8000

clean:
	rm -f aro-hcp-backend

image:
	pushd .. && git archive --output backend/archive.tar.gz HEAD && popd
	docker build -f "./Dockerfile" -t ${ARO_HCP_BACKEND_IMAGE} .
	rm -f archive.tar.gz

push: image
	docker push ${ARO_HCP_BACKEND_IMAGE}

deploy:
	BACKEND_MI_CLIENT_ID=$(shell az identity show \
			-g ${RESOURCEGROUP} \
			-n backend \
			--query clientId);\
	DB_NAME=$(shell az cosmosdb list -g ${RESOURCEGROUP} | jq -r '.[].name') DB_NAME=$${DB_NAME:-"none"};\
	ISTO_VERSION=$(shell az aks list --query "[?tags.clusterType == 'svc-cluster' && starts_with(resourceGroup, '${RESOURCEGROUP}')].serviceMeshProfile.istio.revisions[-1]" -o tsv) && \
	kubectl create namespace aro-hcp --dry-run=client -o json | kubectl apply -f - && \
	kubectl label namespace aro-hcp "istio.io/rev=$${ISTO_VERSION}" --overwrite=true && \
	helm upgrade --install aro-hcp-backend-dev \
		deploy/helm/backend/ \
		--set configMap.databaseName=$${DB_NAME} \
		--set configMap.databaseUrl="https://$${DB_NAME}.documents.azure.com:443/" \
		--set configMap.backendMiClientId="$${BACKEND_MI_CLIENT_ID}" \
		--set serviceAccount.workloadIdentityClientId="$${BACKEND_MI_CLIENT_ID}" \
		--set configMap.currentVersion=${ARO_HCP_BACKEND_IMAGE} \
		--set configMap.location=${LOCATION} \
		--set deployment.imageName=${ARO_HCP_BACKEND_IMAGE} \
		--namespace aro-hcp

undeploy:
	helm uninstall aro-hcp-backend-dev --namespace aro-hcp

.PHONY: backend run clean image push deploy undeploy
