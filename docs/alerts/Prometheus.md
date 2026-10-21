# Prometheus alerts

This page describes the Prometheus self-monitoring alerts in `observability/alerts/prometheus-prometheusRule.yaml`, including the alerts added by [Azure/ARO-HCP#5535](https://github.com/Azure/ARO-HCP/pull/5535).

The timings below are approximate and exclude Azure Monitor evaluation jitter, IcM routing latency, and notification delivery latency. The alert generator maps `critical` to IcM Sev 2, `warning` to Sev 3, and `info` to Sev 4.

## Alert coverage summary

| Alert | `for:` | Severity | What it indicates |
|---|---:|---|---|
| `PrometheusJobUp` | 10m | critical | The cluster exists according to the external anchor, but `up{job="prometheus/prometheus", namespace="prometheus"} == 1` is not present. This catches Prometheus down, unreachable, or reporting `up=0`. |
| `PrometheusMetricsAbsentPerCluster` | 10m | critical | No Prometheus self-`up` samples have arrived for a cluster that should be reporting. With `underlay_clusters`, this means a declared underlay cluster has no Prometheus self-metrics in the recent window. |
| `PrometheusUptimeSampleCount` | 10m | warning | Fewer than 95% of expected Prometheus self-`up` samples arrived over 24h. This treats missing samples as downtime. |
| `PrometheusUptime` | 10m | critical | More than 5% of present Prometheus self-`up` samples over 24h were `0`, weighted by sample count across all observed series. This does not count missing samples as downtime. |
| `PrometheusPendingRate` | 15m | critical | Prometheus remote-write queue pressure is high: too many samples are pending relative to in-flight samples. |
| `PrometheusFailedRate` | 15m | critical | More than 10% of remote-write samples are failing over the short rate window. |
| `PrometheusRemoteStorageFailures` | 15m | critical | Upstream Prometheus Operator alert: more than 1% of samples failed to send to remote storage. Metrics and alerts may be missing or inaccurate. |
| `PrometheusNotIngestingSamples` | 10m | warning | Upstream Prometheus Operator alert: Prometheus is alive and has target metadata, but is not ingesting samples locally. |
| `PrometheusBadConfig` | 10m | critical | Upstream Prometheus Operator alert: Prometheus could not reload its generated configuration and is running with the last known good config. |
| `PrometheusScrapeSampleLimitHit` | 15m | warning | Upstream Prometheus Operator alert: at least one scrape exceeded the configured sample limit, so metrics from that target may be dropped. |
| `PrometheusOperatorNotReady` | 5m | warning | Upstream Prometheus Operator alert: the operator is not ready and may not reconcile Prometheus resources. |
| `PrometheusOperatorRejectedResources` | 20m | warning | Upstream Prometheus Operator alert: the operator rejected one or more managed resources, so their config was not propagated to Prometheus or Alertmanager. |

## Time to page for Prometheus completely down

This table assumes the worst useful "Prometheus is down" shape for each alert: Prometheus self-metrics stop reaching the Azure Monitor Workspace entirely. Alerts that require Prometheus to keep emitting internal metrics will not fire for this shape.

| Alert | `for:` | Severity | Earliest firing for complete Prometheus silence | Why |
|---|---:|---|---:|---|
| `PrometheusJobUp` | 10m | critical | ~10-15m | It can fire as soon as the latest good `up == 1` sample is no longer selected, then the `for: 10m` timer completes. The upper bound accounts for PromQL lookback of the last good sample. |
| `PrometheusMetricsAbsentPerCluster` | 10m | critical | ~20m | `count_over_time(...[10m])` remains non-empty until the last sample is older than 10m, then the `for: 10m` timer completes. |
| `PrometheusUptimeSampleCount` | 10m | warning | ~82m | It fires once more than 5% of the expected 24h samples are missing: 72m of missing samples plus `for: 10m`. |
| `PrometheusUptime` | 10m | critical | Never for pure absence; ~82m if `up=0` samples continue | It compares successful present `up` samples with total present `up` samples. Missing samples do not lower the average. |
| `PrometheusPendingRate` | 15m | critical | Never | Needs Prometheus to emit remote-write queue metrics. |
| `PrometheusFailedRate` | 15m | critical | Never | Needs Prometheus to emit remote-write failure counters. |
| `PrometheusRemoteStorageFailures` | 15m | critical | Never | Needs Prometheus to emit remote storage success/failure counters. |
| `PrometheusNotIngestingSamples` | 10m | warning | Never | Needs Prometheus to emit ingestion and target metadata metrics. |
| `PrometheusBadConfig` | 10m | critical | Never | Needs Prometheus to emit config reload status metrics. |
| `PrometheusScrapeSampleLimitHit` | 15m | warning | Never | Needs Prometheus to emit scrape limit counters. |
| `PrometheusOperatorNotReady` | 5m | warning | Never | Detects operator readiness, not Prometheus self-metric silence. |
| `PrometheusOperatorRejectedResources` | 20m | warning | Never | Detects rejected operator resources, not Prometheus self-metric silence. |

## Failure modes captured

### Prometheus is completely dead or remote-write has stopped

Primary alerts:

| Alert | Signal |
|---|---|
| `PrometheusJobUp` | Fastest page. The declared/anchored cluster does not have a healthy Prometheus self-`up` series. |
| `PrometheusMetricsAbsentPerCluster` | Confirms complete absence of Prometheus self-metrics for the cluster. |
| `PrometheusUptimeSampleCount` | Later SLO-style signal that the 24h sample budget was missed. |

This is the most important case for `PrometheusJobUp` and `PrometheusMetricsAbsentPerCluster`. Once `underlay_clusters` is the source of truth, these should be interpreted as "this declared underlay cluster is expected to report Prometheus self-metrics, but it is not doing so."

### Prometheus is currently reporting `up=0`

Primary alerts:

| Alert | Signal |
|---|---|
| `PrometheusJobUp` | Fires after 10m because the expected healthy `up == 1` series is absent. |
| `PrometheusUptime` | Fires after more than 5% of the present Prometheus self-`up` samples in the 24h window are `0`, plus `for: 10m`. Short-lived target identities are weighted only by their actual sample count. |

`PrometheusUptimeSampleCount` does not detect this by itself because the samples are present. It only counts how many samples arrived, not whether they were `0` or `1`.

### Prometheus self-metrics are gappy or intermittent

Primary alert:

| Alert | Signal |
|---|---|
| `PrometheusUptimeSampleCount` | Fires when the missing samples add up to more than 5% of the 24h expected count. |

This is the main blind spot that `PrometheusUptimeSampleCount` covers. Repeated short outages can avoid `PrometheusJobUp` if each gap is too short to satisfy `for: 10m`, and they can avoid `PrometheusUptime` if the samples that do arrive are all `1`. The sample-count alert still catches the cumulative loss once the missing samples exceed the 5% budget.

### Prometheus is alive, but remote write is failing

Primary alerts:

| Alert | Signal |
|---|---|
| `PrometheusRemoteStorageFailures` | More than 1% of samples failed to send to remote storage. |
| `PrometheusFailedRate` | More than 10% of remote-write samples are failing. |
| `PrometheusPendingRate` | Samples are backing up in the remote-write queue. |
| `PrometheusUptimeSampleCount` | May later fire if the failure results in missing Prometheus self-`up` samples in AMW. |

These alerts require Prometheus to remain alive enough to expose its remote-write metrics. If Prometheus dies completely, they do not fire.

### Prometheus is alive, but not ingesting samples locally

Primary alert:

| Alert | Signal |
|---|---|
| `PrometheusNotIngestingSamples` | Prometheus has target metadata but appends no TSDB samples. |

This means the local scrape/ingestion path is broken even though Prometheus itself is still exporting metrics. The upstream runbook describes the impact as missing metrics.

### Prometheus configuration is bad or stale

Primary alert:

| Alert | Signal |
|---|---|
| `PrometheusBadConfig` | The last configuration reload failed. |

According to the upstream runbook, Prometheus continues running with the last known good configuration. New or changed `Prometheus`, `Probe`, `PodMonitor`, or `ServiceMonitor` objects may not be picked up.

### A target exceeds Prometheus scrape sample limits

Primary alert:

| Alert | Signal |
|---|---|
| `PrometheusScrapeSampleLimitHit` | A scrape exceeded the configured sample limit. |

This usually means a target is exposing too many series or has a cardinality spike. Prometheus may drop metrics from that target, so downstream alerts and dashboards can become incomplete.

### Prometheus Operator cannot reconcile correctly

Primary alerts:

| Alert | Signal |
|---|---|
| `PrometheusOperatorNotReady` | The operator is not ready and cannot reliably operate. |
| `PrometheusOperatorRejectedResources` | The operator rejected one or more managed resources, so their configuration was not propagated. |

These are not direct "Prometheus is down" alerts. They indicate that future Prometheus configuration, rules, scrape definitions, or related managed resources may not be reconciled correctly.

## Upstream runbooks

Several alerts come from the Prometheus Operator upstream runbooks:

| Alert | Upstream runbook |
|---|---|
| `PrometheusRemoteStorageFailures` | <https://runbooks.prometheus-operator.dev/runbooks/prometheus/prometheusremotestoragefailures/> |
| `PrometheusNotIngestingSamples` | <https://runbooks.prometheus-operator.dev/runbooks/prometheus/prometheusnotingestingsamples/> |
| `PrometheusBadConfig` | <https://runbooks.prometheus-operator.dev/runbooks/prometheus/prometheusbadconfig/> |
| `PrometheusOperatorNotReady` | <https://runbooks.prometheus-operator.dev/runbooks/prometheus-operator/prometheusoperatornotready/> |
| `PrometheusOperatorRejectedResources` | <https://runbooks.prometheus-operator.dev/runbooks/prometheus-operator/prometheusoperatorrejectedresources/> |

`PrometheusScrapeSampleLimitHit` points at this page instead of the upstream Prometheus Operator runbook because `https://runbooks.prometheus-operator.dev/runbooks/prometheus/prometheusscrapesamplelimithit` currently returns 404. The alert means that a target exceeded its configured Prometheus scrape sample limit and some target metrics may be missing.
