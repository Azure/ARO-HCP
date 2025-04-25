deploy:
	make -C ./tracing deploy
.PHONY: deploy

kubernetesControlPlane-prometheusRule:
	PROMETHEUS_OPERATOR_REF=$(shell yq -r '.prometheusRules.prometheusOperatorVersion' observability.yaml) && \
	wget --quiet --output-document=alerts/kubernetesControlPlane-prometheusRule.yaml \
	https://raw.githubusercontent.com/prometheus-operator/kube-prometheus/$${PROMETHEUS_OPERATOR_REF}/manifests/kubernetesControlPlane-prometheusRule.yaml
.PHONY: kubernetesControlPlane-prometheusRule

alerts: kubernetesControlPlane-prometheusRule
	make -C ../tooling/prometheus-rules run
	az bicep format -f $$(yq '.prometheusRules.outputBicep' observability.yaml)
.PHONY: alerts
