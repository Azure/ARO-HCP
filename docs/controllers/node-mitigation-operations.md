# Node mitigation operations

The `mgmt-agent` controller has two independent mitigators. SWIFT evicts failing
pods without cordoning or draining their nodes. Never-ready asks AKS to delete a
SWIFT-v2 machine whose Node has never become Ready after 30 minutes, with complete
readiness history. It claims a reservation-owned cordon and rechecks the node in a
separate reconciliation. Nonterminal pods block deletion unless explicitly approved
as disposable node-local DaemonSets. There is no general drain.

The controller never calls direct Pod, Node or Compute DELETE, resizes pools,
removes finalizers or bypasses a PDB. A successful AKS response is not proof that
replacement capacity is available.

## Enablement and configuration

`mgmtAgent.nodeMitigation.enabled` defaults to false and gates both controller
enablement and the AKS machine-deletion role assignment. There is no additional
environment-name allowlist or denylist. Current environment configurations,
including INT, STG and PROD, keep this flag disabled. Runtime mode separately
defaults to `disabled`; changing the runtime ConfigMap cannot override a disabled
deployment flag.

The management-infrastructure rollout owns all assignments of the dedicated
machine-deletion role at the exact management-cluster scope, including manual
assignments. It enumerates every page before cleanup. With the flag enabled, it
retains only the current principal's expected assignment; with the flag disabled,
it removes all assignments of that role at that scope. Identity rotation does not
leave a former principal's assignment behind.

The helper rechecks each assignment's subscription, cluster, ID, principal and
role before deletion and verifies the final assignment set. Incomplete discovery,
invalid metadata or failed verification fails the rollout. Reader, other roles,
assignments at other scopes and the shared role definition remain untouched.

The deployment identity has a cluster-scoped cleanup role with only assignment
read/delete actions. Its Azure condition restricts deletion to the dedicated
machine-deletion role across principals. Azure scope inheritance also permits
that role's assignment deletion at descendant scopes; the helper acts only at the
exact cluster scope. It cannot create assignments or delete machines through this
cleanup role. Azure RBAC propagation can delay effective revocation; changing
only the runtime ConfigMap does not revoke Azure permissions.

Mgmt-agent requires an explicit `AZURE_TOKEN_CREDENTIALS` selection at startup.
Helm selects `WorkloadIdentityCredential`. Non-Helm invocations must explicitly
select their intended credential source.

`mgmtAgent.nodeMitigation.configuration` supplies `config.yaml` in the
`mgmt-agent-node-mitigation` ConfigMap. Git and Helm own this YAML string.
The controller reloads it at runtime. Missing configuration disables new writes;
invalid configuration reports an error and retains the last valid configuration.

- `disabled` stops new writes and observes submitted AKS operations.
- `audit` checks candidates against current observations and real reservations.
  It writes no mitigation resources, status, labels or Events.
- `enforce` permits configured mitigators after their safety checks.

Audit logs use `candidateEligible`. Two candidates can both appear eligible for
one free slot when evaluated separately. This is not a prediction that both
would execute.

This isolated DEV example is not a set of production defaults:

```yaml
mode: audit
clusterResourceID: /subscriptions/SUBSCRIPTION/resourceGroups/RESOURCE_GROUP/providers/Microsoft.ContainerService/managedClusters/CLUSTER
mitigators:
- swift
- never-ready
window: 1h
retryInterval: 30s
observationMaxAge: 2m
evictionWindow: 10m
evictionCooldown: 1m
maxEvictionsPerWorkload: 2
maxEvictionsPerNode: 2
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
  minAvailableReplicas: 1
  allowEmptyDir: false
```

Workload availability floors are explicit, including zero when an authorized
workload can tolerate no available replicas. The controller checks Deployment
availability and live owned Pods; it does not require every replica to recover
before rescuing another stalled pod. A Ready pod on the original node is valid.
Rate limits use the Deployment UID and Node UID, so Pod recreation does not reset
them. Denied or unknown eviction attempts also consume rate allowance.

## Workload and capacity checks

Eligible pods have a live ReplicaSet and Deployment owner chain and match all
three policy selectors. Static pods, unsupported owners, host namespaces, debug
containers, host ports, custom schedulers, scheduling gates, resource claims,
node-bound owner templates, finalizers and unsupported storage block eviction.
Removing `emptyDir` data requires explicit permission.
Pods on nodes reserved for deletion do not count toward workload availability.

Placement checks cover requests, init containers, restartable sidecars, overhead,
pod slots, outstanding SWIFT NIC allocations, taints, required affinity and hard
topology spread. The original node remains a possible destination. Eviction
does not prove NIC release or guarantee a different destination.

The deletion allowance is 10% of the stored AKS pool target, rounded down.
Pools of one to nine nodes have one slot per window, one in flight, and require
ready replacement capacity. Scale-down tightens the allowance immediately.
An increase requires a full stable window and Ready added capacity.
Surge and replacement nodes do not reset the budget.

## Readiness evidence and cordon ownership

Node-health owns `node-health.aro-hcp.azure.com/ever-ready`, containing the Node
UID. A direct, unfiltered Node watch preserves intermediate Ready transitions.
In-memory bindings retain the first Node UID for each immutable VM ID after Node
deletion. The same VM cannot become eligible by registering a new Node UID.
A different VM may reuse a Node name or provider path without inheriting history.
Admission verifies Azure's VM ID against the Node's `systemUUID` and requires
Azure `timeCreated` to be strictly after the start of uninterrupted observation,
no later than Node creation and not in the future. Missing or inconsistent time
is a hold. Controller, Azure and Kubernetes UTC clocks must be synchronized;
an observed backward clock step never moves the observation boundary backward.

Initial-list discovery, reconnects and restart leave existing machines with
Unknown history, including VMs with no Node, regardless of age or an absent marker.
A candidate must have been observed from its watch creation through the
resourceVersion read at admission. The 10,000-entry machine-binding limit starts a
new observation window before bindings are discarded, invalidating existing
eligibility. Deleting an unidentified registration also invalidates the window.
This uses the existing Azure read access, without a persistent history ledger.
The readiness observer requires both deployment authorization for mitigation
and enabled node-health observation. SWIFT does not depend on this history.

The cordon patch atomically sets the ownership label, `spec.unschedulable=true`
and `node-mitigation.aro-hcp.azure.com/cordon-reservation`. Its value combines the
reservation key and timestamp. The reservation binds it to the Node UID and
original machine. Existing cordons or ownership are never adopted.

Before an AKS attempt, recovery, lost readiness history, a blocking pod or removal
of never-ready authorization cancels admission. Matching ownership and machine
identity permit a UID/resourceVersion-guarded patch to remove the cordon and
ownership metadata together. Accounting is cancelled after the patch succeeds.
A restart between those writes finishes cancellation without reclaiming the Node.

An operator taking over the cordon removes or changes the reservation annotation
before relying on it for maintenance. A plain `kubectl cordon` on an already
cordoned Node is not a distinguishable ownership claim. Missing or conflicting
ownership holds for operator reconciliation. Removing the annotation does not
cancel an AKS request already attempted or in flight; inspect its reservation
before relying on the machine for maintenance. No attempted, pending or unknown
deletion is automatically uncordoned. Audit/disabled mode makes no release writes;
an unsubmitted cordon remains held until enforcement resumes or an operator
reconciles it. Pausing does not imply automatic uncordoning.

The cordon protects ordinary placement during late recovery, but does not exclude
in-flight bindings or actors bypassing scheduling restrictions. A pod binding
after the last check may be lost with the machine. AKS does not cordon or drain.

## Disposable node-local DaemonSets

`disposableDaemonSets` defaults to an empty list. Each approval supplies
`namespace`, `name`, live `uid`, a `role` of `networking`, `logging` or `monitoring`,
and lowercase SHA256 values for `templateSHA256` and `podSpecSHA256`.
The template digest covers JSON serialization of the live DaemonSet Pod template.
`DisposablePodSpecHash` computes the admitted Pod-spec digest, normalizing only
`nodeName`, the DaemonSet's matching `metadata.name` node-affinity binding and
generated `kube-api-access-*` projected-volume names and their mount references.
Projection contents and mount properties remain part of the digest.

Review the exact role, template, injected containers, storage and shutdown
requirements before approving either digest. A template digest alone does not
exempt injected or stale pods. Template/spec drift or
DaemonSet recreation holds deletion. No built-in DaemonSet is implicitly approved.
Finalizers, preStop hooks, static/mirror pods, terminating pods, debug containers,
resource claims and persistent, remote or CSI storage always block deletion,
including for Succeeded and Failed pods. Terminal pods passing these
protection checks are exempt from DaemonSet approval only when they have no
hostPath or emptyDir volumes. Local data still requires exact disposable
DaemonSet approval, regardless of phase.
Only explicitly reviewed hostPath, emptyDir, ConfigMap, Secret, projected and
downward-API volumes are eligible.
Approved agents remain until machine removal, without Eviction or PDB protection.

## AKS operations and durable accounting

`NodeMitigationBudget` is a namespaced resource in the mgmt-agent namespace.
It stores pool baselines, atomic deletion reservations, rolling deletion history,
eviction-rate records and unresolved AKS operations. It has no Node owner
reference. Ownership labels do not grant permission or record execution progress.
The controller stores no Pod status or replacement relationship.

The AKS executor maps the original VM resource ID to an AKS machine name and
rechecks the immutable VM identity before submission. It uses the SDK's
`deleteMachines` operation and persists its resume token. Polling observes
`Retry-After`, survives restarts and never resubmits the POST.

A configuration fence spans the durable attempt, AKS submission and returned
operation persistence. Configuration updates wait for that section to finish;
an update accepted before it starts prevents the attempt without marking it
unknown. This does not make the Kubernetes and Azure writes transactional.

A lost response or a crash after recording the attempt and before saving the
operation reference leaves an unknown result. Its reservation remains occupied
and the controller reports the need for operator reconciliation. Neither a
timeout nor a missing Node authorizes a retry. Completed operations release active
allowance only when required healthy capacity is observed. Deletion history stays
for its full window.

Current configuration applies to work in progress. Pausing cannot cancel an AKS
request already accepted. Accounting-window changes require expired history or
an explicit migration. An unsupported ledger version blocks operation rather
than resetting its history. Owned Nodes or Pods without initialized accounting
also block operation. Labels cannot recover history after those resources
disappear, so the ledger must be preserved. Never delete it to bypass these checks.

Mgmt-agent uses Reader for resource and operation observations. Authorized
enablement declares the specific AKS `agentPools/deleteMachines/action` permission
at the management-cluster scope. Fleet receives no deletion privilege. Deployment
and live execution require separate authorization.

## Observability and validation

Structured eviction logs identify the cluster, Node and Pod UIDs, workload owner,
detector, attempt, timestamp and result. Metrics report bounded action outcomes,
AKS operation observations, deletion allowance and pool target/Ready capacity.
Workload identities remain in logs rather than metric labels. Backend analysis
distinguishes repeated workload/node failures from confirmed replacement returns.
Detector silence alone is not proof of recovery.

```bash
cd mgmt-agent
go test -race ./pkg/controller/nodemitigation ./pkg/controller/nodehealth/...
make update-codegen
make update-mitigation-crds
```

The CRD target generates the budget schema. CapacityReport remains independently
maintained. Config and Helm fixtures are rendered with
`make -C config materialize` from the repository root.
