# HostedCluster deletion observation alert

`HostedClusterDeletionStuck` is a severity 4 alert in the RP HCP rule group.
It fires after the same HostedCluster has been continuously observed deleting
for 48 hours. It does **not** calculate the age of `metadata.deletionTimestamp`.
An already-old deletion and the first rollout both start a new 48-hour observation
period. No backend operation record is required, and a `Failed` operation does not
suppress the alert. KAS alerts and SLOs are unchanged.

The source is `observability/alerts/hosted-cluster-deletion-stuck-prometheusRule.yaml`:

```promql
max by (name, namespace, _id, subscription_id, resource_id, cluster, region) (
  hostedClusterAPI_deleting{deletion_timestamp!=""}
) == 1
```

The rule has `for: 48h`. The metric is an Info metric with value 1, not a Unix
timestamp. Its string label `deletion_timestamp` is used only to select active
deletions; absent and empty labels do not match. The declaration in
`observability/prometheus/values-mgmt.yaml` is the same shared prerequisite as
[public PR #7237](https://github.com/Azure/ARO-HCP/pull/7237). Coordinate integration
of that declaration and its generated Helm fixtures; do not duplicate it or
import that PR's KAS changes.

Identity labels come from the configured HostedCluster custom-resource metric
labels and scrape context, not a verified production series schema. No production
series have been observed for this alert. `region` is retained in accordance with
the generator's aggregation-label preservation. `api_url`, the deletion timestamp,
KSM type labels, and ephemeral scrape labels such as `pod`, `instance`, and `job`
are intentionally excluded from the alert identity. Duplicate replicas collapse
to one alert per identity; replica churn does not reset pending state while at
least one matching replica remains visible at every evaluation.

If the expression stops returning an identity, its pending state resets and a
firing Prometheus alert clears. A reappearing identity starts another 48-hour
period. Deletion, stale samples, and telemetry loss can all remove a series;
disappearance is **not proof of successful deletion**. Brief scrape gaps may be
bridged by Prometheus's lookback window, so continuity is evaluated query presence,
not proof that every scrape succeeded. Azure notification resolution is managed
separately from the expression's pending and firing state. Confirm resource removal and
telemetry health independently using the [deletion runbook](https://aka.ms/cluster-deletion-tsg).

## Generation and deployment

`make -C observability alerts` runs the Prometheus rule tests, generates Bicep,
and formats infrastructure. The RP HCP configuration produces
`dev-infrastructure/modules/metrics/rules/generatedRPHCPPrometheusAlertingRules.bicep`,
consumed by `modules/metrics/rp-hcp-rules.bicep` from `templates/monitoring.bicep`.
The duration converter emits `PT48H` (48 hours), not the invalid `PT2D` produced
by placing a Prometheus day duration after the ISO time separator.
The [Azure rule-group API](https://learn.microsoft.com/en-us/azure/templates/microsoft.alertsmanagement/2023-03-01/prometheusrulegroups)
and [runtime documentation](https://learn.microsoft.com/en-us/azure/azure-monitor/metrics/prometheus-rule-groups)
define `for` as the alert firing timeout. Local rule tests and Bicep compilation
validate the observation behavior and deployment representation, not live Azure
service acceptance. Deployment validation and verification of collected labels
remain rollout checks; do not shorten the duration if service validation rejects it.
