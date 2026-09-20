# Node mitigation operations

The `mgmt-agent` node-mitigation controller manages Kubernetes actions for SWIFT
sandbox stalls and nodes that never become Ready. It reads Azure pool and instance
state using the existing Reader role. It does not scale pools, delete Azure
instances, force-delete pods, remove finalizers or automatically uncordon nodes.
Never-ready detection is limited to SWIFT-v2 nodes, matching node-health.

The deployment flag and mutation permissions can be enabled only in DEV
environments. Set `mgmtAgent.nodeMitigation.enabled: true` with a controller image
that supports it. The deployment flag defaults to false, and the runtime mode
defaults to `disabled`. INT, STG and PROD cannot enable mitigation through the
runtime ConfigMap.

## Configuration

`mgmtAgent.nodeMitigation.configuration` supplies `config.yaml` in the
`mgmt-agent-node-mitigation` ConfigMap, in the mgmt-agent namespace. Git and Helm
own the durable configuration. A live ConfigMap edit is temporary and is replaced
by the next deployment.

- `disabled` preserves existing state and observes submitted work.
- `audit` evaluates candidates independently against actual reservations. It
  performs no mitigation writes, including records, status and Events.
- `enforce` permits explicitly enabled phases after all safety checks.

Audit logs use `candidateEligible`. Two candidates can each be eligible for one
available slot; this is not a prediction that both can execute. Audit does not
reserve hypothetical capacity or simulate successful actions.

The following values are an isolated DEV test example, not production defaults.
Use the actual management-cluster resource ID and selectors for a dedicated test
workload. The deployment itself must also have mitigation enabled.

```yaml
mode: audit
clusterResourceID: /subscriptions/SUBSCRIPTION/resourceGroups/RESOURCE_GROUP/providers/Microsoft.ContainerService/managedClusters/CLUSTER
mitigators:
- swift
- never-ready
rescue: true
drain: false
deleteNode: false
window: 10m
retryInterval: 30s
observationMaxAge: 2m
cleanupDelay: 0s
maxUnavailableCluster: 1
maxUnavailablePool: 1
maxUnavailableZone: 1
minHealthyPool: 1
minHealthyZone: 1
workloads:
- namespaceSelector:
    matchLabels:
      node-mitigation-test: "true"
  podSelector:
    matchLabels:
      app: mitigation-test
  deploymentSelector:
    matchLabels:
      app: mitigation-test
  allowUnhealthyDeletion: false
  allowEmptyDir: false
daemonSets: []
```

All timings and disruption limits must be explicit for audit and enforcement.
`cleanupDelay: 0s` is valid but must not be omitted. Missing configuration disables
actions. Invalid configuration is reported and leaves the last valid configuration
in place. Phase switches take effect before the next API submission.

The rolling window is part of persisted accounting. Changing it while a ledger
exists holds admission until an operator reviews the accounting migration. Do not
delete the ledger to make a different window take effect.

## Workloads and capacity

The supported owner chain is a live ReplicaSet owned by a live Deployment.
Namespace, pod and Deployment selectors must all match an explicit policy.
An accepted episode cannot gain broader workload or DaemonSet permissions through
a configuration change.

Static pods, unsupported owners, host namespaces, debug containers, host ports,
custom schedulers, scheduling gates, resource claims, node-bound owner templates,
finalizers and unsupported storage hold cleanup. Removing `emptyDir` data requires
explicit permission. DaemonSets may remain at Node
deletion only through an explicit `namespace/name` allowlist and a live owner
identity check. Any nonterminal pod on a NotReady node, including an allowlisted
DaemonSet, blocks Node deletion.

Placement checks include requests, init containers, restartable sidecars,
overhead, pod slots, SWIFT NIC allocations, taints, required affinity and hard
topology spread. Outstanding NIC allocations remain charged after their pods
disappear. External unscheduled demand also consumes headroom.
Nodes with current mitigation fault evidence cannot supply healthy headroom or
replacement placement, even when their Kubernetes Ready condition is true.

The deletion allowance is 10% of the persisted AKS pool target, rounded down.
Pools of one to nine nodes have one slot per window, one in flight, and require
ready replacement capacity. Scaling down tightens the allowance immediately.
An increase requires a full stable window and ready added capacity. More Node
objects alone never increase the allowance.

Eviction is attempted before any unhealthy-pod fallback. A generic HTTP 429 does
not authorize a bypass. The fallback requires explicit workload permission,
current fault evidence and a confirmed, unique blocking PDB. Grace and identity
preconditions remain in place. A recovered pod is not deleted through a stale
fallback.

Recovery requires a genuinely new Ready pod on a schedulable node outside the
original instance. The recovery target uses live workload availability rather
than assuming the Deployment status is current.

## State, pauses and observation

`MitigationEpisode` and `NodeMitigationBudget` are namespaced resources in the
mgmt-agent namespace. The shared ledger atomically reserves pool, zone and cluster
allowance. Episodes retain the original Node UID, provider ID, immutable instance
identity, accepted policy and action intent.

An accepted SWIFT cleanup plan remains active when fault evidence disappears.
A never-ready node that recovers before deletion can release its reservation only
when no request was submitted, or a new resourceVersion fences the old request.
Unknown outcomes never expire into free allowance. Missing accounting holds
further action instead of constructing empty replacement history.

`NodeObjectDeleted`, `InstanceGone` and `CapacityRestored` are separate conditions.
The original instance remaining for 30 minutes sets `InstanceCleanupStalled` and
emits a warning Event in enforce mode. Failed Azure reads set
`InstanceVerificationUnavailable`; they are not evidence of deletion.
If observation was paused before absence could be persisted, the saved deletion
attempt supplies a separate warning reason. A pause or restart does not erase
that action identity or silently restart its warning period.

If the original instance re-registers, the controller verifies and re-cordons its
new Node UID under the existing reservation. Pods placed before re-cordon pass
the same workload and eviction checks. The original Node DELETE is never replayed
against that registration, and a different replacement instance is left alone.

The `mgmt_agent_node_mitigation_` metrics report action outcomes, active held and
warning conditions, pool allowance, recovery observations and outstanding
recovery wait. Object identities and detailed hold reasons are in episode state
and structured logs. Existing capacity and customer-impact alerts determine
paging; an instance warning does not grant Azure write permission.

## Development checks

```bash
cd mgmt-agent
go test -race ./pkg/controller/nodemitigation ./pkg/controller/nodehealth/...
make update-codegen
make update-mitigation-crds
```

The CRD target generates only the two mitigation schemas into the chart. The
existing cluster-scoped CapacityReport CRD remains independently maintained.
Render config and Helm fixtures with `make -C config materialize` from the
repository root.
