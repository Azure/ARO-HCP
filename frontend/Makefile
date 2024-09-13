SHELL = /bin/bash

# for deploying frontend into private aks cluster via invoke command
# these values must be set
AKSCONFIG ?= svc-cluster
CONFIG_PROFILE ?= dev
include ../dev-infrastructure/configurations/$(CONFIG_PROFILE).mk

COMMIT = $(shell git rev-parse --short=7 HEAD)
ARO_HCP_BASE_IMAGE ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io
ARO_HCP_FRONTEND_IMAGE ?= $(ARO_HCP_BASE_IMAGE)/arohcpfrontend:$(COMMIT)
CLUSTER_NAME ?=
DEPLOYMENTNAME=$(RESOURCEGROUP)

# dev-infrastructure defines this as REGION
LOCATION ?= ${REGION}

frontend:
	go build -o aro-hcp-frontend .

run:
	./aro-hcp-frontend --use-cache --location ${LOCATION} \
		--clusters-service-url http://localhost:8000 \
		--cluster-service-provision-shard 1 \
		--cluster-service-noop-provision \
		--cluster-service-noop-deprovision

clean:
	rm -f aro-hcp-frontend

build-push: image push

image:
	pushd .. && git archive --output frontend/archive.tar.gz HEAD && popd
	docker build -f "./Dockerfile" -t ${ARO_HCP_FRONTEND_IMAGE} .
	rm -f archive.tar.gz

push: image
	docker push ${ARO_HCP_FRONTEND_IMAGE}

deploy: 
	FRONTEND_MI_CLIENT_ID=$(shell az identity show \
			-g ${RESOURCEGROUP} \
			-n frontend \
			--query clientId);\
	DB_NAME=$(shell az cosmosdb list -g ${RESOURCEGROUP} | jq -r '.[].name') DB_NAME=$${DB_NAME:-"none"};\
	ISTO_VERSION=$(shell az aks list --query "[?tags.clusterType == 'svc-cluster' && starts_with(resourceGroup, '${RESOURCEGROUP}')].serviceMeshProfile.istio.revisions[-1]" -o tsv) && \
	kubectl create namespace aro-hcp --dry-run=client -o json | kubectl apply -f - && \
	kubectl label namespace aro-hcp "istio.io/rev=$${ISTO_VERSION}" --overwrite=true && \
	helm upgrade --install aro-hcp-dev \
		deploy/helm/frontend/ \
		--set configMap.databaseName=$${DB_NAME} \
		--set configMap.databaseUrl="https://$${DB_NAME}.documents.azure.com:443/" \
		--set configMap.frontendMiClientId="$${FRONTEND_MI_CLIENT_ID}" \
		--set serviceAccount.workloadIdentityClientId="$${FRONTEND_MI_CLIENT_ID}" \
		--set configMap.currentVersion=${ARO_HCP_FRONTEND_IMAGE} \
		--set configMap.location=${LOCATION} \
		--set deployment.imageName=${ARO_HCP_FRONTEND_IMAGE} \
		--namespace aro-hcp

undeploy:
	helm uninstall aro-hcp-dev --namespace aro-hcp

smoke-tests:
	go test -v -count 1 ./utils/frontend_smoke_test.go

.PHONY: frontend clean image deploy undeploy
