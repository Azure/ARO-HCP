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
		--set aroHcp.prometheusSpec.image.registry=${PROMETHEUS_SPEC_REGISTRY} \
		--set aroHcp.prometheusSpec.image.repository=${PROMETHEUS_SPEC_REPOSITORY} \
		--set aroHcp.prometheusSpec.image.digest=${PROMETHEUS_SPEC_DIGEST} \
		--set aroHcp.prometheusSpec.version=${PROMETHEUS_SPEC_VERSION} \
		--set aroHcp.prometheusSpec.shards=${PROMETHEUS_SPEC_SHARDS} \
		--set aroHcp.prometheusSpec.replicas=${PROMETHEUS_SPEC_REPLICAS} \
		--set aroHcp.prometheusSpec.remoteWrite.url=$${PROMETHEUS_RW_ENDPOINT} \
		--set aroHcp.prometheusSpec.remoteWrite.managedIdentityClientId=$${PROMETHEUS_WI_IDENTITY_CLIENT_ID} \
		--values=deploy/values.yaml
.PHONY: deploy


pull-chart:
	helm repo add prometheus-community https://prometheus-community.github.io/helm-charts;
	helm repo update;
	helm pull prometheus-community/kube-prometheus-stack --destination ./;
	tar -xvf kube-prometheus-stack-${PROM_VERSION}.tgz; 
	rm kube-prometheus-stack-${PROM_VERSION}.tgz; 