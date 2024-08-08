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

kustomize-update:
	pushd deploy/overlays/dev;\
	FRONTEND_MI_CLIENT_ID=$(shell az identity show \
			-g ${RESOURCEGROUP} \
			-n frontend \
			--query clientId);\
	DB_NAME=$(shell az cosmosdb list -g ${RESOURCEGROUP} | jq -r '.[].name') DB_NAME=$${DB_NAME:-"none"};\
	kustomize edit set configmap frontend-config \
		--from-literal=DB_NAME="$${DB_NAME}" \
		--from-literal=DB_URL="https://$${DB_NAME}.documents.azure.com:443/" \
		--from-literal=FRONTEND_MI_CLIENT_ID="$${FRONTEND_MI_CLIENT_ID}" \
		--from-literal=CURRENT_VERSION=${ARO_HCP_FRONTEND_IMAGE} \
		--from-literal=LOCATION="${LOCATION}"

deploy: kustomize-update
	kubectl apply -k deploy/overlays/dev

undeploy:
	kubectl delete -k deploy/overlays/dev

smoke-tests:
	go test -v -count 1 ./utils/frontend_smoke_test.go

.PHONY: frontend clean image deploy undeploy
