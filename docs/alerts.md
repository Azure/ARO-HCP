# Writing Alerts

This guide describes how to create, test, and deploy Prometheus alerting rules for ARO-HCP services.

## How alerts work

Alerts are authored as standard Prometheus `PrometheusRule` CRDs in YAML. A code generator (`tooling/prometheus-rules/`) converts them into Azure Bicep resources (`Microsoft.AlertsManagement/prometheusRuleGroups`), which get deployed to Azure Monitor Workspaces. When an alert fires, Azure Monitor routes it through an IcM action group to create an incident.

```Text
PrometheusRule YAML
  --> promtool validates + runs tests
  --> generator produces Bicep
  --> Bicep deployed to Azure Monitor Workspace
  --> alert fires --> IcM action group --> incident
```

## File layout

Each service keeps its alerts under an `alerts/` directory. Every rule file must have a corresponding test file:

```
<service>/alerts/
  <service>-prometheusRule.yaml            # alert definitions
  <service>-prometheusRule_test.yaml        # required tests
```

Cross-service and platform alerts live in `observability/alerts/`.

## Writing an alert rule

### 1. Create the rule file

Create `<service>/alerts/<service>-prometheusRule.yaml` following the `PrometheusRule` CRD format:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  labels:
    app.kubernetes.io/name: kube-prometheus
    app.kubernetes.io/part-of: kube-prometheus
    prometheus: k8s
    role: alert-rules
  name: <service>-monitoring-rules
  namespace: monitoring
spec:
  groups:
  - name: <service>
    rules:
    - alert: MyAlertName
      expr: |
        sum by (cluster) (rate(errors_total{namespace="aro-hcp"}[5m])) > 0.05
      for: 10m
      labels:
        severity: warning
      annotations:
        summary: 'Short title for the alert'
        description: 'Detailed description. Can reference labels: {{ $labels.cluster }} and value: {{ $value }}.'
        runbook_url: 'https://eng.ms/docs/.../troubleshooting/<service>-tsg.html'
```

### Required fields

| Field | Description |
|---|---|
| `alert` | Alert name in PascalCase (e.g. `BackendControllerPanic`) |
| `expr` | PromQL expression that evaluates to true when the alert should fire |
| `labels.severity` | One of `critical`, `warning`, `info` |
| `annotations.summary` | Short title -- becomes the IcM incident title |
| `annotations.description` | Detailed explanation, can use `{{ $labels.X }}` and `{{ $value }}` |
| `annotations.runbook_url` | Link to the troubleshooting guide for this alert |
| `for` | (Optional) How long the condition must hold before firing (e.g. `5m`, `10m`). Defaults to the group's evaluation interval. |

### Severity mapping

The generator maps severity labels to Azure Monitor / IcM severity levels:

| Label | Azure Severity | IcM Behavior |
|---|---|---|
| `critical` | SEV 3 | Urgent, high business impact |
| `warning` | SEV 3 | Urgent, high business impact |
| `info` | SEV 4 | Not urgent, no SLA impact |

### The `summary` annotation and IcM titles

The `summary` annotation becomes the IcM incident title (prefixed with the cluster name). Keep it short and static -- avoid embedding `{{ $labels.X }}` template variables in `summary`.

The IcM title is rendered as: `<cluster>: <summary>`.

Use `description` for dynamic detail with template variables. The `description` is also duplicated into an `info` annotation by the generator (Azure Monitor strips `description` from the alert context, so `info` preserves it for IcM).

### CorrelationID behavior

The generator automatically sets a `correlationId` annotation on every alert:

```
<AlertName>/{{ $labels.cluster }}
```

IcM uses the correlation ID to group alerts into incidents. Alerts with the same correlation ID on the same cluster are merged into a single incident. Different alert names or different clusters produce separate incidents.

This means that all firings of the same alert on the same cluster are grouped together. For example, if `BackendControllerRetryHotLoop` fires for two different workqueues on the same cluster, they become one incident. The specific workqueue name is still visible in the alert `description`.

This is intentional: fine-grained correlation IDs (per-pod, per-queue, etc.) were found to cause excessive incident fragmentation. If you need to distinguish between instances in the incident, include the relevant labels in the `description` annotation where they are visible to the responder.

## 2. Write tests

Every rule file must have a corresponding `_test.yaml` file in the same directory. The generator will refuse to process rule files without tests.

Tests use the [promtool unit testing format](https://prometheus.io/docs/prometheus/latest/configuration/unit_testing_rules/):

```yaml
rule_files:
- <service>-prometheusRule.yaml
evaluation_interval: 1m
tests:
# Test: alert fires when condition is met
- interval: 1m
  input_series:
  - series: 'errors_total{namespace="aro-hcp"}'
    values: "0+100x20"  # starts at 0, increments by 100 each minute, 20 steps
  alert_rule_test:
  - eval_time: 15m
    alertname: MyAlertName
    exp_alerts:
    - exp_labels:
        severity: warning
      exp_annotations:
        summary: 'Short title for the alert'
        description: 'Detailed description. Can reference labels:  and value: ...'
        runbook_url: 'https://eng.ms/docs/.../troubleshooting/<service>-tsg.html'

# Test: alert does NOT fire below threshold
- interval: 1m
  input_series:
  - series: 'errors_total{namespace="aro-hcp"}'
    values: "0+1x20"  # low error rate
  alert_rule_test:
  - eval_time: 15m
    alertname: MyAlertName
    exp_alerts: []
```

## 3. Register the rule file

Add your rule file to the appropriate configuration in `observability/`:

| Config file | Purpose | When to use |
|---|---|---|
| `observability/observability.yaml` | Service and platform alerts | Most alerts go here |
| `observability/observability-hcp.yaml` | HCP namespace alerts | Alerts specific to hosted control planes |
| `observability/observability-rp.yaml` | Resource provider alerts | RP-specific alerts |
| `observability/observability-msft.yaml` | MSFT-filtered alerts | Subset of alerts for MSFT environments (uses `includedAlertsByGroup`) |

Edit the relevant YAML file and add your rule file path to `rulesFolders`:

```yaml
prometheusRules:
  rulesFolders:
  - ../backend/alerts/backend-prometheusRule.yaml
  - ../myservice/alerts/myservice-prometheusRule.yaml  # <-- add here
```

If your alert should also appear in the MSFT environment, add it to `observability/observability-msft.yaml` under `includedAlertsByGroup`.

## 4. Generate Bicep


```bash
make -C observability/ alerts
```
