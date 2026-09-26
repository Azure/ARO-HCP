# Node mitigation operations

The `mgmt-agent` node-mitigation controller evicts SWIFT pods with repeated
initial-sandbox failures. It does not cordon or drain nodes, call direct Pod or
Node DELETE, delete Azure machines, resize pools, remove finalizers or bypass
eviction admission. It has no Azure client or additional Azure permissions.

## Detectors and mitigators

Detection and action have separate named registries. The
[`detector registry`](../../mgmt-agent/pkg/controller/nodehealth/detectors/registry.go)
contains node-wide `swift-vf-teardown`, `cni-plugin-not-initialized` and
`never-ready`, plus pod-scoped `swift-pod-sandbox-stalled`.
Node-health uses only node-wide results for health labels.

The [`mitigator registry`](../../mgmt-agent/pkg/controller/nodemitigation/registry.go)
contains `swift`, implemented in its own `swift.go`. It consumes only
`swift-pod-sandbox-stalled`, not the node-wide wedge signals. Collection and the
fresh pre-eviction check use the same registered detector. Registering a detector
does not enable mitigation; configuration and safety admission remain separate.

## Enablement and configuration

`mgmtAgent.nodeMitigation.enabled` defaults to false and gates controller
authorization and Kubernetes eviction/accounting permissions. There is no
environment-name allowlist or denylist. Current environment configurations,
including INT, STG and PROD, keep this flag disabled. Runtime mode separately
defaults to `disabled`; changing the runtime ConfigMap cannot override a disabled
deployment flag.

Mgmt-agent requires an explicit `AZURE_TOKEN_CREDENTIALS` selection at startup
for its existing Azure-backed controllers. Helm selects
`WorkloadIdentityCredential`. Non-Helm invocations must explicitly select their
intended credential source.

`mgmtAgent.nodeMitigation.configuration` supplies `config.yaml` in the
`mgmt-agent-node-mitigation` ConfigMap. Git and Helm own this YAML string.
The controller reloads it at runtime. Missing configuration disables writes;
invalid configuration reports an error and retains the last valid configuration.

- `disabled` makes no mitigation writes.
- `audit` checks candidates against current observations and actual eviction
  accounting. It writes no mitigation resources, status, labels or Events.
- `enforce` permits the explicitly selected `swift` mitigator after safety checks.

Audit logs use `candidateEligible`. Candidates are evaluated separately, not as
a prediction of jointly admissible actions. Never-ready mitigation and
machine-deletion configuration are not supported.

This isolated DEV example is not a set of production defaults:

```yaml
mode: audit
clusterResourceID: /subscriptions/SUBSCRIPTION/resourceGroups/RESOURCE_GROUP/providers/Microsoft.ContainerService/managedClusters/CLUSTER
mitigators:
- swift
retryInterval: 30s
observationMaxAge: 2m
evictionWindow: 10m
evictionCooldown: 1m
maxEvictionsPerWorkload: 2
maxEvictionsPerNode: 2
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
  minAvailableReplicas: 1
  allowEmptyDir: false
```

## Workload and placement checks

Eligible pods have a live ReplicaSet and Deployment owner chain and match all
three policy selectors. Static pods, unsupported owners, host namespaces, debug
containers, host ports, custom schedulers, scheduling gates, resource claims,
node-bound owner templates, finalizers and unsupported storage block eviction.
Removing `emptyDir` data requires explicit permission.

Workload availability floors are explicit, including zero when an authorized
workload can tolerate no available replicas. The controller checks Deployment
availability and live owned Pods; it does not require every replica to recover
before rescuing another stalled pod. A Ready pod on the original node is valid.

Placement checks cover requests, init containers, restartable sidecars, overhead,
pod slots, outstanding SWIFT NIC allocations, taints, required affinity and hard
topology spread. The original node remains a possible destination only when
excluding the candidate's own `swift-pod-sandbox-stalled` detection leaves no
node-wide or unrelated Pod fault. Missing scoped evidence retains the node's
fault verdict. Eviction
does not prove NIC release or guarantee a different destination.

Placement uses deterministic backtracking to reconsider simulated assignments
for the replacement and pending Pods. Each check allows at most 4,096 Pod/node
attempts. Reaching the limit reports unknown feasibility and holds eviction.
The search preserves the ordered Pod set and supported constraints; it is not
scheduler-equivalent and does not promise to find every schedulable arrangement.

The SWIFT plan accepts only `swift-pod-sandbox-stalled` detections with Pod scope
and a nonempty Pod UID list. After claiming ownership, admission refreshes the
cluster snapshot and rechecks the live Pod identity, sandbox evidence, workload,
ownership and placement before accounting and eviction. Placement uses the
admitted Pod's current resource requests, including its resident capacity charge.
Failed or stale reads, infeasible placement and lost ownership block the attempt.
These checks do not reserve scheduler capacity or make cross-resource reads atomic.
Eviction carries UID/resourceVersion
preconditions and preserves graceful termination. Kubernetes may bypass PDB
checks for Pending pods; Running-unready pods depend on the PDB's unhealthy-pod
eviction policy.
An eviction denial is reported without a direct DELETE fallback.

## Durable accounting and pause behavior

`NodeMitigationBudget` is a namespaced resource in the mgmt-agent namespace.
It stores versioned eviction-attempt records and their window. It has no Node
owner reference. The controller stores no Pod status or replacement relationship.

Rate limits use Deployment UID and Node UID, so Pod recreation does not reset
them. Denied or unknown eviction attempts also consume allowance. Durable
accounting precedes eviction submission. A configuration fence checks the
accepted revision and enforce mode before every write, including labels,
accounting, eviction and Events. A pause after accounting but before submission
can leave a conservatively counted attempt without an eviction.

Increasing an established eviction window requires explicit accounting migration,
even with an empty ledger: history pruned under a shorter window cannot establish
the longer rate limit. Audit and enforce report the mismatch before pruning or
admission and leave the ledger unchanged. There is no automatic warm-up or reset.
Decreasing the window requires all records to expire under the stored window,
or explicit migration. An empty ledger without an established window can initialize.
For a migration, pause enforcement and establish complete history for the target
window, or verify that no eviction attempts occurred for a full target window,
before explicitly reconciling the ledger and its window. An empty ledger alone
is not that evidence.
An unsupported ledger version blocks operation rather than resetting history.
Nonempty eviction accounting requires a positive stored window before any pruning.
Every record requires a nonempty attempt ID, workload/Node/Pod UIDs and a nonzero
attempt timestamp no later than the controller's current time. Node owner
references and malformed records block operation in every mode, before pruning
or admission, without changing the ledger.
Owned Pods without initialized accounting also block operation. Labels cannot
recover history after those resources disappear, so the ledger must be preserved.
Never delete it to bypass these checks.

## Observability and validation

Structured eviction logs identify cluster, Node and Pod UIDs, workload owner,
detector, attempt, timestamp and result. Metrics report bounded action outcomes
and evaluated workload availability. Workload identities remain in logs rather
than metric labels. Detector silence alone is not proof of recovery.

Tests cover strict configuration, deployment and runtime gates, current evidence,
workload floors, placement, eviction denial, identity guards, restart-safe rates,
lost accounting and recovery during admission. Deployment and live eviction
require separate authorization.
