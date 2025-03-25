-include ../../setup-env.mk
-include ../../helm-cmd.mk

NAMESPACE ?= "prometheus"
CHART_VERSION ?= 70.4.1
LABEL_NAMESPACE_CMD := $(if $(PROMETHEUS_NAMESPACE_LABEL),kubectl label namespace ${NAMESPACE} ${PROMETHEUS_NAMESPACE_LABEL},)

deploy:
	@kubectl create namespace ${NAMESPACE} --dry-run=client -o json | kubectl apply -f -
	@${LABEL_NAMESPACE_CMD}
	PROMETHEUS_RW_ENDPOINT=$(shell az deployment group show -n metrics-infra -g ${RESOURCE_GROUP} --query properties.outputs.dcePromUrl.value -o tsv) && \
	PROMETHEUS_WI_IDENTITY_CLIENT_ID=$(shell az deployment group show -n cluster -g ${RESOURCE_GROUP} --query properties.outputs.userAssignedIdentities.value -o json | jq -r '.[] | select(.uamiName=="prometheus") | .uamiClientID') && \
	${HELM_CMD} prometheus deploy/ \
		--namespace ${NAMESPACE} \
		--values=helm-values.yaml \
		--set crds.enabled=true \
		--set grafana.enabled=false \
		--set prometheusOperator.enabled=true \
		--set prometheusOperator.image.registry=${PROMETHEUS_OPERATOR_REGISTRY} \
		--set prometheusOperator.image.repository=${PROMETHEUS_OPERATOR_REPOSITORY} \
		--set prometheusOperator.image.sha=${PROMETHEUS_OPERATOR_DIGEST} \
		--set prometheusOperator.prometheusConfigReloader.image.registry=mcr.microsoft.com/oss/v2 \
		--set prometheusOperator.prometheusConfigReloader.image.repository=prometheus/prometheus-config-reloader \
		--set prometheusOperator.prometheusConfigReloader.image.sha="b112cdc776c740261d812ab544261b781f9cb3520d7b400a353993d3be9c6df1" \
		--set prometheus.serviceAccount.annotations."azure\.workload\.identity/client-id"=$${PROMETHEUS_WI_IDENTITY_CLIENT_ID} \
		--set prometheus.prometheusSpec.image.registry=${PROMETHEUS_SPEC_REGISTRY} \
		--set prometheus.prometheusSpec.image.repository=${PROMETHEUS_SPEC_REPOSITORY} \
		--set prometheus.prometheusSpec.image.sha=${PROMETHEUS_SPEC_DIGEST} \
		--set prometheus.prometheusSpec.version=${PROMETHEUS_SPEC_VERSION} \
		--set prometheus.prometheusSpec.shards=${PROMETHEUS_SPEC_SHARDS} \
		--set prometheus.prometheusSpec.replicas=${PROMETHEUS_SPEC_REPLICAS} \
		--set prometheus.prometheusSpec.containers[0].env[0].name=INGESTION_URL \
		--set prometheus.prometheusSpec.containers[0].env[0].value=$${PROMETHEUS_RW_ENDPOINT} \
		--set prometheus.prometheusSpec.containers[0].env[1].name=LISTENING_PORT \
		--set-string prometheus.prometheusSpec.containers[0].env[1].value=8081 \
		--set prometheus.prometheusSpec.containers[0].env[2].name=IDENTITY_TYPE \
		--set prometheus.prometheusSpec.containers[0].env[2].value='workloadIdentity'
.PHONY: deploy

pull-chart:
	helm repo add prometheus-community https://prometheus-community.github.io/helm-charts;
	helm repo update;
	helm pull prometheus-community/kube-prometheus-stack --destination ./;
	tar -xvf kube-prometheus-stack-${CHART_VERSION}.tgz; 
	rm kube-prometheus-stack-${CHART_VERSION}.tgz; 
	mv kube-prometheus-stack/ deploy/;
.PHONY: pull-chart