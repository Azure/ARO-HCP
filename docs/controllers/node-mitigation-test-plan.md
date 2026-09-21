# Node mitigation retest plan

## Execution gate

Live testing requires Rael's explicit command after the review rounds. Approval
to edit code, run local checks or publish a PR is not approval to deploy, build
an image in ACR, inject faults or repeat this plan against a cluster.

Use a dedicated personal DEV management cluster and test pool. INT, STG and PROD
remain disabled. The controller must have no Azure write permissions. Operator
actions that remove an instance or restore a pool target are separate test setup,
never controller behavior.

## Revision and evidence

Run the complete lifecycle on one final reviewed commit and image digest. Record
both before execution, together with the cluster resource ID, configuration,
Helm values, Kubernetes version and UTC start time. A lifecycle spanning multiple
images is not a fresh end-to-end pass on the final revision.

Capture command exit codes, timestamped controller logs, warning Events, episode
and budget YAML, relevant metrics, and read-only Azure identity observations at
each checkpoint. Record Node, Pod, episode and reservation UIDs, resourceVersions,
provider IDs, immutable VM IDs, pool targets and readiness. Do not collect tokens,
kubeconfigs, Secret contents or unrelated workload data.

Keep a UTC timeline with an assertion and evidence file for every result. Record
failures, recovery actions and skipped cases explicitly. Hash the sanitized
evidence bundle and verify any uploaded attachment against its local hash.

## Local gates

| Area | Cases and required result |
| --- | --- |
| Runtime | Race-enabled mgmt-agent tests and affected lint pass. Strict parsing rejects unknown fields, oversized input and invalid policies. |
| Structured configuration | Schema validates all fields and selectors. Audit/enforce require explicit limits and timers, including zero cleanup delay. Invalid types and unknown properties fail. Runtime policy and persisted episode policy round-trip unchanged. |
| Layering | Defaults, environment and region overrides preserve scalar types. Workload and DaemonSet lists replace rather than merge; empty lists clear inherited permissions. Selector expressions and string labels survive unchanged. |
| Transport | Concrete config and real SDP resolve generation produce equivalent runtime policies after substitution and Helm rendering. Include quotes, backslashes, newlines, booleans, integers, empty lists and nested selector expressions. No unresolved placeholders reach the runtime. |
| Public lockout | INT/STG/PROD render without the runtime enable flag or Node mutation permissions, even with an attempted configuration override. |
| Generated content | Materialization, Helm fixtures, schema, YAML formatting and generated-content detection pass. Document any local dependency replacement used for SDP separately from an unmodified downstream pass. |
| API load | Idle discovery performs no live Node/Pod/Event/Namespace/NIC lists. An active reconciliation shares one live snapshot. Informer churn cannot bypass the retry interval. Stale positive discovery cannot authorize action; stale negative discovery delays work until cache convergence. |
| Safety | Live identity, resourceVersion, workload ownership, PDB, placement, reservations and global capacity guards remain covered. NIC observation failure still permits read-only observation of submitted work. |

Local commands from the repository root:

```bash
(cd mgmt-agent && go test -race ./...)
make -C config materialize
make verify-yamlfmt
make -C config detect-change
```

Use the pinned repository lint toolchain. Run only local SDP generation with
`--generate-only --skip-storage-account-uploads`, never a live pipeline target.

## Live prerequisites

After explicit authorization, establish an isolated Azure session for the
required tenant and pass the subscription explicitly on every supported command.
Verify the management-cluster resource ID and Kubernetes context independently.

Begin with mitigation disabled. Confirm both mgmt-agent replicas are Ready,
record leader identity and restart counts, and verify public-environment lockout
from rendered artifacts. Use a pinned final image, a dedicated labeled namespace,
a Deployment/ReplicaSet owner chain and selectors that match only the test
workload. Inventory all pods and DaemonSets on the dedicated source node.

Confirm healthy spare capacity, pool target, zone placement and the intended
small-pool deletion allowance. Record the exact source Node UID and immutable VM
identity. Stop if identity, workload scope, readiness, available capacity or
ownership cannot be verified. Do not remove accounting records to bypass a hold.

## API safeguards

Use server-side dry runs where possible, preserving UID and resourceVersion
preconditions. Keep these results separate from actual controller actions.

| Case | Required result |
| --- | --- |
| Pending pod, zero PDB allowance | Normal eviction admission succeeds without a PDB bypass. Other admission errors remain failures. |
| Running Ready pod, zero allowance | Eviction is denied with a confirmed PDB cause. |
| Running unready pod, default policy | Eviction is denied with a confirmed PDB cause. |
| Running unready pod, AlwaysAllow | Eviction admission succeeds. Record the actual policy enum, not a pointer address. |
| Stale Node/Pod UID or resourceVersion | Mutation is rejected; a new object is not acted on through old intent. |
| Generic 429, authorization or network failure | No unhealthy-deletion fallback and no inferred success. |
| Unhealthy fallback | Requires explicit policy, fresh fault evidence and a unique confirmed blocking PDB; recovered pods remain protected. |

## Fresh lifecycle

1. Enable the deployment flag with runtime mode `audit`. Inject only the scoped
   test evidence. Confirm independent candidate eligibility, zero Kubernetes
   mitigation writes, unchanged episode/budget resourceVersions and no simulated
   reservations. Include two candidates competing for one real slot.
2. Switch to scoped `enforce`. Record admission, accepted policy, baseline and
   reservation identity. Confirm the same reservation accounts for pool, zone
   and cluster limits. A faulted-but-Ready node cannot provide spare capacity.
3. Observe cordon ownership and rescue. Confirm only the selected Pod UID is
   evicted, and recovery requires a genuinely new Ready Pod on another instance.
   Out-of-policy pods, unsupported storage/owners and finalizers must hold
   cleanup. Every nonterminal pod blocks deletion on a NotReady node, including
   allowlisted DaemonSets.
4. Exercise audit and disabled pauses with prepared intent and submitted work.
   Record resourceVersions before and after each pause. No mitigation write may
   cross the config revision fence. Restart or change leader with active state;
   accounting, action identity and accepted permissions must survive.
5. Resume enforce, safely drain and observe the exact original Node DELETE.
   Capture intent and submission timestamps, UID/resourceVersion preconditions,
   absence, retained reservation and original VM presence. Fault recovery alone
   must not undo accepted SWIFT cleanup or automatically uncordon the node.
6. If safe and explicitly scoped to the dedicated instance, induce a fresh Node
   registration while the original VM still exists. Record its new UID and same
   immutable VM ID. Confirm re-cordon under the existing episode/reservation,
   no replay of the original DELETE, and unchanged handling of a different VM.
   Record the registration-to-cordon scheduling window and any pods placed in it.
7. Observe the original VM for the full 30-minute warning threshold. No early
   cleanup-stalled warning is allowed. Record the elapsed time and actual
   warning Event/condition. Read failures must report verification unavailable,
   never instance absence. Pauses/restarts must not reset the warning clock.
8. Pause in audit before any operator cleanup. Reverify the exact dedicated VM,
   Node UID, cordon and pod inventory. If manual AKS instance removal is needed
   to finish the test, record it as an operator action. Record any resulting
   pool-target change and restore only the dedicated test target if authorized.
9. Resume enforce only after verification. Confirm parent-verified Compute
   absence, Ready replacement capacity, completed episode and released
   reservation. Original deletion history must remain charged for its window.
   Recognized instance-level 404 codes may establish absence; parent failure,
   unknown errors and access denial may not.

## Additional scenarios

Exercise pool/zone/cluster contention, a second candidate with an outstanding
reservation, scale-down tightening, conservative scale-up stability, incomplete
NIC observations, unscheduled demand and outstanding NIC allocation debt. Confirm
budget history survives restarts and baseline changes.

For never-ready behavior, use a separately authorized failed-join scenario on a
SWIFT-v2 node. Confirm the 30-minute threshold, no prior Ready history and all
deletion safeguards. Synthetic Node conditions are unit/integration evidence,
not proof of recovery from a real AKS failed join.

Measure idle and active API LIST counts over fixed windows with event churn,
reporting duration, node/pod/event counts, configured retry interval and measured
requests. Do not infer a production throughput guarantee from this test pool.
Keep any HostedCluster recovery test separately scoped and reported.

## Safe finish and report

Set runtime mode `disabled`, disable the Helm flag and verify that the mgmt-agent
service account cannot DELETE Nodes. Confirm both replicas Ready, test workload
Ready, intended pool target restored, and no unrecorded active reservation or
cordoned test node. Do not delete state required to investigate an incomplete
episode. Report any remaining cleanup explicitly.

Log out the isolated Azure session once. Publish the tested commit/digest,
configuration, results and limitations with the sanitized evidence and UTC
timeline. Do not describe skipped cases or earlier-image evidence as a final-head
pass.
