-include ../setup-env.mk

ACM_OPERATOR_BUNDLE_IMAGE ?= ${ACM_BUNDLE_REGISTRY}/${ACM_BUNDLE_REPOSITORY}@${ACM_BUNDLE_DIGEST}
MCE_OPERATOR_BUNDLE_IMAGE ?= ${MCE_BUNDLE_REGISTRY}/${MCE_BUNDLE_REPOSITORY}@${MCE_BUNDLE_DIGEST}
REGISTRY ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io

MCE_OPERATOR_BUNDLE_FILE = mce-operator-bundle.tgz
HELM_BASE_DIR = deploy/helm
MCE_CHART_DIR = ${HELM_BASE_DIR}/multicluster-engine
MCE_CRD_CHART_DIR = ${HELM_BASE_DIR}/multicluster-engine-crds
MCE_CONFIG_DIR = ${HELM_BASE_DIR}/multicluster-engine-config
MCE_NS = multicluster-engine
POLICY_NS = open-cluster-management-policies


export MCE_NS
export MCE_CHART_DIR
export MCE_CRD_CHART_DIR
export MCE_CONFIG_DIR
export MCE_PAUSE_RECONCILIATION
export REGISTRY
export DRY_RUN
export ACM_VERSION
export ACM_OPERATOR_BUNDLE_IMAGE

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
	../hack/helm.sh policy deploy/helm/policies ${POLICY_NS}

policy-helm-chart:
	./update-policy-chart.sh "${MCE_CONFIG_DIR}/charts/policy"

mce-helm-chart:
	@podman pull --arch amd64 ${MCE_OPERATOR_BUNDLE_IMAGE}
	@podman save -o ${MCE_OPERATOR_BUNDLE_FILE} ${MCE_OPERATOR_BUNDLE_IMAGE}
	rm -rf ${MCE_CHART_DIR}
	go run ../tooling/olm-bundle-repkg/main.go \
		--crd-chart-name multicluster-engine-crds \
		-c olm-bundle-repkg-config.yaml \
		-b oci://${MCE_OPERATOR_BUNDLE_FILE} \
		-o ${HELM_BASE_DIR} -s scaffold \
		-l ${MCE_OPERATOR_BUNDLE_IMAGE}
	@rm ${MCE_OPERATOR_BUNDLE_FILE}

helm-charts: mce-helm-chart policy-helm-chart
	make -C ../ yamlfmt

.PHONY: deploy deploy-policies scale helm-charts mce-helm-chart policy-helm-chart
