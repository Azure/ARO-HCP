# Cluster Service SLO Alerts

This directory contains the Cluster Service SLO (Service Level Objective) alerting rules for the ARO-HCP project.

## Files

- `clusterServiceSlo-prometheusRule.yaml` - The main PrometheusRule definition containing the SLO alert
- `clusterServiceSlo-prometheusRule_test.yaml` - Comprehensive test suite for the SLO alert and recording rules
- `kubernetesControlPlane-prometheusRule.yaml` - Standard Kubernetes control plane alerts
- `README.md` - This documentation file

## Alert Overview

The `ClustersServiceAPIAvailability5mto1hor30mto6hErrorBudgetBurn` alert implements a **multi-burn rate SLO alerting** strategy following Google SRE best practices.

### SLO Target
- **99% availability** over a 28-day rolling window
- **1% error budget** (maximum allowed error rate)

### Multi-Burn Rate Detection

The alert uses two detection windows to catch both fast and slow error budget burns:

1. **Fast Burn** (5m + 1h windows):
   - Detects rapid issues that would exhaust the error budget in ~2 days
   - Threshold: 13.44x the error budget (13.44 * 1% = 13.44%)

2. **Slow Burn** (30m + 6h windows):
   - Detects sustained issues that would exhaust the error budget in ~5 days
   - Threshold: 5.6x the error budget (5.6 * 1% = 5.6%)

### Dependencies

The alert depends on these recording rules (defined in `dev-infrastructure/modules/metrics/rules/clusterServiceRecordingRules.bicep`):

- `availability:api_inbound_request_count:burnrate5m`
- `availability:api_inbound_request_count:burnrate1h`
- `availability:api_inbound_request_count:burnrate30m`
- `availability:api_inbound_request_count:burnrate6h`

### Metrics Used

The alert expects metrics in this format:
```promql
api_inbound_request_count{namespace="clusters-service", service="clusters-service-metrics", code="200|500|..."}
```

## Test Coverage

The test suite covers:

1. **Normal Operation** - No errors, alert should not fire
2. **Fast Burn Rate** - High error rate for short period (triggers 5m+1h windows)
3. **Slow Burn Rate** - Moderate error rate for longer period (triggers 30m+6h windows)
4. **Edge Cases** - Exactly at threshold (should not fire)
5. **Prometheus Replica Handling** - Proper deduplication with `max without(prometheus_replica)`
6. **Recovery** - Alert stops firing when error rate drops

**Note**: The test file exists but cannot be executed by the test framework because the alert depends on recording rules that are defined in Bicep format (`clusterServiceRecordingRules.bicep`) rather than Prometheus YAML format. This is a limitation of the current test framework setup.

## Running Tests

Tests are automatically run when generating Bicep templates:

```bash
# Run all prometheus rules tests
make -C tooling/prometheus-rules run

# Or run the full observability pipeline
cd observability && make alerts
```

**Note**: The clusterServiceSlo alert is currently in the `untestedRules` section due to the recording rule dependency limitation mentioned above.

## Runbook

When the alert fires, follow the troubleshooting guide at:
https://runbooks.prometheus-operator.dev/runbooks/kubernetes/kubeapierrorbudgetburn

## Alert Labels

- `severity: critical`
- `long: 6h` - Indicates the longer time window used
- `short: 30m` - Indicates the shorter time window used

## Deployment

The alert is automatically deployed as part of the ARO-HCP infrastructure via:
1. PrometheusRule YAML → Bicep generation
2. Bicep deployment through Azure Resource Manager
3. Azure Monitor Prometheus Rule Groups