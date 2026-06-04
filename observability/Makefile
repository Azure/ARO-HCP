deploy:
	make -C ./tracing deploy
.PHONY: deploy

sync-upstream:
	PROMETHEUS_OPERATOR_REF=$$(yq -r '.prometheusRules.prometheusOperatorVersion' observability.yaml) && \
	wget --quiet --output-document=alerts/kubernetesControlPlane-prometheusRule.yaml \
	https://raw.githubusercontent.com/prometheus-operator/kube-prometheus/$${PROMETHEUS_OPERATOR_REF}/manifests/kubernetesControlPlane-prometheusRule.yaml
.PHONY: sync-upstream

alerts:
	EXTRA_FLAGS=$(GENERATOR_FLAGS) make -C ../tooling/prometheus-rules run
	az bicep format -f $$(yq '.prometheusRules.outputBicep' observability.yaml)
	EXTRA_FLAGS=$(GENERATOR_FLAGS) make -C ../tooling/prometheus-rules run-hcp
	az bicep format -f $$(yq '.prometheusRules.outputBicep' observability-hcp.yaml)
	EXTRA_FLAGS=$(GENERATOR_FLAGS) make -C ../tooling/prometheus-rules run-rp
	az bicep format -f $$(yq '.prometheusRules.outputBicep' observability-rp.yaml)
	EXTRA_FLAGS=$(GENERATOR_FLAGS) make -C ../tooling/prometheus-rules run-msft
	az bicep format -f $$(yq '.prometheusRules.outputBicep' observability-msft.yaml)
.PHONY: alerts

recording-rules:
	make -C ../tooling/prometheus-rules run-recording-rules
	az bicep format -f $$(yq '.prometheusRules.outputBicep' observability-recording-rules.yaml)
	make -C ../tooling/prometheus-rules run-hcp-recording-rules
	az bicep format -f $$(yq '.prometheusRules.outputBicep' observability-hcp-recording-rules.yaml)
.PHONY: recording-rules

verify:
	go run ../tooling/grafanactl/main.go verify dashboards --config-file observability.yaml
.PHONY: verify
