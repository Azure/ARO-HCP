deploy:
	make -C ./tracing deploy
.PHONY: deploy

UPSTREAM_RULE := alerts/kubernetesControlPlane-prometheusRule.upstream.yaml
LOCAL_RULE := alerts/kubernetesControlPlane-prometheusRule.yaml
LOCAL_DIFF := alerts/kubernetesControlPlane-prometheusRule.yaml.diff

$(UPSTREAM_RULE): alerts-sl-services.yaml
	PROMETHEUS_OPERATOR_REF=$$(yq -r '.prometheusRules.prometheusOperatorVersion' alerts-sl-services.yaml) && \
	wget --quiet --output-document=$@ \
	https://raw.githubusercontent.com/prometheus-operator/kube-prometheus/$${PROMETHEUS_OPERATOR_REF}/manifests/kubernetesControlPlane-prometheusRule.yaml

$(LOCAL_RULE): $(UPSTREAM_RULE) $(wildcard $(LOCAL_DIFF))
	cp $(UPSTREAM_RULE) $(LOCAL_RULE)
	@if [ -s $(LOCAL_DIFF) ]; then \
		patch $(LOCAL_RULE) $(LOCAL_DIFF); \
	fi

$(LOCAL_DIFF): $(UPSTREAM_RULE) $(LOCAL_RULE)
	diff -u --label upstream/kubernetesControlPlane-prometheusRule.yaml --label alerts/kubernetesControlPlane-prometheusRule.yaml \
	$(UPSTREAM_RULE) $(LOCAL_RULE) > $(LOCAL_DIFF); \
	DIFF_EXIT=$$?; \
	if [ $${DIFF_EXIT} -eq 0 ]; then \
		rm -f $(LOCAL_DIFF); \
	elif [ $${DIFF_EXIT} -ne 1 ]; then \
		rm -f $(LOCAL_DIFF); \
		exit $${DIFF_EXIT}; \
	fi

verify-rule: $(LOCAL_RULE)
	@if [ -n "$$(git status --short $(LOCAL_RULE))" ]; then \
		echo "The following file is out of date after running 'make -C observability/ sync-upstream':"; \
		echo; \
		git diff $(LOCAL_RULE); \
		echo; \
		echo "===================================================="; \
		echo "Run 'make -C observability/ generate-diff' and commit the changes."; \
		echo "===================================================="; \
		exit 1; \
	fi
.PHONY: verify-rule

alerts: verify-no-prom-alerts-in-bicep
	EXTRA_FLAGS=$(GENERATOR_FLAGS) make -C ../tooling/prometheus-rules alerts
.PHONY: alerts

alerts-and-test: verify-no-prom-alerts-in-bicep
	EXTRA_FLAGS=$(GENERATOR_FLAGS) make -C ../tooling/prometheus-rules alerts-and-test
.PHONY: alerts-and-test

correlation-map:
	@make -C ../tooling/prometheus-rules correlation-map OUTPUT=$(OUTPUT)
.PHONY: correlation-map

recording-rules:
	EXTRA_FLAGS=$(GENERATOR_FLAGS) make -C ../tooling/prometheus-rules recording-rules
.PHONY: recording-rules

verify: verify-rule
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
