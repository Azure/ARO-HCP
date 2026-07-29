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
        severity: "3"
      annotations:
        summary: 'Short title for the alert'
        description: 'Detailed description. Can reference labels: {{ $labels.cluster }} and value: {{ $value }}.'
        runbook_url: 'https://eng.ms/docs/.../troubleshooting/<service>-tsg.html'
```

#### Required fields

| Field | Description |
|---|---|
| `alert` | Alert name in PascalCase (e.g. `BackendControllerPanic`) |
| `expr` | PromQL expression that evaluates to true when the alert should fire |
| `labels.severity` | One of `2`, `2.5`, `3`, `4` (canonical, the IcM Sev number); `critical`, `warning`, `info` accepted but deprecated |
| `annotations.summary` | Short title -- becomes the IcM incident title |
| `annotations.description` | Detailed explanation, can use `{{ $labels.X }}` and `{{ $value }}` |
| `annotations.runbook_url` | Link to the troubleshooting guide for this alert |
| `for` | (Optional) How long the condition must hold before firing (e.g. `5m`, `10m`). Defaults to the group's evaluation interval. |

#### Severity mapping

Severity follows the Azure Common Engineering Naming (CEN) standard so alerts route cleanly into Microsoft IcM. It is set independently of burn rate: burn rate decides *when* an alert fires, severity decides *who* is paged at what urgency.

Use the explicit `2` / `2.5` / `3` / `4` values (the severity label is the IcM Sev number). The legacy `critical` / `warning` / `info` values are still accepted (deprecated). `1` is intentionally rejected by the generator: Azure CEN reserves Sev 1 for declared major incidents, so alerts must not self-classify as Sev 1. Any other value fails generation. The label is a string, so both `severity: "3"` and `severity: 3` are accepted (the generator parses the value as a string).

| Label | IcM Severity | Urgency |
|---|---|---|
| `2` (or `critical`) | SEV 2 | Needs immediate attention. |
| `2.5` (or `25`) | SEV 2.5 | Needs attention at start of next shift. |
| `3` (or `warning`) | SEV 3 | Needs prompt investigation. |
| `4` (or `info`) | SEV 4 | Can wait; no immediate action required. |

#### The `summary` annotation and IcM titles

The `summary` annotation becomes the IcM incident title (prefixed with the cluster name). Keep it short and static -- avoid embedding `{{ $labels.X }}` template variables in `summary`.

The IcM title is rendered as: `<cluster>: <summary>`.

Use `description` for dynamic detail with template variables. The `description` is also duplicated into an `info` annotation by the generator (Azure Monitor strips `description` from the alert context, so `info` preserves it for IcM).

#### CorrelationID behavior

The generator automatically sets a `correlationId` annotation on every alert unless the source rule already defines one:

```
<AlertName>/{{ $labels.cluster }}
```

IcM uses the correlation ID to group alerts into incidents. Alerts with the same correlation ID on the same cluster are merged into a single incident. Different alert names or different clusters produce separate incidents.

This means that all firings of the same alert on the same cluster are grouped together. For example, if `BackendControllerQueueDepthHigh` fires for two different workqueues on the same cluster, they become one incident. The specific workqueue name is still visible in the alert `description`.

This default is intentional: fine-grained correlation IDs (per-pod, per-queue, etc.) were found to cause excessive incident fragmentation. If you need to distinguish between instances in the incident, include the relevant labels in the `description` annotation where they are visible to the responder.

##### Overriding the correlationId

If the default per-infra-cluster grouping is too coarse, you can set a custom `correlationId` annotation directly in the source `PrometheusRule` YAML. The generator will preserve it instead of applying the default.

For example, to create a separate IcM incident per hosted cluster:

```yaml
annotations:
  summary: "High KubeAPIServer error budget burn for HostedCluster {{ $labels.name }}"
  description: "..."
  correlationId: "hostedcluster-KubeAPIServer-ErrorBudgetBurn/{{ $labels.cluster }}/{{ $labels._id }}"
```

Use this sparingly — only when distinct instances genuinely need independent incident tracking (e.g. different hosted clusters on the same management cluster).

### 2. Write tests

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

### 3. Register the rule file

Add your rule file to the appropriate configuration in `observability/`:

| Config file | Purpose | When to use |
|---|---|---|
| `observability/alerts-sl-services.yaml` | Service and platform alerts (SL queue) | Most alerts go here |
| `observability/alerts-sre-hcps.yaml` | HCP namespace alerts (SRE queue) | Alerts specific to hosted control planes |
| `observability/alerts-rp-services.yaml` | Resource provider alerts (RP queue) | RP-specific alerts |
| `observability/alerts-msft-services.yaml` | MSFT-filtered alerts (MSFT queue) | Subset of alerts for MSFT environments (uses `includedAlertsByGroup`) |

Edit the relevant YAML file and add your rule file path to `rulesFolders`:

```yaml
prometheusRules:
  rulesFolders:
  - ../backend/alerts/backend-prometheusRule.yaml
  - ../myservice/alerts/myservice-prometheusRule.yaml  # <-- add here
```

If your alert should also appear in the MSFT environment, add it to `observability/alerts-msft-services.yaml` under `includedAlertsByGroup`.

### 4. Verify Alerts

If the metrics are already present in PROD, you can verify your alerts:

* [Alert Verification Guide](./alert-verification.md)

### 5. Generate Bicep & Run Tests

The rules need to be converted from the `.yaml` representation and merged into `.bicep` files (located in [/dev-infrastructure/modules/metrics/rules](/dev-infrastructure/modules/metrics/rules)). Use the following command before committing — it will also run Prometheus rule tests via promtool:

```bash
make -C observability/ alerts
```

## Underlay clusters

### Authoritative list of underlay clusters

Every underlay cluster declares itself in an inventory of which clusters **should** be running.
The `underlay-clusters-metric` Bicep module
(`dev-infrastructure/modules/metrics/underlay-clusters-metric.bicep`) writes a single static
recording rule into the services Azure Monitor Workspace:

```
underlay_clusters{cluster="<cluster-name>", source="bicep"} = 1
```

The module is instantiated from each cluster's own template -- `svc-cluster.bicep` for the
service cluster and `mgmt-cluster.bicep` for each management cluster -- using that deployment's
`aksClusterName`. Because management clusters are deployed once per EV2 stamp, each stamp emits
its own series, and tearing a stamp down removes only that stamp's entry. The cluster name is
the same value that sets the cluster's Prometheus `cluster` external label
(`observability/prometheus/values-{svc,mgmt}.yaml`), so the inventory lines up exactly with the
`cluster` label on real metrics. The `source="bicep"` label marks the series as
deploy-time-declared inventory, distinct from anything a cluster reports about itself; the series
is evaluated independently of whether the cluster is actually reachable.

### Alerting on absent clusters

Alerts that watch a per-cluster signal (e.g. `up`) can only fire for clusters that are
**still reporting**. A cluster that has gone completely absent -- its Prometheus stopped
remote-writing, or the cluster was never provisioned -- produces no series at all, so there is
nothing for the alert to evaluate and nothing fires. PromQL `absent()` does not solve this on
its own: the synthetic series it returns carries only the labels written literally in the
selector, so it has no `cluster` label to key an incident on. This is why the tier configs
rewrite `absent(up{job="..."} == 1)` into `count by (cluster) (up{job="..."} == 1) == 0` via
`regexOutputReplacements` (see `observability/alerts-sre-hcps.yaml` and
`observability/alerts-msft-services.yaml`) -- but that still only covers clusters whose `up` is
present and `0`, not ones that have vanished entirely.

The authoritative inventory closes that gap: an alert compares "should exist" against "is
reporting" and fires -- with a real `cluster` label -- for any cluster present in the inventory
but missing from the live signal:

```yaml
- alert: UnderlayClusterMetricsAbsent
  expr: |
    underlay_clusters unless on (cluster)
      group by (cluster) (up{job="prometheus/prometheus", namespace="prometheus"})
  for: 10m
  labels:
    severity: warning
  annotations:
    summary: 'No metrics received from underlay cluster'
    description: 'Cluster {{ $labels.cluster }} is declared in the underlay inventory but is reporting no Prometheus self-metrics.'
    runbook_url: 'https://eng.ms/docs/.../troubleshooting/prometheus.html'
```

Because the result keeps the `cluster` label, the generator's default
`correlationId: <AlertName>/{{ $labels.cluster }}` and the `<cluster>: <summary>` IcM title
resolve correctly -- one incident per genuinely-absent cluster.

### Known limitation: new cluster bring-up

A cluster declares itself in the inventory as part of its own deployment, before that cluster is
fully up and its Prometheus is hooked up and remote-writing. There is therefore a window during
the stand-up of a new (e.g. freshly stamped) management cluster where the inventory says the
cluster should exist but no metrics are flowing yet, so an absence alert can fire transiently
until all components are up. If this turns out to be a real problem in practice it will likely
need a more comprehensive approach than adjusting this one metric -- other alerts may fire during
bring-up too -- and we will only know once a few more management clusters have been stamped.
