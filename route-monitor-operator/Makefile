-include ../setup-env.mk
-include ../helm-cmd.mk

NAMESPACE ?= openshift-route-monitor-operator

HELM_BASE_DIR = deploy/helm
RMO_CHART_DIR ?= ${HELM_BASE_DIR}/route-monitor-operator
RMO_OPERATOR_BUNDLE_REPO ?= https://gitlab.cee.redhat.com/service/saas-route-monitor-operator-bundle
RMO_OPERATOR_BUNDLE_PATH ?= route-monitor-operator/${BUNDLE_VERSION}
RMO_OPERATOR_BUNDLE_BRANCH ?= production
IMAGE_REGISTRY = ${ACR_NAME}.azurecr.io

deploy:
	kubectl create namespace ${NAMESPACE} --dry-run=client -o json | kubectl apply -f - && \
	KUBE_VERSION=$(shell kubectl version --output=json | jq -r '.serverVersion.gitVersion' | sed 's/^v//') && \
	${HELM_CMD} route-monitor-operator \
		${RMO_CHART_DIR} \
		--set imageRegistryOperator=${IMAGE_REGISTRY} \
		--set imageRepositoryOperator=${OPERATOR_IMAGE_REPOSITORY} \
		--set imageDigestOperator=$(subst sha256:,,$(OPERATOR_IMAGE_DIGEST)) \
		--set imageRegistryManager=${IMAGE_REGISTRY} \
		--set imageRepositoryManager=${OPERATOR_IMAGE_REPOSITORY} \
		--set imageDigestManager=$(subst sha256:,,$(OPERATOR_IMAGE_DIGEST)) \
		--set imageRegistryBlackbox=${IMAGE_REGISTRY} \
		--set imageRepositoryBlackbox=${BLACKBOX_IMAGE_REPOSITORY} \
		--set imageDigestBlackbox=$(subst sha256:,,$(BLACKBOX_IMAGE_DIGEST)) \
		--namespace ${NAMESPACE}
.PHONY: deploy

helm-chart:
	$(eval BUNDLE_TMP_PATH := $(shell mktemp -d tmp-rmo-bundle.XXX))
	git clone -b ${RMO_OPERATOR_BUNDLE_BRANCH} --depth=1 --single-branch -- ${RMO_OPERATOR_BUNDLE_REPO} ${BUNDLE_TMP_PATH}
	rm -rf ${RMO_CHART_DIR}
	go run ../tooling/olm-bundle-repkg/main.go \
		-b file://${BUNDLE_TMP_PATH}/${RMO_OPERATOR_BUNDLE_PATH} \
		-l ${RMO_OPERATOR_BUNDLE_REPO}/${RMO_OPERATOR_BUNDLE_PATH} \
		-c olm-bundle-repkg-config.yaml \
		-s scaffold \
		-o ${HELM_BASE_DIR}
	rm -rf ${BUNDLE_TMP_PATH}
	# TMP Fix: Add runAsUser to route-monitor-operator-controller-manager Deployment
	yq eval '.spec.template.spec.containers[0].securityContext.runAsUser = 65532' \
		-i ${RMO_CHART_DIR}/templates/route-monitor-operator-controller-manager.deployment.yaml
	make -C .. yamlfmt
