

kubernetesControlPlane-prometheusRule:
	wget --quiet --output-document=alerts/kubernetesControlPlane-prometheusRule.yaml \
	https://raw.githubusercontent.com/prometheus-operator/kube-prometheus/refs/heads/main/manifests/kubernetesControlPlane-prometheusRule.yaml
.PHONY: kubernetesControlPlane-prometheusRule

alerts: kubernetesControlPlane-prometheusRule
	make -C ../tooling/prometheus-rules run
	az bicep format -f $$(yq '.prometheusRules.outputBicep' observability.yaml)
.PHONY: alerts
