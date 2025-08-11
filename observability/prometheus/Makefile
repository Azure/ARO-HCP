-include ../../setup-env.mk
-include ../../helm-cmd.mk

NAMESPACE ?= "prometheus"
CHART_VERSION ?= 70.4.1
LABEL_NAMESPACE_CMD := $(if $(PROMETHEUS_NAMESPACE_LABEL),kubectl label namespace ${NAMESPACE} ${PROMETHEUS_NAMESPACE_LABEL},)

ZONE_COUNT := $(shell az account list-locations --query "[?name=='${REGION}'] | [0].availabilityZoneMappings" --output tsv | wc -l)

ifeq ($(ZONE_COUNT),0)
  TOPOLOGY_KEY := "kubernetes.azure.com/agentpool"
else
  TOPOLOGY_KEY := "failure-domain.beta.kubernetes.io/zone"
endif

deploy: pull-chart
	@kubectl create namespace ${NAMESPACE} --dry-run=client -o json | kubectl apply -f -
	$(eval PROMETHEUS_OPERATOR_DIGEST := $(subst sha256:,,$(PROMETHEUS_OPERATOR_DIGEST)))
	$(eval PROMETHEUS_SPEC_DIGEST := $(subst sha256:,,$(PROMETHEUS_SPEC_DIGEST)))
	$(eval PROMETHEUS_CONFIG_RELOADER_DIGEST := $(subst sha256:,,$(PROMETHEUS_CONFIG_RELOADER_DIGEST)))
	@${LABEL_NAMESPACE_CMD}
	${HELM_CMD} arohcp-monitor deploy/ \
		--version ${CHART_VERSION} \
		--namespace ${NAMESPACE} \
		--values=deploy/values.yaml \
		--set crds.enabled=true \
		--set kube-prometheus-stack.prometheusOperator.enabled=true \
		--set kube-prometheus-stack.prometheusOperator.image.registry=${PROMETHEUS_OPERATOR_REGISTRY} \
		--set kube-prometheus-stack.prometheusOperator.image.repository=${PROMETHEUS_OPERATOR_REPOSITORY} \
		--set kube-prometheus-stack.prometheusOperator.image.sha=${PROMETHEUS_OPERATOR_DIGEST} \
		--set kube-prometheus-stack.prometheusOperator.prometheusConfigReloader.image.registry=${PROMETHEUS_CONFIG_RELOADER_REGISTRY} \
		--set kube-prometheus-stack.prometheusOperator.prometheusConfigReloader.image.repository=${PROMETHEUS_CONFIG_RELOADER_REPOSITORY} \
		--set kube-prometheus-stack.prometheusOperator.prometheusConfigReloader.image.sha=${PROMETHEUS_CONFIG_RELOADER_DIGEST} \
		--set prometheus.serviceAccount.managedIdentity=$${PROMETHEUS_UAMI_CLIENT_ID} \
		--set prometheusSpec.image.registry=${PROMETHEUS_SPEC_REGISTRY} \
		--set prometheusSpec.image.repository=${PROMETHEUS_SPEC_REPOSITORY} \
		--set prometheusSpec.image.sha=${PROMETHEUS_SPEC_DIGEST} \
		--set prometheusSpec.version=${PROMETHEUS_SPEC_VERSION} \
		--set prometheus.prometheusSpec.shards=${PROMETHEUS_SPEC_SHARDS} \
		--set prometheus.prometheusSpec.replicas=${PROMETHEUS_SPEC_REPLICAS} \
		--set prometheusSpec.externalLabels.cluster=${CLUSTER_NAME} \
		--set prometheusSpec.remoteWriteUrl="$${DCR_REMOTE_WRITE_URL}" \
		--set prometheusSpec.hcpRemoteWriteUrl="$${HCP_DCR_REMOTE_WRITE_URL}" \
		--set prometheusSpec.topologyKey=${TOPOLOGY_KEY} \
		--set prometheusSpec.maximumStartupDurationSeconds=360 \
		--set environment=${CS_ENVIRONMENT}
.PHONY: deploy

pull-chart:
	helm repo add prometheus-community https://prometheus-community.github.io/helm-charts;
	helm repo update;
	helm dependency build deploy/
.PHONY: pull-chart
