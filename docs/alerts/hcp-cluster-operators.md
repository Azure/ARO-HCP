# HCP Cluster Operator Health Alerts

These alerts watch the health of the OpenShift cluster operators running inside
each hosted cluster, as seen from the management-cluster vantage point. Every
hosted cluster's Cluster Version Operator (CVO) exposes one
`cluster_operator_conditions` series per `(operator, condition)` pair, and these
series are federated to the HCP Azure Monitor Workspace (the `hcps-*`
datasource). The alerts below turn that signal into incidents when a cluster
operator is unavailable or degraded, or when the version operator itself is
failing for a reason no individual component operator explains. The goal is to
catch genuine control-plane problems while staying quiet for states that are
expected (a cluster with no worker nodes) or not actionable by us (a console
route that a customer's network has made unreachable).

Until a dedicated runbook exists, the alerts link here for context:
<https://github.com/Azure/ARO-HCP/blob/main/docs/alerts/hcp-cluster-operators.md>

## The alerts at a glance

- **`HCPClusterOperatorUnavailable`** (critical / SEV 2, **temporarily reduced to
  warning / SEV 3**, `for: 30m`) — fires when one or more cluster operators,
  *excluding the version and console operators*, report `Available=false` on a
  cluster that has worker nodes.
- **`HCPClusterOperatorDegraded`** (info / SEV 4, `for: 2h`) — fires when one or
  more cluster operators, *excluding the version and console operators*, report
  `Degraded=true` on a cluster that has worker nodes.
- **`HCPClusterVersionFailing`** (warning / SEV 3, `for: 1h`) — fires when *only*
  the version operator is `Failing` and *no other* operator is unavailable or
  degraded, isolating the version operator's own failure modes.
- **`HCPClusterOperatorFlapping`** (not implemented) — there is currently no
  alert for an operator that rapidly oscillates between healthy and unhealthy;
  such an operator can stay below every `for:` threshold above and so escape all
  three alerts. See [The missing Flapping alert](#the-missing-flapping-alert).

## Gating on worker-node presence

The two per-operator alerts only fire for hosted clusters that have worker
nodes. The gate is expressed as an intersection with the worker-node signal:

```promql
... and on (cluster, namespace) (
  sum by (cluster, namespace) (node_collector_zone_size) > 0
)
```

`node_collector_zone_size` is emitted by the kube-controller-manager per hosted
cluster; a series with value `> 0` means the cluster has worker nodes. The gate
exists because a number of cluster operators — `ingress`, `console`,
`image-registry`, `dns`, `node-tuning`, `storage` — manage operands that
schedule on data-plane worker nodes. On a cluster with zero workers those
operators are *expected* to be unavailable or degraded. That is a customer
topology choice, not a platform incident, so alerting on it would be pure noise.

This gate has one known limitation: it keys on the *presence* of a non-zero
`node_collector_zone_size` series. If that metric stops being scraped on a
cluster that genuinely has workers, absence is read as "no workers" and a real
problem is suppressed (a false negative). There is a corroborating worker signal
in a different datasource (ACM's managed-cluster worker count), but a single
PrometheusRule evaluates against one datasource and cannot join across them, so
it cannot be used to harden the gate in-alert.

The version alert deliberately does **not** carry this gate; see its section
below for why it is unnecessary there.

## Why the console operator is excluded

The console operator can be degraded due to user misconfiguration (for example
auth stack issues) which is not actionable by SRE. If console health needs to be
detected, it requires a separate signal path that can distinguish platform
failures from user-caused ones.

## `HCPClusterOperatorUnavailable`

Fires when at least one cluster operator other than `version` and `console`
reports `Available=false` for longer than 30 minutes on a cluster with worker
nodes. An operator reporting unavailable means the component it manages is down,
not merely providing reduced service — for example `ingress` being unavailable
means cluster ingress is not serving. This is the most urgent of the three,
hence critical / SEV 2 and the comparatively short 30-minute window.

> **Temporary:** this alert is currently shipped at warning / SEV 3 rather than
> critical / SEV 2 while its firing behaviour is observed in production. The
> reduction lives in its own commit and is reverted to restore critical / SEV 2.

The `$value` of the alert is the number of distinct operators that are
unavailable on that cluster, so a single incident can communicate that more than
one operator is down. Because the expression reads the operators directly, this
alert fires even during an initial cluster install, when the version operator
has not yet rolled the failure up into its own conditions.

## `HCPClusterOperatorDegraded`

Fires when at least one cluster operator other than `version` and `console`
reports `Degraded=true` for longer than 2 hours on a cluster with worker nodes.
A degraded operator is still `Available` — it is serving, but at reduced quality
or with reconciliation problems. This is a lower-urgency signal than
unavailability, hence info / SEV 4 and the long 2-hour window that filters out
transient degradation. As with the unavailable alert, `$value` is the count of
degraded operators on the cluster.

Keeping this separate from `HCPClusterOperatorUnavailable` is deliberate: the two
states differ in both severity and how long they should persist before a human
is involved, and a single combined alert could not express that split cleanly.

## `HCPClusterVersionFailing`

This alert is *only* about the version operator, and it is the one alert that
needs special treatment because the version operator reports differently from
every other operator.

`name="version"` is not an ordinary cluster operator. It is the `ClusterVersion`
object, and its `Failing` condition is a roll-up: the CVO reads every constituent
operator's health and folds it into `version Failing`. That roll-up is why we do
**not** want to treat `version` like the other operators in the per-operator
alerts — doing so would double-count every component failure that the per-operator
alerts already catch. So the per-operator alerts exclude `version`, and this
alert covers it on its own.

But the version operator cannot simply be ignored either, because its `Failing`
condition has **its own failure modes that no component operator reports**. The
CVO sets `version Failing` whenever its reconcile loop returns an error, and
several of those errors never touch any individual operator's conditions:

- **Release payload retrieval fails** — the CVO cannot pull the release image, so
  no operator manifests are ever applied.
- **Release signature verification fails** — the payload is rejected before
  anything is applied.
- **The payload will not load or parse** — manifests never reach the operators.
- **A cluster-scoped manifest apply is rejected** — a CRD, RBAC object,
  namespace, or similar is rejected (for example by a failing admission webhook
  or a quota), and the rejected object is not a ClusterOperator, so no operator
  condition changes.
- **An upgrade precondition blocks** — for example a rollback, a too-large
  version hop, or an "upgradeable=false" gate. The upgrade is refused before it
  starts and no operator is involved.

In all of these, `cluster_operator_conditions{name!="version"}` shows nothing
broken even though `version` is `Failing`. The per-operator alerts are blind to
this entire class of failure by construction; this alert is the only thing that
catches it.

To isolate exactly that case, the expression fires on `version Failing` *unless*
some other operator is itself unavailable or degraded:

```promql
count by (cluster, namespace) (
  cluster_operator_conditions{name="version", condition="failing"} == 1
)
unless on (cluster, namespace)
count by (cluster, namespace) (
  (cluster_operator_conditions{condition="available", name!="version"} == 0)
  or
  (cluster_operator_conditions{condition="degraded", name!="version"} == 1)
)
```

If any other operator explains the failure, that operator is the actionable
signal and one of the per-operator alerts handles it — so this alert stays quiet
to avoid a redundant second incident for the same root cause. The console
operator is intentionally part of the subtracted set, so a `version Failing`
caused only by console is suppressed here just as it is excluded from the
per-operator alerts.

This alert needs no worker-node gate. On a cluster with no workers, the
node-dependent operators (`ingress`, `dns`, `storage`, and so on) are themselves
unavailable or degraded, so the `unless` clause already subtracts that cluster
out — the gate would be redundant. Omitting it keeps the expression shorter and
has a small upside: if a no-worker cluster hits one of the version-only failure
modes above while its operators happen to read healthy, this alert can still
catch it.

Two properties follow from this design and are worth knowing when responding:

- It is **slower** than the per-operator alerts at catching component failures,
  because the CVO applies its own delay before rolling a steadily-degraded
  operator into `version Failing` (an unavailable operator rolls up promptly, a
  degraded one only after an internal grace period). When a component is the
  cause, prefer the per-operator alert; this one is the backstop.
- It does **not** fire during an initial cluster install for component problems,
  because the CVO does not roll component failures into `version Failing` while
  the cluster is still initializing. The per-operator alerts cover that window.

## The missing Flapping alert

There is intentionally no flapping alert yet, and that leaves a known gap.

All three alerts above use a `for:` duration, which Prometheus resets the moment
the expression stops matching. An operator that oscillates — unavailable for
twenty minutes, healthy for five, unavailable again — can therefore stay below
the 30-minute, 1-hour, and 2-hour thresholds indefinitely and never fire any of
them, even though it spent most of an hour unhealthy. A flapping control-plane
operator (for instance `kube-apiserver`) is arguably worse than a cleanly
unavailable one, and today nothing here catches it.

A flapping alert would be built on the same metric, wrapping it in a `changes()`
window rather than a point-in-time match, roughly:

```promql
max by (cluster, namespace, name) (
  changes(cluster_operator_conditions{condition="available", name!~"version|console"}[15m]) > 3
)
```

Until such an alert is added, sustained-but-intermittent operator unhealthiness
can go unalerted; treat the three alerts above as covering *steady* states, not
oscillating ones.
