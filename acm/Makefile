-include ../setup-templatize-env.mk

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

policy-helm-chart:
	./update-policy-chart.sh "${MCE_CONFIG_DIR}/charts/policy"

mce-helm-chart:
	@skopeo copy --override-arch amd64 docker://${MCE_OPERATOR_BUNDLE_IMAGE} docker-archive:${MCE_OPERATOR_BUNDLE_FILE}
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

.PHONY: helm-charts mce-helm-chart policy-helm-chart
