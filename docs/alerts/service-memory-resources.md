# Service Memory Resource Alerts

These alerts detect when any ARO-HCP service's actual memory usage drifts above
its configured request, catching growth before it leads to OOM kills or
evictions. Memory requests are configured per-service in `config/config.yaml`
under each service's `resources.requests.memory` field.

## The alerts at a glance

- **`ServiceMemoryDrift`** (warning / SEV 3, `for: 15m`) — fires when a
  container's working set exceeds 1.5× its memory request for 15 minutes.
- **`ServiceMemoryTrend`** (info / SEV 4, `for: 30m`) — fires when
  `predict_linear` over a 6-hour window projects a container's memory will
  exceed 2× its request within 4 hours.

## Scope

These alerts cover all ARO-HCP service namespaces:
`aro-hcp`, `aro-hcp-admin-api`, `aro-hcp-exporter`, `clusters-service`,
`fleet`, `kube-applier`, `maestro`, `mgmt-agent`, `secret-sync-controller`,
`sessiongate`.

Only containers with a configured memory request will fire (the PromQL join
against `kube_pod_container_resource_requests` naturally excludes untuned
services).

## What causes these alerts to fire

- **Memory leak** in the service or one of its dependencies.
- **Workload growth** — more traffic, more clusters, larger payloads.
- **Request set too low** — the initial sizing was based on a lighter workload
  than production now runs.

## Investigation steps

1. **Confirm the alert is genuine** — open the Ad-hoc Explorer dashboard in
   Grafana and query:
   ```promql
   container_memory_working_set_bytes{container="<container>", namespace="<namespace>"}
   ```
   Compare to the configured request value from `config/config.yaml`.

2. **Check for recent changes** — query Kusto for recent deployments or version
   changes of the affected service on the affected cluster. Check whether
   traffic or workload volume has recently increased.

3. **Check pod restarts via Kusto** — query `KubePodInventory` for the service
   namespace on the affected cluster to check restart counts and OOMKill events.
   Frequent restarts combined with this alert suggest a memory leak.

4. **Look at the growth pattern**:
   - Sudden jump → likely a new workload or config change.
   - Steady linear growth → likely a memory leak.
   - Stepped increases over days → workload growth.

## Remediation

- **Right-size the request**: If usage has legitimately grown, update the memory
  request in `config/config.yaml` for the affected service and roll out. The
  request should be set to observed peak usage × 1.25 safety margin.
- **Investigate a leak**: If growth is unbounded and linear, file a bug against
  the affected service and consider adding a memory limit as a short-term
  guardrail.
- **Scale out**: If the growth is proportional to workload volume, this is
  expected — consider horizontal scaling or workload rebalancing.

## Related

- Parent epic: [AROSLSRE-1027](https://redhat.atlassian.net/browse/AROSLSRE-1027) —
  Resource management for ARO-HCP components
- Ticket: [AROSLSRE-1564](https://redhat.atlassian.net/browse/AROSLSRE-1564) —
  Generic memory drift/trend alerting for all ARO-HCP services
- Alert source: `observability/alerts/serviceMemoryResources-prometheusRule.yaml`
