# Node Health and Mitigation: Evidence Annex

This annex explains the rationale and validation method for
[node-health](node-health.md) and [node-mitigation](node-mitigation.md).
Configured thresholds are policy choices, not claims of optimality or measured
benefit.

## Detailed evidence

[AROSLSRE-2212: Node-health and node-mitigation evidence](https://redhat.atlassian.net/browse/AROSLSRE-2212)
holds the detailed telemetry, threshold comparisons, incident references and
isolated test observations, with their limitations. Jira authentication and
project access are required. The public design does not reproduce those
measurements or operational identifiers.

## Why separate observation and action?

A Ready condition does not demonstrate that a node can create new sandboxes.
Conversely, a sandbox warning alone does not establish a node-wide failure.
Pod-level success evidence helps distinguish a sustained node-wide fault from
a transient or isolated failure.

Detection can be evaluated independently of disruption. A pod-scoped rescue
policy can act on a sustained individual failure without asserting that healthy
neighbors are affected or that the machine needs deletion.

## Threshold methodology

A replay study should:

- Define the cohort, observation period and completeness limits explicitly.
  Correlate by cluster, Node UID and Pod UID rather than names.
- Establish scheduling, sandbox-false transition and first matching failure
  using Kubernetes timestamps, not ingestion order or cumulative Event counts.
- Require repeated matching activity at the candidate threshold, before initial
  success or deletion. Count each candidate Pod once across fault signatures.
- Compare initial-success timing and candidate outcomes across thresholds.
  First container start at zero restarts can supplement sandbox-ready evidence.
- Treat deletion before success and observation cutoff as unknown/censored
  recovery, not permanent failure. Report missing observations and Event cadence.

The SWIFT rescue threshold filters short-lived failures while bounding the wait
for sustained faults. Replay cannot prove that eviction helps, establish an
optimal threshold, or turn candidates into admitted actions. PDB, placement,
availability and rate checks remain independent. Audit and a bounded canary must
establish acceptable outcomes before wider enablement.

## Eviction outcomes

Join action logs with retained workload and resource observations. Measure
availability over time, accepted evictions, repeated workload/node failures,
destination feasibility and time to readiness; report sample sizes and unknown
outcomes in the access-controlled evidence record.

A Deployment does not identify which Pod replaces a particular evicted Pod.
Rollouts, scaling and concurrent evictions make attribution ambiguous. Report
repeated failures of a workload on a node without claiming a proven replacement
relationship. Absence of another warning is not proof of recovery.

Affinity and topology constraints can make the original node the only feasible
destination. A PDB does not choose destinations. Repeated same-node failure
informs a separate policy decision, never automatic node-level escalation.

## Machine lifecycle and readiness

Kubernetes Node-object removal is not evidence of underlying VM removal.
An accepted AKS operation is not completion, and machine removal is not proof of
replacement capacity. Validate those outcomes separately.

The current Ready-condition timestamp cannot reconstruct lifetime readiness.
Deletion therefore requires uninterrupted observation and immutable machine
identity, not just a candidate detector or an absent annotation.

Disposable agent approvals explicitly accept loss without Eviction/PDB
protection. Cordon and fresh checks reduce ordinary scheduling exposure but do
not eliminate late or bypass bindings. Validation must exercise those limits.

## Router PDB support

HyperShift's `AdaptPodDisruptionBudget` sets
`unhealthyPodEvictionPolicy: AlwaysAllow` on the owner-managed router PDB.
The PDB selects `app: private-router`; this is not a dedicated HostedCluster
configuration option. Supporting changes are
[PR 7721](https://github.com/openshift/hypershift/pull/7721),
[the 4.20 backport](https://github.com/openshift/hypershift/pull/8528) and
[the 4.21 backport](https://github.com/openshift/hypershift/pull/8214).

An older image, including 4.19 without an equivalent backport, cannot be assumed
to supply this policy. Verify the actual per-HCP CPO image and live PDB.
Pending Pods follow Kubernetes' normal PDB exemption; Running-unready behavior
depends on effective policy. Mitigation admission still applies in either case.

## Public references

- [Kubernetes Pod sandbox condition](https://kubernetes.io/docs/concepts/workloads/pods/pod-lifecycle/#pod-network-readiness)
- [Kubernetes PDB behavior](https://kubernetes.io/docs/tasks/run-application/configure-pdb/)
- [Kubernetes eviction implementation](https://github.com/kubernetes/kubernetes/blob/v1.34.0/pkg/registry/core/pod/storage/eviction.go)
- [Kubernetes Node lifecycle](https://kubernetes.io/docs/concepts/architecture/nodes/)
- [AKS machine deletion API](https://learn.microsoft.com/en-us/rest/api/aks/agent-pools/delete-machines)
- [AKS specific-machine deletion semantics](https://learn.microsoft.com/en-us/azure/aks/delete-node-pool#remove-specific-vms-in-an-existing-node-pool)
- [AKS pool target API](https://learn.microsoft.com/en-us/rest/api/aks/agent-pools/get)
- [AKS auto-repair](https://learn.microsoft.com/en-us/azure/aks/node-auto-repair)
