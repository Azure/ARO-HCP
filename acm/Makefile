-include ../setup-env.mk
-include ../helm-cmd.mk

ACM_VERSION ?= 2.13.1
ACM_OPERATOR_BUNDLE_IMAGE ?= registry.redhat.io/rhacm2/acm-operator-bundle:v2.13.1-2
MCE_OPERATOR_BUNDLE_IMAGE ?= registry.redhat.io/multicluster-engine/mce-operator-bundle:v2.8.1-12
REGISTRY ?= ${ARO_HCP_IMAGE_ACR}.azurecr.io

MCE_OPERATOR_BUNDLE_FILE = mce-operator-bundle.tgz
HELM_BASE_DIR = deploy/helm
MCE_CHART_DIR = ${HELM_BASE_DIR}/multicluster-engine
MCE_NS = multicluster-engine
POLICY_NS = open-cluster-management-policies


export MCE_NS
export MCE_CHART_DIR
export MCE_PAUSE_RECONCILIATION
export REGISTRY
export DRY_RUN
export HELM_CMD
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
	${HELM_CMD} \
		policy ${POLICY_CHART_DIR} \
		--namespace ${POLICY_NS}

policy-helm-chart:
	./update-policy-chart.sh

helm-chart: policy-helm-chart
	@podman pull --arch amd64 ${MCE_OPERATOR_BUNDLE_IMAGE}
	@podman save -o ${MCE_OPERATOR_BUNDLE_FILE} ${MCE_OPERATOR_BUNDLE_IMAGE}
	rm -rf ${MCE_CHART_DIR}
	go run ../tooling/mcerepkg/main.go \
		-b ${MCE_OPERATOR_BUNDLE_FILE} \
		-o ${HELM_BASE_DIR} -s scaffold \
		-l oci://${MCE_OPERATOR_BUNDLE_IMAGE}
	@rm ${MCE_OPERATOR_BUNDLE_FILE}


.PHONY: deploy helm-chart
