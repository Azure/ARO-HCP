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

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

// StuckClusterCleanupMode selects how far the controller is permitted to go.
type StuckClusterCleanupMode string

const (
	// StuckClusterCleanupModeInspect runs the full preflight and records findings
	// without mutating anything on the management cluster. The controller still
	// writes one ReadDesire so it can observe the namespace; a ReadDesire is a
	// read of the management cluster and changes nothing there.
	StuckClusterCleanupModeInspect StuckClusterCleanupMode = "Inspect"

	// StuckClusterCleanupModeExecute additionally performs the cleanup once
	// preflight passes. Not honored until the mutation release; the controller
	// treats it as Inspect and says so in the phase message.
	StuckClusterCleanupModeExecute StuckClusterCleanupMode = "Execute"
)

// StuckClusterCleanupPhase is the coarse state of one cleanup request.
type StuckClusterCleanupPhase string

const (
	// StuckClusterCleanupPhasePending means the controller has not yet acted.
	StuckClusterCleanupPhasePending StuckClusterCleanupPhase = "Pending"

	// StuckClusterCleanupPhaseObserving means the ReadDesire has been written but
	// the namespace has not been mirrored back yet.
	StuckClusterCleanupPhaseObserving StuckClusterCleanupPhase = "Observing"

	// StuckClusterCleanupPhaseInspected means preflight completed and the findings
	// in Status are current. This is the terminal phase for Mode=Inspect.
	StuckClusterCleanupPhaseInspected StuckClusterCleanupPhase = "Inspected"

	// StuckClusterCleanupPhaseRefused means preflight found a live record, an
	// active publisher, or data it could not resolve, and will not proceed.
	// Preflight fails closed, so ambiguity lands here rather than in Inspected.
	StuckClusterCleanupPhaseRefused StuckClusterCleanupPhase = "Refused"

	// StuckClusterCleanupPhaseFailed means the controller could not complete the
	// inspection for an out-of-band reason (Cosmos or CS unavailable, and so on).
	// This phase is deliberately not terminal: the cause is expected to be
	// transient and the next resync retries.
	StuckClusterCleanupPhaseFailed StuckClusterCleanupPhase = "Failed"
)

// IsTerminal reports whether the phase represents a settled outcome that no
// further reconciliation will change. Terminal objects release the ReadDesire
// they were holding and become eligible for retention-based deletion.
func (p StuckClusterCleanupPhase) IsTerminal() bool {
	switch p {
	case StuckClusterCleanupPhaseInspected, StuckClusterCleanupPhaseRefused:
		return true
	default:
		return false
	}
}

const (
	// ConditionTypeStuckClusterCleanupNamespaceObserved reports whether the
	// target namespace has been mirrored back through its ReadDesire.
	ConditionTypeStuckClusterCleanupNamespaceObserved = "NamespaceObserved"

	// ConditionTypeStuckClusterCleanupPreflightPassed reports whether all three
	// preflight checks found no live owner. False means refuse; Unknown means
	// the checks have not all completed and is also a refusal, because preflight
	// fails closed.
	ConditionTypeStuckClusterCleanupPreflightPassed = "PreflightPassed"
)

// StuckClusterCleanup expresses the desire to retire one stuck Terminating
// namespace on one management cluster.
//
// It exists because the namespace-only orphans it targets have neither an ARM
// Cosmos document nor a Clusters Service record, so no existing controller has
// anything to trigger on. The entry point is therefore an explicit operator
// action through the admin API rather than automatic detection.
//
// The document is nested under a ManagementCluster, which is how the controller
// reaches a management cluster for a cluster that has no cluster document:
// kube-applier desires partition by management cluster, not by HCP cluster.
//
// See docs/ops/stuck-cluster-cleanup-controller-proposal.md.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type StuckClusterCleanup struct {
	// CosmosMetadata.ResourceID is nested under the management cluster; the
	// top-level resource name is a generated, stable identifier derived from the
	// target namespace name, so that a repeated request for the same namespace
	// addresses the same document.
	// PartitionKey holds the lowercased stamp identifier of the parent Stamp.
	coreapi.CosmosMetadata `json:"cosmosMetadata"`

	Spec StuckClusterCleanupSpec `json:"spec"`

	Status StuckClusterCleanupStatus `json:"status"`
}

// StuckClusterCleanupSpec is the operator's request. Every field here is
// supplied at creation and immutable afterwards, except NamespaceUID, which the
// controller pins on first observation and which is immutable once set.
type StuckClusterCleanupSpec struct {
	// Mode selects inspect-only or inspect-then-execute behavior.
	// Written by: Admin API POST StuckClusterCleanup
	Mode StuckClusterCleanupMode `json:"mode"`

	// NamespaceName is the exact name of the Terminating namespace to retire.
	// A prefix is never accepted: the operator must name the namespace exactly.
	// Written by: Admin API POST StuckClusterCleanup
	NamespaceName string `json:"namespaceName"`

	// NamespaceUID pins the identity of the namespace this request refers to.
	// It is empty at creation, because the UID can only be learned by reading
	// the namespace, and is set once by the controller on first observation.
	//
	// It is carried from the inspect release even though inspection does not
	// need it: it is both the deduplication key that distinguishes a re-created
	// namespace from the original and the delete precondition the mutation
	// release will require. Adding it to a live type later costs more than
	// carrying it early.
	//
	// +optional
	// Written by: StuckClusterCleanupPreflight
	NamespaceUID string `json:"namespaceUID,omitempty"`

	// ClusterServiceID optionally names the Clusters Service cluster this
	// namespace is believed to have belonged to. When supplied it turns the
	// preflight's CS lookup from a value derived out of the namespace name into
	// a value the operator has checked. Strengthens ownership proof; its absence
	// does not block inspection.
	//
	// +optional
	// Written by: Admin API POST StuckClusterCleanup
	ClusterServiceID string `json:"clusterServiceID,omitempty"`

	// ClusterResourceID optionally names the ARM resource this namespace is
	// believed to have belonged to, serving the same purpose as ClusterServiceID
	// for the ARM Cosmos lookup.
	//
	// +optional
	// Written by: Admin API POST StuckClusterCleanup
	ClusterResourceID *azcorearm.ResourceID `json:"clusterResourceID,omitempty"`

	// RequestReason is the operator's justification, recorded for audit. The
	// caller's identity is captured separately by the admin API's audit
	// middleware and is not duplicated here.
	// Written by: Admin API POST StuckClusterCleanup
	RequestReason string `json:"requestReason"`
}

// StuckClusterCleanupStatus is everything the controller has learned.
type StuckClusterCleanupStatus struct {
	// Phase is the coarse state of the request.
	// Written by: StuckClusterCleanupPreflight
	Phase StuckClusterCleanupPhase `json:"phase,omitempty"`

	// Message explains the current phase in operator-readable terms. For
	// Refused it states which check refused and why.
	//
	// +optional
	// Written by: StuckClusterCleanupPreflight
	Message string `json:"message,omitempty"`

	// Conditions carries NamespaceObserved and PreflightPassed.
	//
	// +optional
	// Written by: StuckClusterCleanupPreflight
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// CompletedAt is when the request first reached a terminal phase.
	//
	// It is the retention clock. Nothing else garbage-collects these documents:
	// they are stamp-rooted, so DeleteOrphanedCosmosResources cannot reach them
	// by its subscription-prefix walk. Without an explicit expiry every
	// invocation would persist for the lifetime of the container, so the
	// controller deletes a terminal request once this timestamp has aged past
	// the retention window.
	//
	// +optional
	// Written by: StuckClusterCleanupPreflight
	CompletedAt *metav1.Time `json:"completedAt,omitempty"`

	// Preflight records the outcome of the three ownership checks.
	//
	// +optional
	// Written by: StuckClusterCleanupPreflight
	Preflight *StuckClusterCleanupPreflightResult `json:"preflight,omitempty"`

	// ObservedNamespace is the distilled state of the target namespace, mirrored
	// from its ReadDesire. Nil means it has not been observed yet.
	//
	// +optional
	// Written by: StuckClusterCleanupPreflight
	ObservedNamespace *ObservedTerminatingNamespace `json:"observedNamespace,omitempty"`
}

// StuckClusterCleanupPreflightResult records the eligibility verdict and the
// three ownership checks.
//
// Eligibility and ownership answer different questions. Ownership asks whether
// anything still owns the namespace; eligibility asks whether the namespace is
// the kind of object this action may touch at all. A mistyped namespace name
// can land on a healthy namespace that no cluster record refers to, in which
// case every ownership check passes — so ownership alone does not protect
// against a mistaken request. Both must pass.
//
// Each ownership check is tri-state: nil means the check has not completed,
// and because preflight fails closed, nil is treated as a refusal rather than
// a pass.
type StuckClusterCleanupPreflightResult struct {
	// NamespaceEligible reports whether the namespace is a legitimate target:
	// not a platform namespace, already Terminating, deleting for longer than
	// the minimum dwell, and free of a surviving HostedCluster. Nil means the
	// verdict has not been reached yet.
	//
	// +optional
	// Written by: StuckClusterCleanupPreflight
	NamespaceEligible *bool `json:"namespaceEligible,omitempty"`

	// IneligibleReason names the eligibility check that refused, empty when
	// the namespace is eligible.
	//
	// +optional
	// Written by: StuckClusterCleanupPreflight
	IneligibleReason string `json:"ineligibleReason,omitempty"`

	// CheckedAt is when the three checks last ran to completion. Preflight is
	// re-run before any mutation, so this is not a one-time stamp.
	//
	// +optional
	// Written by: StuckClusterCleanupPreflight
	CheckedAt *metav1.Time `json:"checkedAt,omitempty"`

	// ARMCosmosRecordAbsent is true when a live point read of the ARM resources
	// container found no cluster document. Derived from a point read, never from
	// an ADX snapshot: snapshots are discovery signals, not proof of absence.
	//
	// +optional
	// Written by: StuckClusterCleanupPreflight
	ARMCosmosRecordAbsent *bool `json:"armCosmosRecordAbsent,omitempty"`

	// ClusterServiceRecordAbsent is true when a live Clusters Service lookup
	// found no record.
	//
	// +optional
	// Written by: StuckClusterCleanupPreflight
	ClusterServiceRecordAbsent *bool `json:"clusterServiceRecordAbsent,omitempty"`

	// NoSurvivingApplyDesires is true when no ServerSideApply desire for this
	// management cluster still targets the namespace. A surviving publisher
	// means something is still reconciling into it, which is a refusal.
	//
	// +optional
	// Written by: StuckClusterCleanupPreflight
	NoSurvivingApplyDesires *bool `json:"noSurvivingApplyDesires,omitempty"`

	// SurvivingApplyDesireNames lists the desires found by the scan, so an
	// operator can see what is still publishing without a breakglass session.
	//
	// +optional
	// Written by: StuckClusterCleanupPreflight
	SurvivingApplyDesireNames []string `json:"survivingApplyDesireNames,omitempty"`

	// RefusalReason names the first check that refused, empty when all passed.
	//
	// +optional
	// Written by: StuckClusterCleanupPreflight
	RefusalReason string `json:"refusalReason,omitempty"`
}

// ObservedTerminatingNamespace is the distilled live state of the target
// namespace.
//
// Remaining content is reported at group/resource granularity rather than per
// object, because that is the granularity the data supports: a ReadDesire reads
// exactly one object by exact name and there is no list primitive, so the
// findings come from the namespace's own status conditions
// (NamespaceContentRemaining, NamespaceFinalizersRemaining and friends) rather
// than from enumerating its contents.
type ObservedTerminatingNamespace struct {
	// UID is the observed namespace UID, mirrored into Spec.NamespaceUID on
	// first observation and compared against it on every later observation.
	//
	// +optional
	// Written by: StuckClusterCleanupPreflight
	UID string `json:"uid,omitempty"`

	// Phase is the namespace's own status.phase, expected to be "Terminating".
	//
	// +optional
	// Written by: StuckClusterCleanupPreflight
	Phase string `json:"phase,omitempty"`

	// DeletionTimestamp is when the namespace deletion was requested. The age
	// derived from it is the strongest signal that a namespace is genuinely
	// wedged rather than merely slow.
	//
	// +optional
	// Written by: StuckClusterCleanupPreflight
	DeletionTimestamp *metav1.Time `json:"deletionTimestamp,omitempty"`

	// SpecFinalizers is the namespace's own spec.finalizers, normally the single
	// "kubernetes" entry. Clearing it requires the namespaces/finalize
	// subresource, which the kube-applier is not granted; it is reported here so
	// the gap is visible in the findings rather than discovered during a cleanup.
	//
	// +optional
	// Written by: StuckClusterCleanupPreflight
	SpecFinalizers []string `json:"specFinalizers,omitempty"`

	// RemainingContent lists the group/resource pairs the API server still finds
	// inside the namespace, parsed from the NamespaceContentRemaining condition.
	// Empty with a Terminating phase points at a discovery or finalize failure
	// rather than leftover content.
	//
	// +optional
	// Written by: StuckClusterCleanupPreflight
	RemainingContent []RemainingGroupResource `json:"remainingContent,omitempty"`

	// NamespaceConditions is the namespace's own status.conditions, carried
	// verbatim. This is the primary evidence the inspect release exists to
	// collect, so it is recorded whole rather than summarized away.
	//
	// +optional
	// Written by: StuckClusterCleanupPreflight
	NamespaceConditions []metav1.Condition `json:"namespaceConditions,omitempty"`
}

// RemainingGroupResource identifies one kind of object still present in a
// Terminating namespace.
type RemainingGroupResource struct {
	// Group is the API group, empty for the core group.
	//
	// +optional
	// Written by: StuckClusterCleanupPreflight
	Group string `json:"group,omitempty"`

	// Resource is the lower-cased plural resource name, e.g. "poddisruptionbudgets".
	// Written by: StuckClusterCleanupPreflight
	Resource string `json:"resource"`

	// KubeApplierPermitted reports whether the kube-applier's ClusterRole grants
	// delete on this group/resource today. False means retiring this content
	// needs an RBAC change before any mutation release can touch it, which is
	// the single most useful thing the inspection can tell us.
	//
	// +optional
	// Written by: StuckClusterCleanupPreflight
	KubeApplierPermitted *bool `json:"kubeApplierPermitted,omitempty"`
}
