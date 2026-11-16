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
	// MaxNamespaceDeletionRetries bounds audit history and the desire names
	// that can be retired if a request document disappears unexpectedly.
	MaxNamespaceDeletionRetries int64 = 10
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

// NamespaceDeletion expresses the desire to retire one wedged Terminating
// namespace on one management cluster.
//
// It exists because the namespace-only orphans it targets have no live Cosmos
// cluster document, so no existing controller has anything to trigger on. The
// entry point is therefore an explicit operator action through the admin API.
//
// The capability finishes deletions; it does not start them. A namespace that
// is not already Terminating is refused outright, and the sole namespace
// mutation after cleanup is clearing the namespace's own "kubernetes"
// finalizer. An already deleting HostedCluster may also have its known stuck
// finalizer chain cleared after live ownership and Azure checks. There is no
// namespace or HostedCluster DELETE in this flow.
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

// NamespaceDeletionSpec holds the immutable target and the latest retry request.
type NamespaceDeletionSpec struct {
	// ManagementClusterName is the management cluster supplied in the admin API
	// route. Keeping it in the request makes the target explicit to the
	// controller rather than inferring it from the resource ID.
	// Written by: Admin API POST NamespaceDeletion
	ManagementClusterName string `json:"managementClusterName"`

	// HostedClusterNamespaceName is the exact name of the Terminating namespace to retire.
	// A prefix is never accepted. It duplicates the resource name and is
	// validated to equal it.
	// Written by: Admin API POST NamespaceDeletion
	HostedClusterNamespaceName string `json:"hostedClusterNamespaceName"`

	// HostedClusterName is the exact metadata.name of the HostedCluster this
	// namespace belongs to. It is required, and it does two jobs: it derives the
	// control-plane namespace name, and it confirms that the
	// HostedCluster observed in the related namespace is the one the operator
	// believes is there. A mistyped name is the main way a request lands on the
	// wrong namespace.
	//
	// The controller uses the observed HostedCluster identity before issuing
	// any delete or finalization desire.
	// Written by: Admin API POST NamespaceDeletion
	HostedClusterName string `json:"hostedClusterName"`

	// RequestReason is the operator's justification, recorded for audit. The
	// caller's identity is captured separately by the admin API's audit
	// middleware and is not duplicated here.
	// Written by: Admin API POST NamespaceDeletion
	RequestReason string `json:"requestReason"`

	// RetryGeneration and RetryReason are advanced when the Admin POST receives
	// a matching, settled Stopped request. The backend starts the next attempt
	// after retiring old desires.
	// Written by: Admin API POST NamespaceDeletion
	RetryGeneration int64 `json:"retryGeneration,omitempty"`
	// Written by: Admin API POST NamespaceDeletion
	RetryReason string `json:"retryReason,omitempty"`
}

// NamespaceDeletionAttempt records a settled attempt before a retry starts.
type NamespaceDeletionAttempt struct {
	// Written by: NamespaceDeletion
	Generation int64 `json:"generation"`
	// Written by: NamespaceDeletion
	Reason string `json:"reason"`
	// Written by: NamespaceDeletion
	StoppedAt metav1.Time `json:"stoppedAt"`
	// Written by: NamespaceDeletion
	StopReason string `json:"stopReason"`
	// Written by: NamespaceDeletion
	StopMessage string `json:"stopMessage"`
}

// NamespaceDeletionStatus is everything the controller has learned.
//
// There is deliberately no Message field and no preflight struct: a message
// belongs on a condition, and the controller logic only needs "do I act or
// not" while a human needs one good condition string.
type NamespaceDeletionStatus struct {
	// AttemptGeneration identifies the currently executing attempt. Zero is
	// the original request, including documents created before retry existed.
	// Written by: NamespaceDeletion
	AttemptGeneration int64 `json:"attemptGeneration,omitempty"`
	// AttemptReason is the operator reason for the current retry. The original
	// attempt uses Spec.RequestReason instead.
	// Written by: NamespaceDeletion
	AttemptReason string `json:"attemptReason,omitempty"`
	// PreviousAttempts preserves stopped outcomes when a new attempt begins.
	// Written by: NamespaceDeletion
	PreviousAttempts []NamespaceDeletionAttempt `json:"previousAttempts,omitempty"`
	// NamespaceUID pins the observed namespace identity. A same-named
	// replacement cannot inherit this request; kube-applier checks the pinned
	// UID against the live object immediately before finalization.
	// +optional
	// Written by: NamespaceDeletion
	NamespaceUID string `json:"namespaceUID,omitempty"`

	// HostedClusterUID and ControlPlaneNamespaceUID fence the cleanup to the
	// exact deleting Kubernetes objects observed before mutation.
	// +optional
	// Written by: NamespaceDeletion
	HostedClusterUID string `json:"hostedClusterUID,omitempty"`
	// +optional
	// Written by: NamespaceDeletion
	ControlPlaneNamespaceUID string `json:"controlPlaneNamespaceUID,omitempty"`

	// CleanupIntent is persisted before publishing a destructive ApplyDesire.
	// +optional
	// Written by: NamespaceDeletion
	CleanupIntent string `json:"cleanupIntent,omitempty"`

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
}
