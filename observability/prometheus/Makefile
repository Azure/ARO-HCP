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
	@${LABEL_NAMESPACE_CMD}
	DCE_METRICS_INGESTION_URL=$(shell az monitor data-collection endpoint list -g ${RESOURCE_GROUP} --query "[?tags.purpose=='aks'].metricsIngestion.endpoint" -o tsv) && \
	SERVICE_DCR_IMMUTABLE_ID=$(shell az monitor data-collection rule list -g ${RESOURCE_GROUP} --query "[?tags.purpose=='services'].immutableId" -o tsv) && \
	HCP_DCR_IMMUTABLE_ID=$(shell az monitor data-collection rule list -g ${RESOURCE_GROUP} --query "[?tags.purpose=='hcp'].immutableId" -o tsv) && \
	HCP_REMOTE_WRITE_URL=$$(if [ "$${HCP_DCR_IMMUTABLE_ID}" != "" ]; then echo "$${DCE_METRICS_INGESTION_URL}/dataCollectionRules/$${HCP_DCR_IMMUTABLE_ID}/streams/Microsoft-PrometheusMetrics/api/v1/write?api-version=2023-04-24"; else echo ""; fi) && \
	PROMETHEUS_WI_IDENTITY_CLIENT_ID=$(shell az identity show -g ${RESOURCEGROUP} -n prometheus --query clientId -o tsv) && \
	${HELM_CMD} arohcp-monitor deploy/ \
		--version ${CHART_VERSION} \
		--namespace ${NAMESPACE} \
		--values=deploy/values.yaml \
		--set crds.enabled=true \
		--set kube-prometheus-stack.prometheusOperator.enabled=true \
		--set kube-prometheus-stack.prometheusOperator.image.registry=${PROMETHEUS_OPERATOR_REGISTRY} \
		--set kube-prometheus-stack.prometheusOperator.image.repository=${PROMETHEUS_OPERATOR_REPOSITORY} \
		--set kube-prometheus-stack.prometheusOperator.image.sha=${PROMETHEUS_OPERATOR_DIGEST} \
		--set kube-prometheus-stack.prometheusOperator.prometheusConfigReloader.image.registry=mcr.microsoft.com/oss/v2 \
		--set kube-prometheus-stack.prometheusOperator.prometheusConfigReloader.image.repository=prometheus/prometheus-config-reloader \
		--set kube-prometheus-stack.prometheusOperator.prometheusConfigReloader.image.sha="b112cdc776c740261d812ab544261b781f9cb3520d7b400a353993d3be9c6df1" \
		--set prometheus.serviceAccount.managedIdentity=$${PROMETHEUS_WI_IDENTITY_CLIENT_ID} \
		--set prometheusSpec.image.registry=${PROMETHEUS_SPEC_REGISTRY} \
		--set prometheusSpec.image.repository=${PROMETHEUS_SPEC_REPOSITORY} \
		--set prometheusSpec.image.sha=${PROMETHEUS_SPEC_DIGEST} \
		--set prometheusSpec.version=${PROMETHEUS_SPEC_VERSION} \
		--set prometheus.prometheusSpec.shards=${PROMETHEUS_SPEC_SHARDS} \
		--set prometheus.prometheusSpec.replicas=${PROMETHEUS_SPEC_REPLICAS} \
		--set prometheusSpec.externalLabels.cluster=${CLUSTER_NAME} \
		--set prometheusSpec.remoteWriteUrl="$${DCE_METRICS_INGESTION_URL}/dataCollectionRules/$${SERVICE_DCR_IMMUTABLE_ID}/streams/Microsoft-PrometheusMetrics/api/v1/write?api-version=2023-04-24" \
		--set prometheusSpec.hcpRemoteWriteUrl="$${HCP_REMOTE_WRITE_URL}" \
		--set prometheusSpec.topologyKey=${TOPOLOGY_KEY} \
		--set prometheusSpec.maximumStartupDurationSeconds=360 \
		--set environment=${CS_ENVIRONMENT}
.PHONY: deploy

pull-chart:
	helm repo add prometheus-community https://prometheus-community.github.io/helm-charts;
	helm repo update;
	helm dependency build deploy/
.PHONY: pull-chart
