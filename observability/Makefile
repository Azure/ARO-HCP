deploy:
	make -C ./tracing deploy
.PHONY: deploy

sync-upstream:
	PROMETHEUS_OPERATOR_REF=$$(yq -r '.prometheusRules.prometheusOperatorVersion' alerts-sl-services.yaml) && \
	wget --quiet --output-document=alerts/kubernetesControlPlane-prometheusRule.yaml \
	https://raw.githubusercontent.com/prometheus-operator/kube-prometheus/$${PROMETHEUS_OPERATOR_REF}/manifests/kubernetesControlPlane-prometheusRule.yaml && \
	if [ -f alerts/kubernetesControlPlane-prometheusRule.yaml.diff ]; then \
		patch alerts/kubernetesControlPlane-prometheusRule.yaml alerts/kubernetesControlPlane-prometheusRule.yaml.diff; \
	fi
.PHONY: sync-upstream

generate-diff:
	PROMETHEUS_OPERATOR_REF=$$(yq -r '.prometheusRules.prometheusOperatorVersion' alerts-sl-services.yaml) && \
	UPSTREAM=$$(mktemp) && \
	wget --quiet --output-document=$${UPSTREAM} \
	https://raw.githubusercontent.com/prometheus-operator/kube-prometheus/$${PROMETHEUS_OPERATOR_REF}/manifests/kubernetesControlPlane-prometheusRule.yaml && \
	diff -u --label upstream/kubernetesControlPlane-prometheusRule.yaml --label alerts/kubernetesControlPlane-prometheusRule.yaml \
	$${UPSTREAM} alerts/kubernetesControlPlane-prometheusRule.yaml > alerts/kubernetesControlPlane-prometheusRule.yaml.diff || true && \
	rm -f $${UPSTREAM}
.PHONY: generate-diff

verify-sync-upstream: sync-upstream
	@if [ -n "$$(git status --short alerts/kubernetesControlPlane-prometheusRule.yaml)" ]; then \
		echo "The following files are out of date after running 'make -C observability/ sync-upstream':"; \
		echo; \
		git status --short alerts/kubernetesControlPlane-prometheusRule.yaml; \
		echo; \
		git diff alerts/kubernetesControlPlane-prometheusRule.yaml; \
		echo; \
		echo "===================================================="; \
		echo "Run 'make -C observability/ generate-diff' and commit the changes."; \
		echo "===================================================="; \
		exit 1; \
	fi
.PHONY: verify-sync-upstream

alerts: verify-no-prom-alerts-in-bicep
	EXTRA_FLAGS=$(GENERATOR_FLAGS) make -C ../tooling/prometheus-rules alerts
.PHONY: alerts

correlation-map:
	@make -C ../tooling/prometheus-rules correlation-map OUTPUT=$(OUTPUT)
.PHONY: correlation-map

recording-rules:
	EXTRA_FLAGS=$(GENERATOR_FLAGS) make -C ../tooling/prometheus-rules recording-rules
.PHONY: recording-rules

verify: verify-sync-upstream
	go run ../tooling/grafanactl/main.go verify dashboards --config-file observability.yaml
.PHONY: verify

verify-no-prom-alerts-in-bicep:
	@if grep -rFl --include='*.bicep' 'Microsoft.AlertsManagement/prometheusRuleGroups' \
		../dev-infrastructure/ 2>/dev/null | grep -qv -e '/generated' -e '/defaultRecordingRuleGroups.bicep' -e '/underlay-clusters-metric.bicep'; then \
		echo "ERROR: Prom alerts need to be added with mechanisms in observability/"; \
		grep -rFl --include='*.bicep' 'Microsoft.AlertsManagement/prometheusRuleGroups' \
			../dev-infrastructure/ 2>/dev/null | grep -v -e '/generated' -e '/defaultRecordingRuleGroups.bicep' -e '/underlay-clusters-metric.bicep'; \
		exit 1; \
	fi
.PHONY: verify-no-prom-alerts-in-bicep
