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
MCE_CONFIG_DIR = ${HELM_BASE_DIR}/multicluster-engine-config
MCE_NS = multicluster-engine
POLICY_HELM_REPO = https://github.com/stolostron/mce-install-kube.git

deploy:
	@kubectl create namespace ${MCE_NS} --dry-run=client -o json | kubectl apply -f -
	helm upgrade --install --wait \
		mce ${MCE_CHART_DIR} \
		--namespace ${MCE_NS} \
		--set imageRegistry=${REGISTRY}
	helm upgrade --install --wait \
		mce-config ${MCE_CONFIG_DIR} \
		--namespace ${MCE_NS}

helm-chart:
	@podman pull --arch amd64 ${MCE_OPERATOR_BUNDLE_IMAGE}
	@podman save -o ${MCE_OPERATOR_BUNDLE_FILE} ${MCE_OPERATOR_BUNDLE_IMAGE}
	rm -rf ${MCE_CHART_DIR}
	go run ../tooling/mcerepkg/main.go \
		-b ${MCE_OPERATOR_BUNDLE_FILE} \
		-o ${HELM_BASE_DIR} -s scaffold \
		-l ${MCE_OPERATOR_BUNDLE_IMAGE}
	@rm ${MCE_OPERATOR_BUNDLE_FILE}

	@echo "Cloning remote mce-install-kube repository..."
	TMP_DIR=$$(mktemp -d) && \
	git clone ${POLICY_HELM_REPO} $$TMP_DIR && \
	rm -rf ${MCE_CONFIG_DIR}/charts && \
	mkdir -p ${MCE_CONFIG_DIR}/charts && \
	cp -r $$TMP_DIR/policy ${MCE_CONFIG_DIR}/charts && \
	rm -rf $$TMP_DIR
	@echo "Helm chart imported successfully."

import-chart:


.PHONY: deploy helm-chart
