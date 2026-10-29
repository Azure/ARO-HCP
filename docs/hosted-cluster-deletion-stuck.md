# HostedCluster deletion age alert

`userJourneyClusterDeletionStuck` is a severity 4 alert in the RP HCP queue.
It fires when a HostedCluster's deletion timestamp is strictly more than 48 hours
old and that condition remains present for 15 minutes. At exactly 48 hours the
condition is false. An already-old deletion at rollout starts only the 15-minute
pending period, not a new 48-hour wait. No backend operation record is required;
a `Failed` operation does not suppress this alert. KAS alerts and SLOs are unchanged.

```promql
time() - max by (name, namespace, _id, subscription_id, resource_id, cluster, region) (
  hostedClusterAPI_deletion_timestamp_seconds > 0
) > 172800
```

The rule has `for: 15m`, emitted as `PT15M` in the generated Azure rule group.
The positive-value comparison filters zero timestamps without replacing the
timestamp with a boolean. Replica deduplication retains per-HostedCluster identity
before subtraction from the evaluator's Unix time. Distinct clusters fire
independently, even within one namespace or subscription. The correlation ID is
`userJourneyClusterDeletionStuck/{{ $labels.cluster }}/{{ $labels.subscription_id }}`
for customer-scoped correlation, not the Prometheus alert identity. The resource ID
remains in the grouping and diagnosis annotations. IcM adds the management cluster
to its title, so the summary does not repeat it.

## Metric contract

`observability/prometheus/values-mgmt.yaml` declares a Gauge named
`deletion_timestamp_seconds` under the `hostedClusterAPI` prefix, with
`path: [metadata, deletionTimestamp]` and `nilIsZero: true`.
The configured KSM image in `config/config.yaml` is v2.20.0. Its
[custom-resource gauge implementation](https://github.com/kubernetes/kube-state-metrics/blob/v2.20.0/pkg/customresourcestate/registry_factory.go)
parses RFC3339 strings and emits `float64(t.Unix())` (whole Unix seconds).
Absent or null timestamps produce zero with this configuration; deleted objects
produce no series. An invalid or empty timestamp string is not a valid Kubernetes
deletion timestamp and is not guaranteed to produce zero: conversion errors omit
the sample. See the [KSM conversion documentation](https://github.com/kubernetes/kube-state-metrics/blob/v2.20.0/docs/metrics/extend/customresourcestate-metrics.md#type-conversion-and-special-handling).

Identity labels derive from configured resource labels and scrape context, not a
verified production series schema. `api_url`, KSM type labels and ephemeral scrape
labels are excluded. Replica churn does not reset pending while a matching replica
remains visible. If the expression disappears, pending resets and a firing alert
clears. On reappearance the timestamp's age has not reset, but the 15-minute pending
period starts again. Short scrape gaps may be bridged by Prometheus's lookback
window. Telemetry loss is **not proof of successful deletion**; confirm resource
removal independently. Azure notification resolution is separate from query state.

## Rollout and validation

Deploy this metric change first and verify the new series and identity labels
before enabling dependent changes in [PR #7237](https://github.com/Azure/ARO-HCP/pull/7237).
That PR will be updated separately to consume this new metric; this change neither
modifies that PR nor imports its KAS suppression behavior. Coordinate metric-first
merge and deployment ordering, not just source compatibility.

`make -C observability alerts` tests and generates the alert Bicep;
`make -C config materialize` refreshes Helm fixtures. The RP HCP generated rules
are consumed through `modules/metrics/rp-hcp-rules.bicep` by
`dev-infrastructure/templates/monitoring.bicep`.
Local tests and compilation do not establish live Azure acceptance, production
label correctness, or firing counts. An int/stage alert-tester report remains
required after the metric rolls out. The existing
[cluster deletion TSG](https://aka.ms/cluster-deletion-tsg) remains the runbook link;
its dedicated Alert Triage entry, diagnosis and escalation updates are a separate
outstanding dependency, not completed by this repository change.
