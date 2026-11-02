// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package fleetapi

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

// Condition types carried by a NamespaceDeletion.
//
// There is deliberately no phase. A phase forces a single linear state onto a
// controller that keeps reconciling, and "Failed" as a phase is wrong because a
// controller always continues trying. Progress, retryable trouble and a latched
// safety stop are three independent axes, so they are three conditions.
const (
	// ConditionTypeNamespaceDeletionComplete reports whether this request's
	// target namespace is absent. It is the only success signal.
	ConditionTypeNamespaceDeletionComplete = "Complete"

	// ConditionTypeNamespaceDeletionDegraded reports a retryable error that is
	// preventing progress: Cosmos, observation, or Kubernetes. Degraded gates no
	// mutation and is retried indefinitely with the workqueue's rate-limited
	// backoff. There is no attempt cap, because a cap would strand a request in
	// a dead state after a transient blip and force the operator to resubmit.
	ConditionTypeNamespaceDeletionDegraded = "Degraded"

	// ConditionTypeNamespaceDeletionStopped reports a latched safety stop. The
	// controller still reconciles status and retires desires, but issues no
	// further mutation for this attempt.
	ConditionTypeNamespaceDeletionStopped = "Stopped"
)

// Reasons for Complete=False. These describe work in progress, not failure.
const (
	// ReasonObservationPending means no valid observation of the target has
	// arrived yet. Note that an unready or errored ReadDesire is pending, not
	// absence: only Successful=True with nil KubeContent proves absence.
	ReasonObservationPending = "ObservationPending"

	// ReasonDwellNotElapsed means the namespace has not been Terminating long
	// enough to conclude it is wedged rather than merely slow. Retryable.
	ReasonDwellNotElapsed = "DwellNotElapsed"

	// ReasonDeletingHostedCluster means the opt-in HostedCluster delete has been
	// issued and the request is waiting for the HostedCluster to be observed
	// absent before it will finalize the namespace.
	ReasonDeletingHostedCluster = "DeletingHostedCluster"

	// ReasonFinalizingNamespace means the finalize desire has been issued with
	// the pinned UID.
	ReasonFinalizingNamespace = "FinalizingNamespace"

	// ReasonFinalizePreconditionFailed means kube-applier re-checked the
	// preconditions immediately before acting and one did not hold. Non-terminal:
	// the target is re-observed and retried.
	ReasonFinalizePreconditionFailed = "FinalizePreconditionFailed"

	// ReasonBlockedByIdentityMismatch pairs with a Stopped identity mismatch.
	ReasonBlockedByIdentityMismatch = "BlockedByIdentityMismatch"
)

// Reasons for Complete=True.
const (
	// ReasonNamespaceAbsent means the namespace was already gone when observed;
	// no action was needed.
	ReasonNamespaceAbsent = "NamespaceAbsent"

	// ReasonNamespaceFinalized means the namespace is observed absent after this
	// request cleared its finalizer.
	ReasonNamespaceFinalized = "NamespaceFinalized"
)

// Reasons for Stopped=True. Every one of these is latched: the attempt cannot
// resume without deliberate operator action.
const (
	// ReasonNamespaceNotTerminating means the namespace is Active. This API
	// finishes deletions; it does not start them. No request field overrides
	// this.
	ReasonNamespaceNotTerminating = "NamespaceNotTerminating"

	// ReasonNamespaceStateInconsistent means phase and deletionTimestamp
	// contradict each other; take no action until the state reads consistently.
	ReasonNamespaceStateInconsistent = "NamespaceStateInconsistent"

	// ReasonHostedClusterNotDeleting means a HostedCluster in the related
	// namespace has no deletionTimestamp and the request did not opt in to
	// deleting it, so its Azure infrastructure is provably still live.
	ReasonHostedClusterNotDeleting = "HostedClusterNotDeleting"

	// ReasonHostedClusterLive means the observed HostedCluster looks like a
	// working cluster. This is the gate that keeps the DeleteHostedCluster
	// opt-in from being able to destroy a live cluster, and it cannot be
	// overridden by any request field.
	ReasonHostedClusterLive = "HostedClusterLive"

	// ReasonHostedClusterRemovalTimedOut means the opt-in delete was issued but
	// HyperShift did not finish within the configured timeout. The namespace is
	// deliberately NOT finalized: Azure was live when the attempt began, so
	// finalizing now would strand it.
	ReasonHostedClusterRemovalTimedOut = "HostedClusterRemovalTimedOut"

	// ReasonHostedClusterIdentityMismatch means the observed HostedCluster has a
	// different name than the request supplied.
	ReasonHostedClusterIdentityMismatch = "HostedClusterIdentityMismatch"

	// ReasonNamespaceIdentityMismatch means namespace labels contradict the
	// supplied identity. Missing legacy labels alone do not cause this.
	ReasonNamespaceIdentityMismatch = "NamespaceIdentityMismatch"

	// ReasonNamespaceUIDChanged means the namespace was replaced after binding;
	// this attempt is void.
	ReasonNamespaceUIDChanged = "NamespaceUIDChanged"

	// ReasonClusterRecordAppeared means a Cosmos owner appeared after
	// acceptance. Use the normal cluster deletion flow.
	ReasonClusterRecordAppeared = "ClusterRecordAppeared"

	// ReasonActiveDesirePresent means a foreign ApplyDesire still actively
	// maintains the target.
	ReasonActiveDesirePresent = "ActiveDesirePresent"
)

// Reasons for Degraded=True.
const (
	// ReasonOwnershipCheckFailed means the Cosmos ownership query failed. Never
	// treated as absence.
	ReasonOwnershipCheckFailed = "OwnershipCheckFailed"

	// ReasonObservationFailed means a Kubernetes observation failed.
	ReasonObservationFailed = "ObservationFailed"
)

// NamespaceDeletion expresses the desire to retire one wedged Terminating
// namespace on one management cluster.
//
// It exists because the namespace-only orphans it targets have no live Cosmos
// cluster document, so no existing controller has anything to trigger on. The
// entry point is therefore an explicit operator action through the admin API.
//
// The capability finishes deletions; it does not start them. A namespace that
// is not already Terminating is refused outright, and the sole namespace
// mutation is clearing the namespace's own "kubernetes" finalizer — there is no
// namespace DELETE anywhere in this flow, because DELETE on a Terminating
// namespace succeeds and changes nothing.
//
// The document is nested under a ManagementCluster, which is how the controller
// reaches a management cluster for a cluster that has no cluster document:
// kube-applier desires partition by management cluster, not by HCP cluster.
//
// See docs/ops/stuck-cluster-cleanup-controller-proposal.md.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type NamespaceDeletion struct {
	// CosmosMetadata.ResourceID is nested under the management cluster. The
	// resource name is the target namespace name itself: a Kubernetes namespace
	// name is at most 63 characters of [a-z0-9-], which is a valid single ARM
	// path segment and a valid Cosmos resource name, so a derived hash would buy
	// nothing and cost readability.
	// For example, for stamp "eastus" and namespace
	// "ocm-arohcpprod-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ResourceID is
	// /providers/Microsoft.RedHatOpenShift/stamps/eastus/managementClusters/default/namespaceDeletions/ocm-arohcpprod-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
	// and PartitionKey is "eastus".
	// PartitionKey holds the lowercased stamp identifier of the parent Stamp.
	coreapi.CosmosMetadata `json:"cosmosMetadata"`

	Spec NamespaceDeletionSpec `json:"spec"`

	Status NamespaceDeletionStatus `json:"status"`
}

// NamespaceDeletionSpec is the operator's request. Every field is supplied at
// creation and immutable afterwards, except NamespaceUID, which the controller
// pins on first observation and which is immutable once set.
type NamespaceDeletionSpec struct {
	// NamespaceName is the exact name of the Terminating namespace to retire.
	// A prefix is never accepted. It duplicates the resource name and is
	// validated to equal it.
	// Written by: Admin API POST NamespaceDeletion
	NamespaceName string `json:"namespaceName"`

	// HostedClusterName is the exact metadata.name of the HostedCluster this
	// namespace belongs to. It is required, and it does two jobs: it derives and
	// validates the control-plane namespace name, and it confirms that the
	// HostedCluster observed in the related namespace is the one the operator
	// believes is there. A mistyped name is the main way a request lands on the
	// wrong namespace.
	//
	// It does not by itself drive a deletion. Only DeleteHostedCluster does
	// that.
	// Written by: Admin API POST NamespaceDeletion
	HostedClusterName string `json:"hostedClusterName"`

	// DeleteHostedCluster authorizes this request to delete the HostedCluster
	// when the HostedCluster has no deletionTimestamp of its own.
	//
	// It exists for exactly that one observation. When the HostedCluster is
	// already deleting, issuing a delete is a no-op and this field changes
	// nothing, so it is not a general "also delete things" flag and should not
	// be set out of habit.
	//
	// Default false. Immutable after creation, including across a restart, so an
	// authorization to originate a deletion can never be added to an attempt
	// that was accepted without one. It never overrides the liveness gate: a
	// HostedCluster that looks alive stops the request whatever this says.
	//
	// +optional
	// Written by: Admin API POST NamespaceDeletion
	DeleteHostedCluster bool `json:"deleteHostedCluster,omitempty"`

	// NamespaceUID pins the identity of the namespace this request refers to.
	// It is empty at creation, because the UID can only be learned by reading
	// the namespace, and is set once by the controller on first observation.
	// Replacement of the pinned namespace stops the attempt; the controller
	// never repins automatically.
	//
	// This is a controller-observed safety value, not an operator-supplied
	// identity field, and the validator forbids it on create.
	//
	// +optional
	// Written by: NamespaceDeletion
	NamespaceUID string `json:"namespaceUID,omitempty"`

	// RequestReason is the operator's justification, recorded for audit. The
	// caller's identity is captured separately by the admin API's audit
	// middleware and is not duplicated here.
	// Written by: Admin API POST NamespaceDeletion
	RequestReason string `json:"requestReason"`
}

// NamespaceDeletionStatus is everything the controller has learned.
//
// There is deliberately no Message field and no preflight struct: a message
// belongs on a condition, and the controller logic only needs "do I act or
// not" while a human needs one good condition string.
type NamespaceDeletionStatus struct {
	// Conditions carries Complete, Degraded and Stopped.
	//
	// +optional
	// Written by: NamespaceDeletion
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// CompletedAt is when the request first settled, by completing or stopping.
	//
	// It is the retention clock. Nothing else garbage-collects these documents:
	// they are stamp-rooted, so DeleteOrphanedCosmosResources cannot reach them
	// by its subscription-prefix walk. Without an explicit expiry every
	// invocation would persist for the lifetime of the container.
	//
	// +optional
	// Written by: NamespaceDeletion
	CompletedAt *metav1.Time `json:"completedAt,omitempty"`

	// Audit is the durable evidence captured before any mutation.
	//
	// +optional
	// Written by: NamespaceDeletion
	Audit *NamespaceDeletionAudit `json:"audit,omitempty"`
}

// NamespaceDeletionAudit is what an operator will have if the Azure assumption
// turns out to be wrong for a given cluster.
//
// This is a requirement of the design rather than telemetry: the capability
// accepts that clearing a namespace finalizer can abandon an in-flight
// HyperShift deprovision, and compensates with this record instead of an Azure
// API check. It is written before the mutation, not after.
type NamespaceDeletionAudit struct {
	// RecordedAt is when this evidence was captured, immediately before the
	// first mutation the request issued.
	//
	// +optional
	// Written by: NamespaceDeletion
	RecordedAt *metav1.Time `json:"recordedAt,omitempty"`

	// NamespaceUID is the pinned UID of the namespace at the time of the
	// mutation.
	//
	// +optional
	// Written by: NamespaceDeletion
	NamespaceUID string `json:"namespaceUID,omitempty"`

	// NamespaceDeletionTimestamp is when the namespace deletion was originally
	// requested by whoever requested it. The age derived from it is what makes
	// "wedged" a defensible conclusion.
	//
	// +optional
	// Written by: NamespaceDeletion
	NamespaceDeletionTimestamp *metav1.Time `json:"namespaceDeletionTimestamp,omitempty"`

	// NamespaceConditions is the namespace's own status.conditions carried
	// verbatim — NamespaceDeletionDiscoveryFailure, NamespaceContentRemaining
	// and friends. Recorded whole rather than summarized, because which one was
	// set is the evidence.
	//
	// +optional
	// Written by: NamespaceDeletion
	NamespaceConditions []metav1.Condition `json:"namespaceConditions,omitempty"`

	// FinalizersRemoved lists the namespace spec.finalizer strings this request
	// cleared. In practice the single "kubernetes" entry; recorded explicitly so
	// the record states what was done rather than implying it.
	//
	// +optional
	// Written by: NamespaceDeletion
	FinalizersRemoved []string `json:"finalizersRemoved,omitempty"`

	// HostedCluster is the observed state of the HostedCluster at the time of
	// the mutation, when one was present.
	//
	// +optional
	// Written by: NamespaceDeletion
	HostedCluster *NamespaceDeletionAuditHostedCluster `json:"hostedCluster,omitempty"`
}

// NamespaceDeletionAuditHostedCluster is the observed HostedCluster evidence.
type NamespaceDeletionAuditHostedCluster struct {
	// Name is the observed HostedCluster metadata.name.
	//
	// +optional
	// Written by: NamespaceDeletion
	Name string `json:"name,omitempty"`

	// UID is the observed HostedCluster metadata.uid.
	//
	// +optional
	// Written by: NamespaceDeletion
	UID string `json:"uid,omitempty"`

	// DeletionTimestamp is the HostedCluster's own deletionTimestamp. Absent
	// means HyperShift's finalizer had not started, which on the default path is
	// a refusal and on the opt-in path is what the request acted on.
	//
	// +optional
	// Written by: NamespaceDeletion
	DeletionTimestamp *metav1.Time `json:"deletionTimestamp,omitempty"`

	// Finalizers is the HostedCluster's remaining metadata.finalizers, which is
	// the direct evidence of an abandoned HyperShift deprovision.
	//
	// +optional
	// Written by: NamespaceDeletion
	Finalizers []string `json:"finalizers,omitempty"`

	// DeletedByThisRequest records that this request issued the HostedCluster
	// delete under the DeleteHostedCluster opt-in. It distinguishes a namespace
	// finalized after a real deprovisioning attempt from one finalized without
	// one — two outcomes with materially different Azure-leak likelihoods.
	//
	// +optional
	// Written by: NamespaceDeletion
	DeletedByThisRequest bool `json:"deletedByThisRequest,omitempty"`
}
