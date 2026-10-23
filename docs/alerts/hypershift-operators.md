# HyperShift Operator Alerts

These alerts catch a HyperShift Operator or Control Plane Operator (CPO) that is
down or wedged. They complement the backend operation (LRO) alerts: they fire
before customer operations start failing, and they still fire when no customer
is making changes, so there is no LRO traffic to fail.

Rules: [`observability/alerts/hypershiftOperatorUserJourney-prometheusRule.yaml`](../../observability/alerts/hypershiftOperatorUserJourney-prometheusRule.yaml) (HyperShift Operator) and [`observability/alerts/hypershiftUserJourney-prometheusRule.yaml`](../../observability/alerts/hypershiftUserJourney-prometheusRule.yaml) (CPO).
Dashboard: **HyperShift User Journey SLO** (`observability/grafana-dashboards/sre/user-journey/hypershift.json`).

Other HyperShift signals (reconcile errors and latency, saturation, traffic,
HostedCluster health) are dashboard-only. Use them as triage context; they do
not create tickets.

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

1. Confirm on the dashboard (**Available / Desired Replicas**) that operator
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
   Depth (Saturation)**).
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
