SHELL = /bin/bash

AKSCONFIG ?= mgmt-cluster
CONFIG_PROFILE ?= dev
include ../dev-infrastructure/configurations/$(CONFIG_PROFILE).mk

MCE_OPERATOR_BUNDLE_IMAGE ?= quay.io/acm-d/mce-operator-bundle:v2.7.0-110
REGISTRY ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io/acm-d-mce
# once MCE 2.7 is released, we can find the official bundle image here
# https://catalog.redhat.com/software/containers/multicluster-engine/mce-operator-bundle/6160406290fb938ecf6009c6

MCE_OPERATOR_BUNDLE_FILE = mce-operator-bundle.tgz
HELM_BASE_DIR = deploy/helm
MCE_CHART_DIR = ${HELM_BASE_DIR}/multicluster-engine
MCE_NS = multicluster-engine

deploy:
	@kubectl create namespace ${MCE_NS} --dry-run=client -o json | kubectl apply -f -
	helm upgrade --install mce deploy/helm/multicluster-engine \
		--namespace ${MCE_NS} \
		--set imageRegistry=${REGISTRY}

create-chart:
	@podman pull --arch amd64 ${MCE_OPERATOR_BUNDLE_IMAGE}
	@podman save -o ${MCE_OPERATOR_BUNDLE_FILE} ${MCE_OPERATOR_BUNDLE_IMAGE}
	rm -rf ${MCE_CHART_DIR}
	go run ../tooling/mcerepkg/main.go \
		-b ${MCE_OPERATOR_BUNDLE_FILE} \
		-o ${HELM_BASE_DIR} -s scaffold
	@rm ${MCE_OPERATOR_BUNDLE_FILE}

.PHONY: deploy create-chart
