# HyperShift Operator Alerts

These alerts catch a HyperShift Operator or Control Plane Operator (CPO) that is
down or wedged. They complement the backend operation (LRO) alerts: they fire
before customer operations start failing, and they still fire when no customer
is making changes, so there is no LRO traffic to fail.

Runbook: <https://aka.ms/arohcp-runbook-hypershift>

Rules: [`observability/alerts/hypershiftOperatorUserJourney-prometheusRule.yaml`](../../observability/alerts/hypershiftOperatorUserJourney-prometheusRule.yaml) (HyperShift Operator) and [`observability/alerts/hypershiftUserJourney-prometheusRule.yaml`](../../observability/alerts/hypershiftUserJourney-prometheusRule.yaml) (CPO).
Dashboard: **HyperShift User Journey SLO** (`observability/grafana-dashboards/sre/user-journey/hypershift.json`).

Other HyperShift signals (reconcile errors and latency, saturation, traffic,
HostedCluster health) are dashboard-only. Use them as triage context; they do
not create tickets.

## Dashboard SLI accounting

The dashboard extensions in ARO-27788 reuse the ARO-27787 dashboard and alerts.
They do not change the paging policy.

Dashboard accounting and the burn-rate alerts proposed in
[Azure/ARO-HCP#7989](https://github.com/Azure/ARO-HCP/pull/7989) intentionally use
different recordings:

| Consumer | Operator recording | CPO recording | Accounting |
|---|---|---|---|
| Dashboard availability, coverage, and budgets | `hypershift:operator:availability:ratio1m` | `hypershift:cpo:availability:ratio1m` | Good fraction from one-minute raw scrape windows, with conservative HA aggregation |
| Component availability burn-rate alerts | `hypershift:operator:replicas_unavailable:ratio` | `hypershift:cpo:replicas_unavailable:ratio` | Point-in-time bad fraction: `1 - min(available / desired, 1)`, using maximum available replicas across HA scrapes |

The dashboard preserves short outages between evaluations; burn-rate recordings
sample the current replica state. Different sampling and HA aggregation mean
`1 - availability:ratio1m` is not an exact replacement for
`replicas_unavailable:ratio`. Dashboard budgets describe dashboard observations,
not the exact budget consumed by the burn-rate alerts. Both exclude zero-desired
replicas and missing deployment inputs; dashboard summaries additionally require
complete current inventory and expose observation coverage.

Both operator recordings and the alerting reconciliation inputs share
`hypershiftUserJourney-recordingRule.yaml` and its
`hypershift-operator-recording-rules` group. Both CPO availability recordings and
the alerting reconciliation inputs share
`hypershiftControlPlaneOperator-recordingRule.yaml` and its
`hypershift-cpo-recording-rules` group. The HCP configuration includes that source
once; there is no second dashboard-only CPO source or duplicate Bicep resource.

The consolidated recordings include the inputs proposed in the alerting PR,
without adding its alerts or changing paging policy. When integrating that PR,
retain these combined sources, regenerate the services/HCP Bicep outputs rather
than concatenating generated resources, and merge its alert runbooks into this
document while preserving the accounting distinction above.

- Component objectives remain proposed: 99.95% replica availability, 99.9%
  successful reconciliations, and 99% of reconciliations within 10 seconds,
  measured over 30 days. Revisit these proposals with owners after historical
  telemetry becomes available; approval and baselining are deferred operational
  follow-ups, not closure gates for dashboard delivery without live data.
- Availability recording rules summarize raw deployment scrape samples over
  each minute before recording the ratio. The lowest replica-averaged
  availability is used conservatively so staggered HA scrapes cannot mask
  outages between rule evaluations. Deployments scaled to zero are excluded.
  Summaries and availability budgets require a current recording for every known
  eligible deployment; missing observations are not replaced with healthy values.
- Reporting, current/historical availability, observation coverage, and budgets
  also cross-check the operators' independently scraped `up` targets against
  deployment inventory, by management cluster for the operator and by cluster
  and namespace for CPOs. Failed scrapes (`up=0`) still identify expected targets.
  If a target loses both deployment inputs, another healthy cluster cannot
  manufacture 100% reporting: the affected family shows **No data**. If the
  independent target inventory is absent, coverage is unknown. Explicit zero
  desired replicas remain excluded.
- CPO reporting, availability, observation coverage, and budgets have a
  five-minute first-observation grace period. A CPO becomes eligible when its
  cluster/namespace has an `up` sample at least five minutes old within the
  30-day lookback. This uses existing telemetry, not a creation timestamp.
  Historical samples preserve eligibility across pod rotations and scrape gaps.
  New CPOs are excluded from summary inputs and inventory checks; once grace
  expires, a missing recording reduces reporting and makes availability summaries
  **No data**. A fleet containing only new CPOs also remains **No data**, rather
  than manufacturing healthy availability.
- Check both current reporting and 30-day observation coverage. Recording-rule
  rollout, new deployments, intentional scale-to-zero, and telemetry gaps
  reduce historical coverage. Partial-window availability is not proof of
  full-window compliance.
- Completed provisioning reuses the existing 95% create-success and 95%
  successful-create timeliness (less than 20 minutes) definitions. The trailing
  3-day window fits within the operation gauges' 7-day retention. Canceled
  operations are excluded; wait gauges are diagnostic, not completion evidence.
- Remaining budgets use `100 * (1 - bad_fraction / allowed_bad_fraction)`.
  Negative values indicate overspending and are intentionally retained.
- Zero wait counts require reporting HostedCluster inventory; zero KAS counts
  require reporting KAS availability. Missing inventory must not manufacture a
  healthy zero.

## Operational capacity guides (not SLIs)

- Management-node CPU, memory, disk, and pod capacity gauges include shared and
  HCP workloads. Their colours are advisory utilization/headroom markers, not
  validated safety limits or approved business SLOs. These panels are outside
  the SLO overview and do not contribute to compliance, error budgets, or
  burn-rate alerts. Missing capacity metrics show **No data**.
- CPU, memory, and disk maxima require reporting from every node in current
  `kube_node_info` inventory. Its `node` label identifies the managed node-exporter
  `instance`, as in the existing AKS performance dashboard. CPU rates also require
  current idle counters; historical rate samples alone do not establish coverage.
  Disk coverage remembers filesystem identities observed in any of its three
  inputs over 30 days, restricted to current nodes. Missing available bytes,
  positive size, or read-only status makes the gauge **No data**, even when other
  nodes or mounts remain healthy. Explicitly read-only, zero-size, virtual, and
  empty-mountpoint filesystems remain excluded. Without an independent mount
  inventory, an intentional unmount also remains unknown until its last samples
  expire from that window; removing a node from current inventory retires its
  filesystems immediately.

Threshold provenance:

| Gauge | Yellow / red | Source and interpretation |
|---|---|---|
| CPU | 60% / 80% used | Reuses the Top Nodes by CPU Utilization colours in [Management Cluster Capacity Planning](../../observability/grafana-dashboards/sre/user-journey/mgmt-capacity-planning.json) |
| Memory | 60% / 80% used | Reuses the Top Nodes by Memory Utilization colours in the same capacity dashboard |
| Disk | 75% / 85% used | Red maps the rendered node-exporter `NodeFilesystemSpaceFillingUp` warning's [15% free-space boundary](../../observability/prometheus/testdata/zz_fixture_TestHelmTemplate_helmtest_mgmt_resources.yaml#L70262); yellow is a dashboard-selected earlier headroom marker. The gauge does not reproduce the alert's forecast or duration gates |
| Pod slots | 60% / 80% used | Dashboard-selected provisional markers following the CPU/memory colour convention, representing nominal 40% / 20% pod-slot headroom; not an approved pod-capacity objective or measured safe HCP density |

Review and baseline these guides with owners; a red capacity gauge is not an SLO
breach or an exhausted error budget.

## Dashboard validation

Validate changes with the recording-rule generators and
`cd tooling/prometheus-rules && go test ./...`. `TestHyperShiftDashboardQueries`
parses the actual dashboard expressions and evaluates completed provisioning,
budget overspending, idle traffic, and missing-telemetry cases with Promtool.
These expression tests do not validate Grafana rendering or establish real SLO
compliance. Before/after screenshots and clearly labeled synthetic healthy,
breached, idle/no-traffic, and missing-data renders are still required for visual
review; none are attached as delivery evidence. Live telemetry is unavailable
and is not required for this delivery.
Once telemetry exists, verify collection, query labels, and regional datasource
pairing, then baseline historical behavior and review proposed targets with
owners.

## The alerts at a glance

| Alert | Severity | Fires when |
|---|---|---|
| `hypershiftOperatorPodsUnavailable` | SEV 3 | 1 or more HyperShift Operator pods (desired minus available) have been unavailable for 15m |
| `hypershiftOperatorPodsDown` | SEV 2 | 2 or more HyperShift Operator pods have been unavailable for 5m |
| `hypershiftOperatorIdle` | SEV 3 | The HyperShift Operator has reconciled nothing for 30m while HostedClusters exist on the management cluster |
| `hypershiftCPODown` | SEV 3 | One hosted control plane's CPO has had no available pods for 10m |

`hypershiftOperatorIdle` does not fire when the operator has no pods, because
there is no reconcile series. In that case `hypershiftOperatorPods*`
fires instead. A CPO deployment scaled to zero (for example, hibernating or
deleting) does not fire `hypershiftCPODown`.

## SOP: `hypershiftOperatorPodsUnavailable` / `hypershiftOperatorPodsDown`

1. Confirm on the dashboard (**Current Component Availability**) that operator
   pods in the `hypershift` namespace on the management cluster from the alert
   are unavailable.
2. Check pod status and events for CrashLoopBackOff, image pull failures, or
   pods that cannot be scheduled. Check the management cluster's node health.
3. Check whether a HyperShift rollout or image bump went out recently. If it
   did, roll it back through the deployment pipeline.
4. Otherwise, collect pod logs and events and file a bug with the HyperShift
   team.
5. Check the backend operation alerts for the same region to see whether
   customers are already affected.

## SOP: `hypershiftOperatorIdle`

1. Confirm on the dashboard (**Reconciliations and Queue Adds per Second (Traffic)**)
   that the reconcile rate is zero while HostedClusters exist.
2. Check operator pod logs for leader-election failures, a lost lease, or a
   stuck loop, and check whether the work queue is growing (**Workqueue
   Depth (Informational)**).
3. Collect the logs and metrics and file a bug with the HyperShift team.
   Restarting the operator requires approval; do not make manual changes
   without it.

## SOP: `hypershiftCPODown`

1. Map the namespace in the alert (`ocm-<prefix>-<cluster id>-<name>`) to the
   customer cluster.
2. Check CPO pod status and events in that namespace for CrashLoopBackOff,
   image pull failures, or pods that cannot be scheduled.
3. If several namespaces on the same management cluster are firing, treat it as
   a management cluster or rollout problem: check node health and recent
   HyperShift or OCP image rollouts.
4. Otherwise, collect pod logs and events and file a bug with the HyperShift
   team.
