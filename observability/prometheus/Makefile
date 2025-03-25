-include ../../setup-env.mk
-include ../../helm-cmd.mk

PROM_VERSION ?= "70.1.1"
NAMESPACE ?= "prometheus"

deploy:
	@kubectl create namespace ${NAMESPACE} --dry-run=client -o json | kubectl apply -f -
	${HELM_CMD} prometheus deploy/ \
		--namespace ${NAMESPACE} \
		--set kube-prometheus-stack.crds.enabled=true \
		--set kube-prometheus-stack.grafana.enabled=false \
		--set kube-prometheus-stack.prometheusOperator.enabled=true \
		--set kube-prometheus-stack.prometheusOperator.image.registry=mcr.microsoft.com/oss/v2 \
		--set kube-prometheus-stack.prometheusOperator.image.repository=prometheus/prometheus-operator \
		--set kube-prometheus-stack.prometheusOperator.image.sha="a5bf4407cb83dc93d4e29ef680e0a4d621256e0f004822f53b2ff1c592bf2a82" \
		--values=deploy/values.yaml
.PHONY: deploy


pull-chart:
	helm repo add prometheus-community https://prometheus-community.github.io/helm-charts;
	helm repo update;
	helm pull prometheus-community/kube-prometheus-stack --destination ./;
	tar -xvf kube-prometheus-stack-${PROM_VERSION}.tgz; 
	rm kube-prometheus-stack-${PROM_VERSION}.tgz; 