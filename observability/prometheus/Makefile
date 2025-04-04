-include ../../setup-env.mk
-include ../../helm-cmd.mk

NAMESPACE ?= "prometheus"

deploy:
	@kubectl create namespace ${NAMESPACE} --dry-run=client -o json | kubectl apply -f -
	@kubectl label namespace ${NAMESPACE} ${PROMETHEUS_NAMESPACE_LABEL}
	PROMETHEUS_RW_ENDPOINT=$(shell az deployment group show -n metrics-infra -g ${RESOURCE_GROUP} --query properties.outputs.dcePromUrl.value -o tsv) && \
	PROMETHEUS_WI_IDENTITY_CLIENT_ID=$(shell az deployment group show -n cluster -g ${RESOURCE_GROUP} --query properties.outputs.userAssignedIdentities.value -o json | jq -r '.[] | select(.uamiName=="prometheus") | .uamiClientID') && \
	${HELM_CMD} prometheus deploy/ \
		--namespace ${NAMESPACE} \
		--set kube-prometheus-stack.crds.enabled=true \
		--set kube-prometheus-stack.grafana.enabled=false \
		--set kube-prometheus-stack.prometheusOperator.enabled=true \
		--set kube-prometheus-stack.prometheusOperator.image.registry=${PROMETHEUS_OPERATOR_REGISTRY} \
		--set kube-prometheus-stack.prometheusOperator.image.repository=${PROMETHEUS_OPERATOR_REPOSITORY} \
		--set kube-prometheus-stack.prometheusOperator.image.sha=${PROMETHEUS_OPERATOR_DIGEST} \
		--set kube-prometheus-stack.prometheusOperator.prometheusConfigReloader.image.registry=mcr.microsoft.com/oss/v2 \
		--set kube-prometheus-stack.prometheusOperator.prometheusConfigReloader.image.repository=prometheus/prometheus-config-reloader \
		--set kube-prometheus-stack.prometheusOperator.prometheusConfigReloader.image.sha="b112cdc776c740261d812ab544261b781f9cb3520d7b400a353993d3be9c6df1" \
		--set kube-prometheus-stack.prometheus.prometheusSpec.image.registry=${PROMETHEUS_SPEC_REGISTRY} \
		--set kube-prometheus-stack.prometheus.prometheusSpec.image.repository=${PROMETHEUS_SPEC_REPOSITORY} \
		--set kube-prometheus-stack.prometheus.prometheusSpec.image.sha=${PROMETHEUS_SPEC_DIGEST} \
		--set kube-prometheus-stack.prometheus.prometheusSpec.version=${PROMETHEUS_SPEC_VERSION} \
		--set kube-prometheus-stack.prometheus.prometheusSpec.shards=${PROMETHEUS_SPEC_SHARDS} \
		--set kube-prometheus-stack.prometheus.prometheusSpec.replicas=${PROMETHEUS_SPEC_REPLICAS} \
		--set kube-prometheus-stack.prometheus.prometheusSpec.containers[0].env[0].name=INGESTION_URL \
		--set kube-prometheus-stack.prometheus.prometheusSpec.containers[0].env[0].value="$${PROMETHEUS_RW_ENDPOINT}/dataCollectionRules/dcr-990febd148834b4bb42caeb05c667250/streams/Microsoft-PrometheusMetrics/api/v1/write?api-version=2021-11-01-preview" \
		--set kube-prometheus-stack.prometheus.prometheusSpec.containers[0].env[1].name=LISTENING_PORT \
		--set-string kube-prometheus-stack.prometheus.prometheusSpec.containers[0].env[1].value=8081 \
		--set kube-prometheus-stack.prometheus.prometheusSpec.containers[0].env[2].name=IDENTITY_TYPE \
		--set kube-prometheus-stack.prometheus.serviceAccount.annotations."azure\.workload\.identity/client-id"=$${PROMETHEUS_WI_IDENTITY_CLIENT_ID} \
		--set kube-prometheus-stack.prometheus.prometheusSpec.containers[0].env[2].value='workloadIdentity' \
		--values=deploy/values.yaml
.PHONY: deploy


# --set kube-prometheus-stack.prometheus.prometheusSpec.remoteWrite[0].url="$${PROMETHEUS_RW_ENDPOINT}/dataCollectionRules/dcr-990febd148834b4bb42caeb05c667250/streams/Microsoft-PrometheusMetrics/api/v1/write?api-version=2021-11-01-preview" \

pull-chart:
	helm repo add prometheus-community https://prometheus-community.github.io/helm-charts;
	helm repo update;
	helm pull prometheus-community/kube-prometheus-stack --destination ./;
	tar -xvf kube-prometheus-stack-${PROM_VERSION}.tgz; 
	rm kube-prometheus-stack-${PROM_VERSION}.tgz; 