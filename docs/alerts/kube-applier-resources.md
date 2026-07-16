# kube-applier Memory Resource Alerts

These alerts detect when kube-applier's actual memory usage drifts above its
configured request, catching growth before it leads to OOM kills or evictions.
The request value lives in `config/config.yaml` under
`mgmt.deployments.kubeApplier.resources.requests.memory`.

The alerts link to this page for investigation and remediation context:
<https://github.com/Azure/ARO-HCP/blob/main/docs/alerts/kube-applier-resources.md>

## The alerts at a glance

- **`KubeApplierMemoryDrift`** (warning / SEV 3, `for: 15m`) — fires when the
  working set exceeds 1.5× the memory request for 15 minutes.
- **`KubeApplierMemoryTrend`** (info / SEV 4, `for: 30m`) — fires when
  `predict_linear` over a 6-hour window projects memory will exceed 2× the
  request within 4 hours.

## What causes these alerts to fire

- **Memory leak** in kube-applier or one of its dependencies.
- **Workload growth** — more ApplyDesires being reconciled, larger manifests,
  more clusters assigned to the management cluster.
- **Request set too low** — the initial sizing was based on a lighter workload
  than production now runs.

## Investigation steps

1. **Confirm the alert is genuine** — open the Ad-hoc Explorer dashboard in
   Grafana and query:
   ```promql
   container_memory_working_set_bytes{container="kube-applier", namespace="kube-applier"}
   ```
   Compare to the configured request value.

2. **Check for recent changes** — query Kusto for recent kube-applier
   deployments or version changes on the affected cluster. Check whether the
   number of ApplyDesires or managed clusters has recently increased.

3. **Check pod restarts via Kusto** — query `KubePodInventory` for the
   kube-applier namespace on the affected cluster to check restart counts and
   OOMKill events. Frequent restarts combined with this alert suggest a memory
   leak.

4. **Look at the growth pattern**:
   - Sudden jump → likely a new workload or config change.
   - Steady linear growth → likely a memory leak.
   - Stepped increases over days → workload growth (more clusters/desires).

## Remediation

- **Right-size the request**: If usage has legitimately grown, update the memory
  request in `config/config.yaml` and roll out. The request should be set to
  observed peak usage × 1.25 safety margin.
- **Investigate a leak**: If growth is unbounded and linear, file a bug against
  kube-applier and consider adding a memory limit as a short-term guardrail.
- **Scale the cluster**: If the growth is proportional to the number of
  ApplyDesires, this is expected — scale the management cluster or rebalance
  hosted clusters across management clusters.

## Related

- Parent epic: [AROSLSRE-1027](https://redhat.atlassian.net/browse/AROSLSRE-1027) —
  Resource management for ARO-HCP components
- Sub-task: [AROSLSRE-1068](https://redhat.atlassian.net/browse/AROSLSRE-1068) —
  Manage resources: kube-applier
- Alert source: `observability/alerts/kubeApplierResources-prometheusRule.yaml`
