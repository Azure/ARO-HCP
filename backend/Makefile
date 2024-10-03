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

.PHONY: backend run clean image push
