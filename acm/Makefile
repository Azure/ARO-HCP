SHELL = /bin/bash

MCE_OPERATOR_BUNDLE_IMAGE ?= quay.io/zhiweiyin/backplane-operator-bundle:v2.6.0
MCE_OPERATOR_BUNDLE_FILE = mce-operator-bundle.tgz
HELM_BASE_DIR = deploy/helm
MCE_CHART_DIR = ${HELM_BASE_DIR}/multicluster-engine

deploy:
	./scripts/install-olm.sh v0.28.0
	./scripts/install-acm.sh

create-chart:
	@podman pull --arch amd64 ${MCE_OPERATOR_BUNDLE_IMAGE}
	@podman save -o ${MCE_OPERATOR_BUNDLE_FILE} ${MCE_OPERATOR_BUNDLE_IMAGE}
	rm -rf ${MCE_CHART_DIR}
	go run ../tooling/mcerepkg/main.go \
		-b ${MCE_OPERATOR_BUNDLE_FILE} \
		-o ${HELM_BASE_DIR} -s scaffold
	@rm ${MCE_OPERATOR_BUNDLE_FILE}

.PHONY: deploy create-chart
