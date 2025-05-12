-include ../setup-env.mk
-include ../helm-cmd.mk

MCE_OPERATOR_BUNDLE_IMAGE ?= registry.redhat.io/multicluster-engine/mce-operator-bundle:v2.8.1-12
REGISTRY ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io

MCE_OPERATOR_BUNDLE_FILE = mce-operator-bundle.tgz
HELM_BASE_DIR = deploy/helm
MCE_CHART_DIR = ${HELM_BASE_DIR}/multicluster-engine
POLICY_CHART_DIR = ${HELM_BASE_DIR}/policies
MCE_CONFIG_DIR = ${HELM_BASE_DIR}/multicluster-engine-config
MCE_NS = multicluster-engine
POLICY_NS = open-cluster-management-policies
POLICY_HELM_REPO = https://github.com/stolostron/mce-install-kube.git
POLICY_HELM_REPO_BRANCH = release-2.12

deploy:
	@kubectl create namespace ${MCE_NS} --dry-run=client -o json | kubectl apply -f -
	# Ensure smooth upgrade from mce 2.7.0 to 2.8.1
	- helm uninstall --ignore-not-found \
		clc-state-metrics \
		--namespace ${MCE_NS}
	${HELM_CMD} \
		mce ${MCE_CHART_DIR} \
		--namespace ${MCE_NS} \
		--set imageRegistry=${REGISTRY}
	${HELM_CMD} \
		mce-config ${MCE_CONFIG_DIR} \
		--namespace ${MCE_NS} \
		--set global.registryOverride=${REGISTRY}
	$(MAKE) deploy-policies

deploy-policies:
	${HELM_CMD} \
		policy ${POLICY_CHART_DIR} \
		--namespace ${POLICY_NS}

helm-chart:
	@podman pull --arch amd64 ${MCE_OPERATOR_BUNDLE_IMAGE}
	@podman save -o ${MCE_OPERATOR_BUNDLE_FILE} ${MCE_OPERATOR_BUNDLE_IMAGE}
	rm -rf ${MCE_CHART_DIR}
	go run ../tooling/mcerepkg/main.go \
		-b ${MCE_OPERATOR_BUNDLE_FILE} \
		-o ${HELM_BASE_DIR} -s scaffold \
		-l oci://${MCE_OPERATOR_BUNDLE_IMAGE}
	@rm ${MCE_OPERATOR_BUNDLE_FILE}

	@echo "Cloning remote mce-install-kube repository..."
	TMP_DIR=$$(mktemp -d) && \
	git clone --branch ${POLICY_HELM_REPO_BRANCH} ${POLICY_HELM_REPO} $$TMP_DIR && \
	rm -rf ${MCE_CONFIG_DIR}/charts && \
	mkdir -p ${MCE_CONFIG_DIR}/charts && \
	cp -r $$TMP_DIR/policy ${MCE_CONFIG_DIR}/charts && \
	rm -rf $$TMP_DIR
	@echo "Helm chart imported successfully."

.PHONY: deploy helm-chart
