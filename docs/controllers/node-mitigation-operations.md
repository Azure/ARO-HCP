# Node mitigation operations

The `mgmt-agent` node-mitigation controller evicts SWIFT pods with repeated
initial-sandbox failures. It does not cordon or drain nodes, call direct Pod or
Node DELETE, delete Azure machines, resize pools, remove finalizers or bypass
eviction admission. It has no Azure client or additional Azure permissions.

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
topology spread. The original node remains a possible destination. Eviction
does not prove NIC release or guarantee a different destination.

Admission rechecks the live Pod identity, sandbox evidence and workload after
claiming its ownership label. Eviction carries UID/resourceVersion preconditions
and preserves graceful termination. Kubernetes may bypass PDB checks for Pending
pods; Running-unready pods depend on the PDB's unhealthy-pod eviction policy.
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

Accounting-window changes require expired history or an explicit migration.
An unsupported ledger version blocks operation rather than resetting history.
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
