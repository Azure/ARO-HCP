# Risk Acceptance: Ungated AKS Auto-Upgrades on ARO-HCP Clusters

| | |
|---|---|
| Status | Accepted |
| Accepted on | 2026-10-21 |
| Tracking | [AROSLSRE-1891](https://redhat.atlassian.net/browse/AROSLSRE-1891) (parent epic: [AROSLSRE-798](https://redhat.atlassian.net/browse/AROSLSRE-798)) |
| PMR incidents | ITN-2026-00151, ITN-2026-00158, ITN-2026-00192 |
| Sign-off | Anthony Vollmer, Rael Garcia Arnes, Alexei Fedoret — recorded in [AROSLSRE-1891](https://redhat.atlassian.net/browse/AROSLSRE-1891) |
| Reassess on | Any trigger listed in [Reassessment triggers](#reassessment-triggers) |

This document formally records the residual risk that Red Hat accepts by continuing to
run AKS-managed, unattended auto-upgrades on ARO-HCP management and service clusters,
after Microsoft rejected our request to move those updates behind a gated rollout.

## Background and scope

**Scope**: every ARO-HCP AKS cluster — management clusters and service clusters, in all
environments. Management-cluster Swift V2 worker pools are the sensitive case: their
nodes carry secondary delegated NICs that must have accelerated networking enabled, and
correct cluster operation depends on the AKS platform preserving that per-NIC
configuration across every VMSS operation it performs (upgrades, node-image rolls,
reconciles, instance replacements).

AKS updates on these clusters run in two lanes:

| Lane | How it runs | Where defined |
|---|---|---|
| Kubernetes minor version | **Gated.** Driven by the rollout pipeline (`upgrade-aks-cluster.sh`), which upgrades the cluster to the configured target minor. Rollouts are monitored, gated by E2E tests, and progress Dev → INT → Stage → Prod. | `dev-infrastructure/mgmt-pipeline.yaml`, `dev-infrastructure/svc-pipeline.yaml` |
| Kubernetes patch version and node OS image | **Ungated.** AKS-managed auto-upgrade (`upgradeChannel: 'patch'`, `nodeOSUpgradeChannel: 'NodeImage'`), applied unattended inside weekly maintenance windows (Saturday 22:00 UTC, 10 hours). | `dev-infrastructure/modules/aks-cluster-base.bicep`, `dev-infrastructure/modules/aks/maintenance.bicep` |

## Risk statement

This acceptance covers the **ungated lane**. Red Hat cannot hold, canary, or
pre-validate the patch and node-OS updates that AKS applies on its own weekly schedule,
and Microsoft has declined to change that. Platform regressions in the AKS RP and NRP
code paths that manage Swift V2 secondary NICs have repeatedly broken
management-cluster node pools (see the incident evidence below). Because the updates
run unattended in weekend maintenance windows, the failures they introduce typically
surface only in business hours — in two of the three incidents, detection lagged by
days.

## Incident evidence

Three production-impact incidents within roughly six weeks (May–June 2026) form the
pattern behind this acceptance:

| Incident | AKS-side trigger | Failure mode | Production impact | Detection |
|---|---|---|---|---|
| ITN-2026-00151 (May 2026) | AKS RP regression in US East | Cluster provisioning and deletion failures; three distinct root causes compounding | Customer-facing provisioning failures over ~51 hours | ~2.5-day delay |
| ITN-2026-00158 (June 2026) | AKS management-cluster upgrades 1.35.1 → 1.35.4 | Two NRP bugs blocked **all** VMSS operations on Swift-enabled node pools | Release rollouts and customer workload autoscaling halted fleet-wide; Microsoft asked Red Hat to manually recreate all affected node pools | Surfaced as stuck release rollouts |
| ITN-2026-00192 (June 2026) | AKS RP regression emitting `enableAcceleratedNetworking: false` on the secondary NICs of multi-tenancy node pools (ICM 815156644) | Swift V2 nodes reported zero Swift NIC capacity while appearing `NodeReady` and `Succeeded`; the same regression broke the nodepool self-heal's temporary pools, stalling repair loops | Management-cluster workload scheduling degraded; broken pools needed manual recreation | No alert fired; the same silent condition persisted on INT for two days before the prod incident forced discovery |

The common thread: all three were triggered by AKS-side changes that Red Hat did not
initiate, schedule, or gate. In two of them, detection lagged by days: a ~2.5-day delay
for ITN-2026-00151, and for ITN-2026-00192 the same silent condition persisted on INT
for two days before the prod incident forced discovery.

## The rejected mitigation

The safe alternative was identified and built: drive **all** AKS updates — patch
versions and node OS images, not just minors — through the monitored, E2E-gated rollout
pipeline, and disable AKS-managed auto-upgrade together with the weekend maintenance
windows. The work was completed in
[AROSLSRE-1164](https://redhat.atlassian.net/browse/AROSLSRE-1164) as
[PR #5627](https://github.com/Azure/ARO-HCP/pull/5627) (pipeline-driven patch and
node-image upgrades) and
[PR #5628](https://github.com/Azure/ARO-HCP/pull/5628) (disable AKS-managed
auto-upgrade).

Microsoft rejected that approach, so AROSLSRE-1164 was closed as Won't Do and both PRs
were closed unmerged (the branches are preserved on the author's fork in case the
decision is revisited). Red Hat subsequently re-raised the request, citing the
three-incident pattern above; Microsoft declined again, and no gating mechanism will be
delivered ([AROSLSRE-1891](https://redhat.atlassian.net/browse/AROSLSRE-1891)).

Microsoft **did** fix the known AKS RP regression behind ITN-2026-00192: the fix for
the accelerated-networking flag on secondary NICs (ICM 815156644) shipped in an AKS RP
release and was confirmed mitigated in July 2026. That closes the specific defect but
does not change the structural exposure — future AKS RP or NRP regressions in the Swift
V2 NIC paths will still land unattended and ungated.

## Compensating controls

### In place

- **Accelerated-networking-aware nodepool self-heal**
  ([PR #5649](https://github.com/Azure/ARO-HCP/pull/5649),
  [AROSLSRE-1172](https://redhat.atlassian.net/browse/AROSLSRE-1172)): temporary and
  recreated pools are created at zero replicas, inherit accelerated networking from the
  reference pool, and are re-verified after scale-up; a live Swift pool missing
  accelerated networking is itself a trigger for recreation.
- **Swift NIC capacity reporting in mgmt-agent**
  ([PR #5651](https://github.com/Azure/ARO-HCP/pull/5651)): nodes report their actual
  Swift NIC capacity, making the `swift-nic=0` failure signature observable.
- **`MgmtClusterNodeSwiftNICCapacityZero` alert, deployed fleet-wide**
  ([AROSLSRE-1534](https://redhat.atlassian.net/browse/AROSLSRE-1534)): catches exactly
  the silent failure mode that delayed ITN-2026-00192 detection.
- **Wedged-node detection and mitigation**
  ([node-health controller](../controllers/node-health.md),
  [mitigation runbook](mitigate-swiftv2-wedged-node.md)): automated labeling of wedged
  Swift V2 nodes and a documented recycle procedure.
- **Minor-version upgrades stay pipeline-gated** (see background), so the highest-risk
  version jumps never run unattended.

### Open (tracked, not yet complete)

These reduce but do not eliminate the residual risk:

- Alerting for sustained VMSS write failures and node-pool degradation on management
  clusters ([AROSLSRE-1662](https://redhat.atlassian.net/browse/AROSLSRE-1662) and
  sub-tasks)
- Swift V2 NIC configuration testing with the AKS RP
  ([ARO-28455](https://redhat.atlassian.net/browse/ARO-28455))
- Deployment SLA for critical fixes reaching production
  ([AROSLSRE-1480](https://redhat.atlassian.net/browse/AROSLSRE-1480))

## Residual risk assessment

- **Likelihood: medium-high.** Three production-impact incidents in roughly six weeks
  came from AKS-side regressions in these code paths. The specific known defect is
  fixed, but the AKS RP and NRP code that manages Swift V2 secondary NICs changes
  continuously, and any future regression lands on our clusters without a gate.
- **Impact: high.** The observed worst case (ITN-2026-00158) is simultaneous
  degradation of management clusters: release rollouts halted, customer workload
  autoscaling blocked, and manual recreation of node pools across environments.
- **Detection lag: up to days.** Updates run on weekend maintenance windows; the observed detection delays were 2.5 days (ITN-2026-00151) and 2 days
  (ITN-2026-00192 on INT).

**Rating: high residual risk, accepted** on the conditions that the compensating
controls above remain deployed and the open items stay tracked.

## Acceptance

Red Hat accepts the residual risk of AKS-managed, unattended patch and node-OS
auto-upgrades on ARO-HCP clusters, given that:

1. Microsoft has rejected the gated-upgrade approach and will not deliver a gating
   mechanism.
2. The compensating controls listed above are in place, and the open ones are tracked.
3. This acceptance is time-boxed by the reassessment triggers below, not open-ended.

| Role | Name | Sign-off |
|---|---|---|
| Author | Anthony Vollmer | recorded in [AROSLSRE-1891](https://redhat.atlassian.net/browse/AROSLSRE-1891) |
| Reviewer | Rael Garcia Arnes | recorded in [AROSLSRE-1891](https://redhat.atlassian.net/browse/AROSLSRE-1891) |
| Approver (PMR owner) | Alexei Fedoret | recorded in [AROSLSRE-1891](https://redhat.atlassian.net/browse/AROSLSRE-1891) |

### Reassessment triggers

This acceptance must be revisited, and re-escalated to Microsoft if warranted, when any
of the following occurs:

- Any recurrence of Swift V2 NIC degradation attributable to AKS-initiated operations.
- A material change in AKS auto-upgrade behavior (channels, maintenance window
  semantics, or upgrade mechanics).
- Microsoft offers a supported gating or canary mechanism for AKS updates.
- Any of the open compensating-control items is closed as Won't Do or stays unstarted
  for two consecutive sprints.

## References

- [AROSLSRE-1891](https://redhat.atlassian.net/browse/AROSLSRE-1891) — this risk
  acceptance (parent: [AROSLSRE-798](https://redhat.atlassian.net/browse/AROSLSRE-798),
  AKS & Infrastructure Upgrades)
- [AROSLSRE-1164](https://redhat.atlassian.net/browse/AROSLSRE-1164) — the rejected
  gated-upgrade work: [PR #5627](https://github.com/Azure/ARO-HCP/pull/5627),
  [PR #5628](https://github.com/Azure/ARO-HCP/pull/5628)
- [AROSLSRE-1172](https://redhat.atlassian.net/browse/AROSLSRE-1172) — ITN-2026-00192
  incident tracker and self-heal hardening
- [AROSLSRE-1534](https://redhat.atlassian.net/browse/AROSLSRE-1534) —
  `MgmtClusterNodeSwiftNICCapacityZero` alerting
- ICM 815156644 — AKS RP accelerated-networking regression (fixed, mitigated July 2026)
- [Mitigation: SWIFT v2 delegated-NIC wedged node](mitigate-swiftv2-wedged-node.md)
