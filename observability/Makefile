include ../.bingo/Variables.mk

deploy:
	$(MAKE) -C ./tracing deploy
.PHONY: deploy

kubernetesControlPlane-prometheusRule:
	PROMETHEUS_OPERATOR_REF=$(shell yq -r '.prometheusRules.prometheusOperatorVersion' observability.yaml) && \
	wget --quiet --output-document=alerts/kubernetesControlPlane-prometheusRule.yaml \
	https://raw.githubusercontent.com/prometheus-operator/kube-prometheus/$${PROMETHEUS_OPERATOR_REF}/manifests/kubernetesControlPlane-prometheusRule.yaml
.PHONY: kubernetesControlPlane-prometheusRule

alerts: kubernetesControlPlane-prometheusRule
	EXTRA_FLAGS=$(GENERATOR_FLAGS) $(MAKE) -C ../tooling/prometheus-rules run PROMTOOL=$(PROMTOOL)
	az bicep format -f $$(yq '.prometheusRules.outputBicep' observability.yaml)
	EXTRA_FLAGS=$(GENERATOR_FLAGS) $(MAKE) -C ../tooling/prometheus-rules run-hcp PROMTOOL=$(PROMTOOL)
	az bicep format -f $$(yq '.prometheusRules.outputBicep' observability-hcp.yaml)
	EXTRA_FLAGS=$(GENERATOR_FLAGS) $(MAKE) -C ../tooling/prometheus-rules run-rp PROMTOOL=$(PROMTOOL)
	az bicep format -f $$(yq '.prometheusRules.outputBicep' observability-rp.yaml)
	EXTRA_FLAGS=$(GENERATOR_FLAGS) $(MAKE) -C ../tooling/prometheus-rules run-msft PROMTOOL=$(PROMTOOL)
	az bicep format -f $$(yq '.prometheusRules.outputBicep' observability-msft.yaml)
.PHONY: alerts

recording-rules:
	$(MAKE) -C ../tooling/prometheus-rules run-recording-rules PROMTOOL=$(PROMTOOL)
	az bicep format -f $$(yq '.prometheusRules.outputBicep' observability-recording-rules.yaml)
	$(MAKE) -C ../tooling/prometheus-rules run-hcp-recording-rules PROMTOOL=$(PROMTOOL)
	az bicep format -f $$(yq '.prometheusRules.outputBicep' observability-hcp-recording-rules.yaml)
.PHONY: recording-rules

verify:
	go run ../tooling/grafanactl/main.go verify dashboards --config-file observability.yaml
.PHONY: verify
