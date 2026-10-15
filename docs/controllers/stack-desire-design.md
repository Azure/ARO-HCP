# Deployment Stack Design

One generic, cluster-rooted `StackDesire` in Cosmos **Resources** separates producer
policy from Azure execution. See the [controller catalog](../cosmos-data-flow.md#stackdesires)
for registration, watches and field ownership.

## Contract

- Producers own the complete spec: target, FPA/SMI authentication, Apply/Delete,
  typed SDK `Stack` (`*armdeploymentstacks.DeploymentStack`) and reconcile interval.
  Apply and Delete use the same spec shape: Apply sends `Spec.Stack`; Delete derives
  SDK options from `Spec.Stack.Properties.ActionOnUnmanage` and
  `Spec.Stack.Properties.BypassStackOutOfSyncError`.
  The target is immutable for the desire's lifetime, and producers must not publish
  competing desires for the same stack.
- The document belongs to its owning cluster's subscription partition. Its Azure
  target independently supports resource-group, subscription and management-group
  scopes. Purging the document stops management; explicit Delete requests teardown.
- Status is one outcome: `ObservedSpecHash`, `ObservedAt`, typed SDK
  `Stack` (`armdeploymentstacks.DeploymentStack`), typed SDK `Error`
  (`armdeploymentstacks.ErrorDetail`) and `Conditions`. Conditions are
  `SuccessfullyApplied`, `SuccessfullyDeleted` and `Degraded`.
- Hash the whole canonicalized spec, not the Azure GET body. Apply associates its
  spec hash with the body `Stack.Properties.CorrelationID`; it is not copied into
  another field. Delete does not track operation correlation.
  There is no historical success, target binding, persisted next-reconcile deadline
  or controller-local outcome cache.

## Reconciliation

`decideStackDesire` takes one immutable desire, current time and a lazy GET callback,
and chooses an explicit status update, Apply, Delete, or `NoOp` action. A zero-valued
decision is invalid; `NoOp` may carry an error or a pending-operation polling delay.
The callback runs at most once, only after local target, scope, authentication,
action and routing prechecks, spec hashing and cooldown checks. Typed SDK input
does not replace runtime validation. GET is a read within this decision, not a
separate decision returned for another reconcile. The outer loop supplies the
direct SDK observation and executes Apply and Delete explicitly,
without a generic execution/outcome layer.
Initial Apply responses and GETs associated with a recorded Apply share the same
provisioning-state interpretation. That interpretation always describes Apply,
even when the desired action has since changed to Delete. Submission errors and
Delete acceptance have separate semantics. Logs explain submission attempts and
observations that change status; unchanged observations and cooldown skips are silent.
A changed observation of a known operation causes one Cosmos replacement and return;
its changefeed event drives the next decision. The only combined mutation is
submitting to Azure and recording its result. Replacement uses the original
document ETag, without a live-read merge or pre-submit checkpoint.
Unchanged GET observations cause no write and do not refresh timestamps.

Eligible reconciles GET before submission. An absent stack submits Apply immediately,
without an observation checkpoint; absence satisfies Delete. Nonterminal operations
wait for completion, including operations with unknown identity. An unknown completed
operation permits direct submission, but never proves Apply success.
For Apply, matching body correlation carries the recorded spec hash forward;
success requires an associated successful stack. Delete records submission
acceptance, not completion. Later GETs showing deletion in progress wait, and a
GET 404 proves success. These GETs need no Delete correlation association or
separate operation-tracking state, even if the accepted submission's association
is lost.
Pending and failed outcomes replace success rather than preserving readiness.
Credential/GET failures and non-response transport/decoding failures return an
error without changing status. Azure rejection responses retain diagnostics and
HTTP classification even when their body is malformed.

Only success or `InvalidIntent` for the requested action cools down, and only with
matching `ObservedSpecHash` and a recorded `ObservedAt`. The deadline is derived
from the document's `ObservedAt + ReconcileInterval` (default 6h). All events,
including self-generated status events and resyncs, use this same decision; no
event filter or extra retry state is needed. Resync drives live GET and repair
after expiry; unchanged observations do not renew the interval. Changed intent
bypasses the old outcome's cooldown. Only pending work explicitly schedules polls,
at a fixed 30s. Earlier events may reconcile immediately; neither pending polls nor
ordinary workqueue error backoff impose a global retry gate. The controller does
not manually interpret `Retry-After`.

Local precheck failures write changed conditions with success False and
`Degraded=False`, reason `PreCheckFailed`, then return without a retry error or
Azure call. Identical precheck results do not rewrite status. Invalid Azure
intent sets the action's success condition False and
`Degraded=False`, reason `InvalidIntent`. Matching current-spec invalid intent uses
the bounded cooldown above, then retries on resync without fast workqueue retry.
A new invalid-intent submission rejection refreshes `ObservedAt` even when its
diagnostics equal the previous rejection. Unchanged GET observations do not write.
Diagnostic-only changes do not restart a completed outcome's cooldown, and condition
transition timestamps change only when the condition's status changes. Other
submission and terminal execution failures degrade and return errors for ordinary
workqueue backoff.

Invalid intent is limited to `InvalidTemplate` and `InvalidRequestContent`, or
the demonstrated `DeploymentStackDeploymentFailed` wrapper with a nonempty set
of direct causes that all have those narrow codes and no nested details. Broad
or nested wrappers are not recursively unwrapped. `InvalidTemplateDeployment`
is always retryable, including when returned with HTTP 400; neither HTTP 400
alone nor message text establishes invalid intent. The
[Azure error fixtures](../../backend/pkg/controllers/stackdesires/testdata/azure-errors/README.md#classification-evidence)
demonstrate missing permission expressed only in an `InvalidTemplateDeployment`
message and recovery after permission was granted without changing intent.

This is at-least-once execution, not an Azure/Cosmos transaction. A crash after
submission can lose the response association. In an A-B-A spec sequence, recorded
success for A can temporarily outlive an unrecorded submission of B. That bounded
stale view is accepted until cooldown expiry and periodic live GET correct it;
ETag checks cannot cancel an Azure request or fence concurrent external writers.

## Azure Client

The controller calls the generated deployment-stacks SDK directly for all three
supported scopes, preserving standard client options, telemetry and the SDK's
built-in API version. There is no custom SDK adapter or custom ARM routing.
FPA/SMI builders and the GET are lazy, so cooldown requires no Azure client or call.
Observation and submission construct clients independently, without shared closure
state. SDK response capture is limited to the initial Apply response: while the
operation is nonterminal, `BeginCreateOrUpdate`'s poller cannot expose that typed
stack through `Result`. Capturing it preserves the body correlation ID for later
Apply observations. Delete uses normal SDK returns without response capture and
records only acceptance; GET proves absence. SDK pollers are passive and safe to
discard; subsequent GETs observe progress without a persisted poller, resume token
or separate correlation field.

## Health

`ClusterDegradedAggregator` watches StackDesire events mapped to the owning cluster
and directly aggregates their current-spec `Degraded` conditions with controller
and cluster-scoped kube-applier desire health. It requires `ObservedSpecHash` to
match the desire's spec hash; stale outcomes, missing conditions and
`Degraded=False` do not contribute degradation. This is generic health reporting,
independent of role-assignment readiness, with no child `Controller` document for
the stack executor. Invalid intent is not service degradation.

## Role Assignments

`IdentityRoleAssignments` publishes one complete, sorted/deduplicated MRG template
with deterministic role names and FPA authentication. Missing prerequisites never
produce partial intent: omission deletes previously stack-managed assignments.
The template keeps the public [ARM schema](https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#).
Deny assignments are independent. All nondeleting clusters use this producer;
obsolete assignments outside the adopted set are not discovered.

`StackReady` requires current desired/spec/observed hash equality,
`SuccessfullyApplied=True` and a typed stack in `Succeeded` state. Pending or
failed repair, absence, changed intent or missing prerequisites revoke readiness.
Cluster creation consumes this boolean; it is not continuous RBAC propagation
proof. Cluster deletion purges intent and relies on MRG cascade, without Delete.

## Validation

Before rollout, validate existing-role adoption, managed omissions, RBAC propagation
and deployed FPA/SMI permissions for each intended tenant/scope. Creation success
alone does not establish teardown or crash-recovery correctness. The
[manual Azure probe and sanitized fixtures](../../backend/pkg/controllers/stackdesires/testdata/azure-errors/README.md)
supply error-classification evidence for offline controller tests; they are not
end-to-end cluster lifecycle validation or guarantees for every provider and scope.
