-include ../setup-env.mk
-include ../helm-cmd.mk

MCE_OPERATOR_BUNDLE_IMAGE ?= ${MCE_BUNDLE_REGISTRY}/${MCE_BUNDLE_REPOSITORY}@${MCE_BUNDLE_DIGEST}
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

export MCE_NS
export MCE_CHART_DIR
export MCE_CONFIG_DIR
export MCE_PAUSE_RECONCILIATION
export REGISTRY
export DRY_RUN
export HELM_CMD

deploy:
	./deploy.sh

scale:
ifneq ($(DRY_RUN),true)
	@if [  "${MCE_PAUSE_RECONCILIATION}" != "true" ]; then \
		echo "MCE_PAUSE_RECONCILIATION is not set, skipping scale down"; \
		exit 0; \
	fi; \
	echo "Waiting for MCE multiclusterengine to reach Paused status..."; \
	kubectl wait --for=jsonpath='{.status.phase}'=Paused mce multiclusterengine -n ${MCE_NS} --timeout=600s; \
	echo "Phase is Paused, scaling down all deployments to 0..."; \
	kubectl -n ${MCE_NS} scale deployment/multicluster-engine-operator --replicas=0; \
	echo "All deployments scaled down to 0 replicas"
endif

deploy-policies:
	${HELM_CMD} \
		policy ${POLICY_CHART_DIR} \
		--namespace ${POLICY_NS}

helm-chart:
	@podman pull --arch amd64 ${MCE_OPERATOR_BUNDLE_IMAGE}
	@podman save -o ${MCE_OPERATOR_BUNDLE_FILE} ${MCE_OPERATOR_BUNDLE_IMAGE}
	rm -rf ${MCE_CHART_DIR}
	go run ../tooling/olm-bundle-repkg/main.go \
		-c olm-bundle-repkg-config.yaml \
		-b oci://${MCE_OPERATOR_BUNDLE_FILE} \
		-o ${HELM_BASE_DIR} -s scaffold \
		-l ${MCE_OPERATOR_BUNDLE_IMAGE}
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
