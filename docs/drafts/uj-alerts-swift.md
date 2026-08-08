# SWIFT Networking: Alerting Design (ARO-25978)


## File locations

```
observability/alerts/swift-networking-recordingRule-KSM.yaml       # recording rules
observability/alerts/swift-networking-recordingRule-KSM_test.yaml  # recording rule tests
observability/alerts/swift-networking-prometheusRule.yaml          # alert rules
observability/alerts/swift-networking-prometheusRule_test.yaml     # alert tests
```

Register in the appropriate lane config (see [Routing lanes](#routing-lanes) below). Run `make alerts` in `tooling/prometheus-rules/` to generate Bicep. Format with `az bicep format`.

See [docs/prometheus-rules.md](https://github.com/Azure/ARO-HCP/blob/main/docs/prometheus-rules.md) for full authoring guide.

## ADR-001 Requirements Summary

- **Naming:** `{Scope}{Subject}{Metric}{BurnRateTier}`, e.g. `userJourneySwiftLatencyP991h5m`
- **Multi-window multi-burn-rate** for Availability, Errors, Latency
- **Threshold-based** for Traffic and Saturation
- **Burn-rate tiers:** Fast (1h/5m, 14.4x), Medium (6h/30m, 6x), Slow (3d/6h, 1x)
- **Severity:** Sev 3 for customer-facing journey degradation; Sev 4 for component-internal
- **`runbook_url`:** every alert must link to the **runbook** via an `aka.ms/arohcp-runbook-{shortname}` short-link (the chain is alert → runbook → TSG, per ADR-001 Section 3)
- **`correlationId`:** required on every alert; granularity must be set explicitly:
  - Default = per-cluster
  - Per-subscription for stuck-operation alerts (so per-customer suppression doesn't mask other customers)
  - Per-namespace for per-HCP alerts
- **Summary annotation:** must NOT include `{{ $labels.cluster }}` because the RP bicep template already prepends cluster name to every IcM title; including it doubles the title

## Routing lanes

| Lane | Config file | Use for |
|---|---|---|
| RP (per-HCP) | `observability/alerts-rp-hcps.yaml` | Per-HCP alerts carrying `_id`, `namespace`, `subscription_id` |
| RP (fleet/services) | `observability/alerts-rp-services.yaml` | Fleet-aggregate alerts firing per region (`by (cluster)` only) |
| SL | `observability/alerts-sl-services.yaml` | Component-internal, no current customer impact |

Both RP lanes route to `icm-action-group-rp`.

**SWIFT routing:**
- `userJourneySwift*` latency/error alerts: **RP per-HCP** (`alerts-rp-hcps.yaml`), fires per router pod namespace
- `SwiftCNSAvailability3d`, `SwiftPendingProgramming`: **SRE HCP** (`alerts-sre-hcps.yaml`), AKS-managed infrastructure signals, consistent with `MgmtClusterNodeSwiftNICCapacityZero`

**ADR-001 rule:** fleet-aggregate alerts must NOT filter by subscription. Internal-sub failures indicate the same bugs affecting customers.

## Alert Inventory

### Journey-level alerts

**`userJourneySwiftLatencyP991h5m`** - Sev 3 - RP lane
```yaml
alert: userJourneySwiftLatencyP991h5m
expr: |
  router:startup_latency:p99_avg_5m > 300
  and
  router:startup_latency:p99_avg_1h > 300
for: 2m
labels:
  severity: "3"
  long_window: 1h
  short_window: 5m
annotations:
  summary: "SWIFT router pod startup latency p99 elevated (fast burn)"
  runbook_url: "https://aka.ms/arohcp-runbook-swift"
```

**`userJourneySwiftLatencyP996h30m`** - Sev 3 - RP lane
```yaml
alert: userJourneySwiftLatencyP996h30m
expr: |
  router:startup_latency:p99_avg_30m > 300
  and
  router:startup_latency:p99_avg_6h > 300
for: 15m
labels:
  severity: "3"
  long_window: 6h
  short_window: 30m
annotations:
  summary: "SWIFT router pod startup latency p99 elevated (medium burn)"
  runbook_url: "https://aka.ms/arohcp-runbook-swift"
```

**`SwiftCNSAvailability3d`** - Sev 4 - SRE HCP lane (`alerts-sre-hcps.yaml`)
- CNS daemonset replica ratio slow-burn
- Basis: `kube_daemonset_status_number_ready / kube_daemonset_status_desired_number_scheduled`
- SLO: ≥ 99.9% over 28 days
- Threshold alert, not burn-rate (binary availability ratio)

**`SwiftPendingProgramming`** - Sev 4 - SRE HCP lane (`alerts-sre-hcps.yaml`)
- `cx_pending_programming_ips_v2 > 0` sustained for 15m
- Threshold alert, not burn-rate: pending-programming is a binary state (IPs stuck or not), not a ratio
Per ADR-001, user journey alerts typically wire Fast + Medium only. The slow tier requires a 3-day measurement window (`router:startup_latency:p99_avg_3d`), which is not defined in this PR. Add in a follow-up once production baseline data is available.

---

**`userJourneySwiftErrors1h5m`** - Sev 3 - RP lane
- Basis: `http_request_latency_seconds_count{url=~".*/requestipconfig.*", cns_return_code!="0"}` error rate fast-burn

**`userJourneySwiftErrors6h30m`** - Sev 3 - RP lane
- Same, medium-burn

**`userJourneySwiftCNSLatencyP991h5m`** - Sev 3 - RP lane
- Basis: `ip_assignment_latency_seconds` histogram p99 > 10s, fast-burn (1h/5m windows)

**`userJourneySwiftCNSLatencyP996h30m`** - Sev 3 - RP lane
- Same, medium-burn (6h/30m windows)

## aka.ms Short-links

| Short-link | Points to |
|---|---|
| `aka.ms/arohcp-runbook-swift` | This runbook (SWIFT Networking User Journey Runbook) |
| `aka.ms/arohcp-tsg-swift` | SWIFT Networking TSG |
| `aka.ms/arohcp-dashboard-swift` | SWIFT Networking Grafana dashboard |

## promtool Test Requirements

Every alert must have a `_test.yaml` with passing promtool tests. Tests must cover:
- Alert fires when threshold is breached
- Alert does not fire when metric is healthy (absence of series = no fire for latency alerts)
- For multi-window alerts: both windows must exceed the threshold for the alert to fire

## Recording Rules

Recording rules live alongside the alert file or in a dedicated `swift-recording-rules.yaml`. The full chain is defined in [uj-slislo-swift.md](uj-slislo-swift.md).

## References

- [ARO HCP Alerting Recommendation ADR](https://github.com/openshift-online/architecture/pull/79)
- [docs/alerts.md](https://github.com/Azure/ARO-HCP/blob/main/docs/alerts.md): severity mapping, correlationId, IcM title rendering
- [docs/prometheus-rules.md](https://github.com/Azure/ARO-HCP/blob/main/docs/prometheus-rules.md): authoring, testing, generating Bicep
- [Observability code](https://github.com/Azure/ARO-HCP/tree/main/observability): existing alert and recording rule examples
