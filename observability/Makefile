deploy:
	make -C ./tracing deploy
.PHONY: deploy

sync-upstream:
	PROMETHEUS_OPERATOR_REF=$$(yq -r '.prometheusRules.prometheusOperatorVersion' alerts-sl-services.yaml) && \
	wget --quiet --output-document=alerts/kubernetesControlPlane-prometheusRule.yaml \
	https://raw.githubusercontent.com/prometheus-operator/kube-prometheus/$${PROMETHEUS_OPERATOR_REF}/manifests/kubernetesControlPlane-prometheusRule.yaml
.PHONY: sync-upstream

alerts:
	EXTRA_FLAGS=$(GENERATOR_FLAGS) make -C ../tooling/prometheus-rules alerts
.PHONY: alerts

recording-rules:
	EXTRA_FLAGS=$(GENERATOR_FLAGS) make -C ../tooling/prometheus-rules recording-rules
.PHONY: recording-rules

verify:
	go run ../tooling/grafanactl/main.go verify dashboards --config-file observability.yaml
.PHONY: verify
