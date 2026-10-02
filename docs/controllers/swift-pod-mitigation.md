# SWIFT router Pod mitigation

The `swift-pod-mitigation` controller evicts selected router Pods stuck creating
their initial SWIFT network sandbox. It runs in mgmt-agent under its leader lease.

Its scope is **Pod eviction only**: it does not cordon, drain, delete Nodes, or
call Azure. Node-health detection remains independent.

## When a Pod is eligible

All of these conditions must hold:

- The Pod requests a SWIFT NIC on a Ready, uncordoned SWIFT-v2 Node.
- `PodReadyToStartContainers=False`, with no evidence of any container running
  or restarting.
- Matching sandbox Events identify the exact Pod UID, namespace and Node.
  Overlapping failure intervals span at least **60 seconds**, with activity
  within the last **60 seconds**. Event counts alone are insufficient.
- The live owner chain is Pod -> ReplicaSet -> stable `router` Deployment
  managed by the control-plane operator.
- The namespace, Pod and Deployment match the policy's explicit opt-in selectors.

Eligibility alone does not permit eviction. The workload, replacement and
accounting checks below must also pass. A **hold** means no eviction is submitted.

## Safety checks

### Workload and replacement template

Static Pods, host namespaces/ports, debug containers, custom schedulers,
scheduling gates, resource claims, finalizers and unsupported storage hold
admission. The router's temporary `emptyDir` requires explicit permission, and
the termination grace period must be positive.

The ReplicaSet template must pass the same supported-Pod and `emptyDir` checks.
It must also match the admitted Pod's RuntimeClass, labels, node selector,
affinity, topology spread and placement-relevant tolerations. Differences hold
eviction rather than guessing how admission will mutate a replacement.

For a named RuntimeClass, each admission pass reads its current definition.
Its overhead must match the admitted Pod, and its node selectors and tolerations
are applied to a template copy before comparison. Missing or unreadable classes,
overhead differences and selector conflicts hold eviction. Non-identical
overlapping tolerations that require admission normalization may also hold.
Pods without a RuntimeClass require no RuntimeClass read.

The following admission equivalence rules apply:

- Finite `NoExecute` tolerations are excluded from comparison because they never
  admit a destination in placement.
- Empty and default scheduler names are equivalent.
- Canonical `Exists/NoSchedule` tolerations for SWIFT NIC requests and
  memory pressure on Pods with CPU or memory requests are allowed.
- Binding-added region and zone labels may be absent from the template, but
  must match the source Node. Explicit template topology labels still match
  exactly. Required Pod affinity, anti-affinity or hard spread selectors that
  depend on region/zone Pod labels hold admission, including selectors on
  resident and pending Pods. Node topology keys remain supported. This avoids
  treating source-node labels as replacement labels on another destination.

Templates cannot set the mitigation ownership label, and replacement placement
omits it. Template checks repeat after the ownership claim.

### Availability

Availability counts owned Pods on Ready Nodes that have remained Ready for the
Deployment's `minReadySeconds`, capped by its available replicas. A positive
`minReadySeconds` requires a known Ready transition time and an elapsed readiness
interval. The configured floor applies **even to Pending Pods**, for which
Kubernetes can bypass PDB checks.

### Replacement capacity

Placement accounts for:

- CPU, memory and Pod slots, including pending demand.
- Outstanding SWIFT NIC allocations, even when the Pod has disappeared.
- Taints, required affinity/anti-affinity and hard topology spread.

Terminating Pods are excluded from topology-spread counts, as in the scheduler.
Their resident resource requests and outstanding NIC allocations remain charged.

Resident resources use status-aware requests. Replacement requests use the Pod
spec, including admission-injected resources, and must cover the validated
ReplicaSet template's demand for every resource. This comparison includes init
containers and Pod-level requests, and repeats after the ownership claim.

The search is bounded at **4,096 Pod/node attempts**. Exhaustion, unsupported
constraints or infeasible placement hold eviction. Only the candidate's own
Pod-scoped fault is removed from the simulation; node-wide and other Pod faults
remain exclusions.

## How eviction works

In enforce mode, the controller:

1. Claims the Pod with an identity-tested ownership-label patch.
2. Reads a fresh cluster snapshot and repeats admission checks.
3. Records an attempt in durable accounting.
4. Submits `policy/v1` Eviction with Pod UID/resourceVersion preconditions.

Every write is configuration-fenced. **Denied, timed-out and uncertain attempts
consume allowance.** There is no direct DELETE fallback.

Accepted evictions emit an Event. Eligibility and outcomes are logged with
workload, Pod and Node identities. Reconcile logs carry
`controller_name="swift-pod-mitigation"` for controller-level filtering.
Eligibility logging is configuration-fenced,
so an evaluation cannot report eligibility after its configuration is replaced
or disabled.

### Limits

Kubernetes reads are not atomic, and the capacity check does not reserve
capacity. Pod, template, RuntimeClass and resource changes can race admission. Eviction does
not guarantee different-node placement or successful recovery.

Synchronized controller and Kubernetes clocks are required.

## Configuration

All environments default to disabled. Both
`mgmtAgent.swiftPodMitigation.enabled` and runtime `mode: enforce` are required
for eviction.

When the deployment flag is false, no SWIFT mitigation controller, event handlers
or configuration informer is created or started. Shared Node, Pod and Event
informers still serve the other controllers. For an enabled deployment, runtime
`mode: disabled` stops mitigation without a rollout.

| Runtime mode | Behavior |
| --- | --- |
| `disabled` | No mitigation. |
| `audit` | Reports independent candidate eligibility, with no mutations or simulated budget consumption. |
| `enforce` | Claims, accounts for and requests eviction only when all checks pass. |

`mgmtAgent.swiftPodMitigation.configuration` is a YAML string rendered into
`mgmt-agent-swift-pod-mitigation`, key `config.yaml`, in the mgmt-agent namespace.
It is watched for changes; a Helm rollout restores the configured content.
Missing configuration or key disables the controller. Invalid configuration is
logged and retains the last valid configuration.

### Example audit policy

This opt-in policy requires separately approved enablement:

```yaml
mode: audit
retryInterval: 30s
observationMaxAge: 1m
evictionWindow: 1h
evictionCooldown: 5m
maxEvictionsPerWorkload: 2
maxEvictionsPerNode: 3
workload:
  namespaceSelector:
    matchLabels:
      swift-pod-mitigation.aro-hcp.azure.com/enabled: "true"
  podSelector:
    matchLabels:
      app: private-router
  deploymentSelector:
    matchLabels:
      hypershift.openshift.io/managed-by: control-plane-operator
  allowEmptyDir: true
  minAvailableReplicas: 2
```

## Durable accounting

Both audit and enforce require valid version-1 accounting in the
`swift-pod-mitigation-budget` ConfigMap. **Neither the controller nor Helm renders,
creates or resets it.**

### Initial setup

Initialize accounting separately, **only when no earlier attempts exist**:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: swift-pod-mitigation-budget
  namespace: mgmt-agent
data:
  ledger.json: '{"version":1,"evictionWindow":"1h","evictions":{}}'
```

Do not give this ConfigMap an owner reference. Back it up and preserve its UID,
resourceVersion and contents across upgrades.

### Storage and concurrency

- The ledger holds at most **1,024 attempts** and **256 KiB**, recording
  Deployment, Node and Pod UIDs and timestamps.
- Updates use resourceVersion concurrency. Conflicts prevent eviction.
- Expiry uses the stored window and occurs in memory before the next accepted
  accounting write.
- Missing, corrupt, future-dated or oversized accounting holds eviction without
  resetting the ledger.

### Window changes and recovery

Increasing the window always requires explicit migration, even for an empty
ledger. Decreasing it requires all retained history to expire under the stored
window.

For lost accounting or a migration:

1. Disable enforcement.
2. Reconcile all accepted and uncertain attempts.
3. Restore sufficient history. If history cannot be recovered, confirm all
   controller instances are disabled and wait out the entire intended window.

**Never replace accounting with an empty object just to unblock admission.**

## Permissions

Enablement adds Pod GET/patch, eviction create, namespace GET, ReplicaSet reads,
RuntimeClass GET and a resourceName-scoped accounting GET/update Role. Existing mgmt-agent read
and Event permissions are reused; its existing ConfigMap create/patch permissions
are not removed. No additional Node or Azure mutation permissions are granted.

## Code organization

The controller lives in
[`mgmt-agent/pkg/controller/swiftpod`](../../mgmt-agent/pkg/controller/swiftpod).

| File | Responsibility |
| --- | --- |
| `controller.go` | Pod-keyed scheduling and the configuration fence. |
| `candidate.go` | Candidate evidence, using shared node-health primitives without changing node-health behavior. |
| `workloads.go` | Workload ownership, availability and template admission. |
| `placement.go` | Replacement capacity and scheduling checks. |
| `snapshot.go` | Live cluster and delegated NIC reads, with single-pass Pod and Event indexes by Node for fault evaluation. |
| `budget.go` | Durable attempt accounting. |
| `eviction.go` | Guarded eviction. |

Each admission uses its own fresh snapshot. No generic mitigation registry or
custom accounting API is required.
