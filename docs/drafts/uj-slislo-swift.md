# SWIFT Networking: SLI/SLO Design (ARO-25919)

## ADR-001 Baseline Requirements

Every user journey must have SLIs and SLOs for all five baseline metrics:

| Baseline metric | SWIFT SLI |
|---|---|
| **Availability** | Router pod startup p99 ≤ 300s (via kube-state-metrics recording rule) |
| **Errors** | CNS `requestipconfig` error rate (`http_request_latency_seconds`, `cns_return_code != "0"`) |
| **Latency** | CNS IP assignment latency p99 (`ip_assignment_latency_seconds`) |
| **Traffic** | CNS `requestipconfig` call rate (`http_request_latency_seconds_count`) |
| **Saturation** | SWIFT NIC utilization per node (`aro.openshift.io/swift-nic` extended resource ratio) |

SLIs must be expressed as good events / total events (ratio format) per ADR-001.

## Measurement Challenge (Brendan Bergen, Jun 2026)

Measuring SWIFT health directly is hard because the managed components (CNS, DNC-RC, NRP) are largely opaque. The SLIs below measure SWIFT's observable effects (router pod startup latency, NIC scheduler capacity, CNS IP assignment) rather than the SWIFT internals.

**NodePool caveat:** HCPs can be provisioned with zero NodePools. Every metric must be interpreted in context: a zero reading may mean "nothing to schedule" rather than "broken". Gate metrics on NodePools existing and scaled > 0.

## Additional Signals to Consider

1. **Konnectivity error rate / queue depth:** can HCP KAS talk to worker nodes? (HCP->Node direction)
2. **Node `Ready` condition ratio gated on NodePool count:** can worker nodes talk to KAS? (Node->HCP direction)
3. **Router Pod SWIFT NIC traffic drought or saturation:** is traffic flowing through the SWIFT path?
4. **Router Pods `Pending`/`Unschedulable`:** mgmt-agent didn't report NICs, NICs not provisioned, or scheduler can't place pods

## Journey-level SLI: Router pod startup latency

SLI: p99 duration a router pod spends in `Pending`/`ContainerCreating` state across all management clusters.

**Why this is the journey SLI:** A simple readiness ratio is unsuitable because new HCPs legitimately have pods in `ContainerCreating` during normal provisioning. Startup latency is the authoritative signal that SWIFT NIC assignment is stalled.

**Why kube-state-metrics is authoritative:** kube-state-metrics reads directly from the Kubernetes API server, which is the system of record for pod state.

**Recording rule chain:**
```yaml
# Base: per-pod duration in ContainerCreating (no series when healthy)
- record: router:startup_latency:seconds
  expr: |
    (time() - kube_pod_created{namespace=~"ocm-.*"})
    * on(namespace, pod) kube_pod_owner{owner_kind="ReplicaSet", owner_name=~"router-.*"}
    * on(namespace, pod) (kube_pod_status_phase{phase="Pending"} == 1)

# p99 across all stuck router pods (no series when none stuck)
- record: router:startup_latency:p99
  expr: quantile(0.99, router:startup_latency:seconds)

# Window averages for multi-burn-rate alert expressions
- record: router:startup_latency:p99_avg_5m
  expr: avg_over_time(router:startup_latency:p99[5m])
- record: router:startup_latency:p99_avg_30m
  expr: avg_over_time(router:startup_latency:p99[30m])
- record: router:startup_latency:p99_avg_1h
  expr: avg_over_time(router:startup_latency:p99[1h])
- record: router:startup_latency:p99_avg_6h
  expr: avg_over_time(router:startup_latency:p99[6h])
```

Design decisions:
- No threshold baked into recording rules (follows repo convention: `kas:`, `hostedClusterAPI_kubeapiserver_available`)
- Absence of series = healthy (alert correctly does not fire)
- `kube_pod_status_phase{phase="Pending"}` covers both `Pending` and `ContainerCreating` (both show as Pending in kube-state-metrics)

**SLO:** router pod startup latency p99 ≤ 300s
- Rationale: a router pod stuck > 5 minutes indicates a SWIFT NIC assignment failure, not normal startup latency

### Component-level: SWIFT NIC saturation per node

SLI: ratio of SWIFT NIC slots in use to total NIC capacity per node.

```promql
sum by (node) (kube_pod_container_resource_requests{resource="aro.openshift.io/swift-nic"})
/
kube_node_status_capacity{resource="aro.openshift.io/swift-nic"}
```

Use for capacity planning dashboards. Scale-out is triggered automatically by the cluster autoscaler, so this metric does not require an SRE alert.

### Component-level: CNS daemonset availability

SLI: fraction of SWIFT-enabled management cluster worker nodes with CNS running.

```promql
kube_daemonset_status_number_ready{daemonset="azure-cns", namespace="kube-system"}
/
kube_daemonset_status_desired_number_scheduled{daemonset="azure-cns", namespace="kube-system"}
```

**SLO:** ≥ 99.9% of nodes have CNS running (28-day window)

## CNS PodMonitor SLIs

**Traffic:** CNS `requestipconfig` call rate
```promql
sum(rate(http_request_latency_seconds_count{url=~".*/requestipconfig.*"}[5m]))
```

**Errors:** CNS IP assignment error rate
```promql
sum(rate(http_request_latency_seconds_count{url=~".*/requestipconfig.*", cns_return_code!="0"}[5m]))
/
sum(rate(http_request_latency_seconds_count{url=~".*/requestipconfig.*"}[5m]))
```
Note: CNS endpoint is `POST /network/requestipconfig` (not `requestIPAddress`; the Go function name differs from the URL path).

**Latency:** CNS IP assignment p99
```promql
histogram_quantile(0.99, sum(rate(ip_assignment_latency_seconds_bucket[5m])) by (le))
```

**Saturation (proxy):** IPs stuck in PendingProgramming. Sustained non-zero is the best available proxy for stuck MTPNCs while MTPNC reconciler emits no metrics.
```promql
cx_pending_programming_ips_v2
```

**Pool state (diagnostic, no SLO):**
```promql
sum by (instance) (cx_assigned_ips_v2) / sum by (instance) (cx_ipam_max_ips)   # IP pool utilization per node
cx_ipam_requested_ips - cx_ipam_total_ips     # divergence = DNC not fulfilling requests
cx_ipam_subnet_exhaustion_state > 0           # subnet full
```

## SLO Targets

| SLI | Proposed target | Window | Notes |
|---|---|---|---|
| Router pod startup latency p99 | ≤ 300s | per-event | |
| CNS daemonset availability | ≥ 99.9% | 28 days | |
| IP assignment error rate | ≤ 1% | 28 days | |
| IP assignment latency p99 | ≤ 10s | 28 days | |

## References

- [ARO HCP Alerting Recommendation ADR](https://github.com/openshift-online/architecture/pull/79): SLI design, baseline metrics, coverage model
- [ARO SLO Policy ADR](https://github.com/swiencki/architecture/pull/1) [Draft]: SLO target-setting, error budgets
- [Monitoring / Metrics / Alerting Recommendation for ARO HCP](https://docs.google.com/document/d/11WCoJa7E8X9dalgzwtnCiPeRHD72AkoqMLG71gK61OA/edit)
- ARO Classic & HCP: SLI/SLO meeting (weekly, 17:00 UTC), #external-wg-aro-hcp
