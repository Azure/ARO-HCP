# Service Resource Request Alerts

These alerts detect when any ARO-HCP service's actual CPU or memory usage drifts
above its configured request, catching growth before it leads to OOM kills,
evictions, or CPU starvation. Requests are configured per-service in
`config/config.yaml` under each service's `k8s.resources.requests.cpu` /
`k8s.resources.requests.memory` field (e.g. `backend.k8s.resources.requests.cpu`;
some components such as `arobit.forwarder` nest `resources` directly).

## The alerts at a glance

- **`ServiceMemoryDrift`** (warning / SEV 3, `for: 5m`) — fires when a
  container's **30-minute average** working set exceeds 1.2× its memory request.
- **`ServiceCPUDrift`** (warning / SEV 3, `for: 5m`) — fires when a container's
  **30-minute average** CPU usage (`rate(container_cpu_usage_seconds_total[5m])`)
  exceeds 1.2× its CPU request.
- **`ServiceMemoryTrend`** (info / SEV 4, `for: 30m`) — fires when
  `predict_linear` over a 6-hour window projects a container's memory will
  exceed 2× its request within 4 hours.

### How "sustained" is measured

The drift alerts intentionally do **not** fire on instantaneous spikes. Each
compares the **average usage/request ratio over the last 30 minutes**
(`avg_over_time(... [30m:1m])`) against 1.2, and only fires once that average
has stayed above the threshold for a further 5 minutes (`for: 5m`).

A data-guard (`count_over_time(...[30m:1m]) >= 30`) requires at least 30 minutes
of samples before either drift alert can fire, so we never page on partial or
warm-up data (for example, right after a pod starts or a scrape gap).

## Scope

These alerts cover all ARO-HCP service namespaces:
`aro-hcp`, `aro-hcp-admin-api`, `aro-hcp-exporter`, `arobit`, `clusters-service`,
`fleet`, `kube-applier`, `maestro`, `mgmt-agent`, `monitoring`, `prometheus`,
`secret-sync-controller`, `sessiongate`.

Only containers with a configured request will fire (the PromQL join against
`kube_pod_container_resource_requests` naturally excludes untuned services).

## What causes these alerts to fire

- **Memory leak** in the service or one of its dependencies (memory only).
- **Workload growth** — more traffic, more clusters, larger payloads.
- **Request set too low** — the initial sizing was based on a lighter workload
  than production now runs.

## Investigation steps

1. **Confirm the alert is genuine** — open the Ad-hoc Explorer dashboard in
   Grafana and query:
   ```promql
   container_memory_working_set_bytes{container="<container>", namespace="<namespace>"}
   # or, for CPU:
   rate(container_cpu_usage_seconds_total{container="<container>", namespace="<namespace>"}[5m])
   ```
   Compare to the configured request value from `config/config.yaml`.

2. **Check for recent changes** — query Kusto for recent deployments or version
   changes of the affected service on the affected cluster. Check whether
   traffic or workload volume has recently increased.

3. **Check pod restarts via Kusto** — query `KubePodInventory` for the service
   namespace on the affected cluster to check restart counts and OOMKill events.
   Frequent restarts combined with the memory alert suggest a memory leak.

4. **Look at the growth pattern**:
   - Sudden jump → likely a new workload or config change.
   - Steady linear growth → likely a memory leak (memory) or a busier hot path
     (CPU).
   - Stepped increases over days → workload growth.

## Remediation

- **Right-size the request**: If usage has legitimately grown, update the CPU or
  memory request in `config/config.yaml` for the affected service and roll out.
  The request should be set to observed peak usage × 1.25 safety margin. The
  `rightsize-requests` tool (`tooling/rightsize-requests`) can compute and apply
  these values automatically from production Grafana data.
- **Investigate a leak**: If memory growth is unbounded and linear, file a bug
  against the affected service and consider adding a memory limit as a
  short-term guardrail.
- **Scale out**: If the growth is proportional to workload volume, this is
  expected — consider horizontal scaling or workload rebalancing.

## Related

- Parent epic: [AROSLSRE-1027](https://redhat.atlassian.net/browse/AROSLSRE-1027) —
  Resource management for ARO-HCP components
- Ticket: [AROSLSRE-1564](https://redhat.atlassian.net/browse/AROSLSRE-1564) —
  Generic memory drift/trend alerting for all ARO-HCP services
- Alert source: `observability/alerts/serviceMemoryResources-prometheusRule.yaml`
- Right-sizing tool: `tooling/rightsize-requests`
