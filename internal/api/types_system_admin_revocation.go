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

package api

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// SystemAdminRevocation is the cluster-scoped, per-revoke-operation
// tracking document for a customer-initiated `revokecredentials` ARM
// request. It is NOT exposed through the ARM API surface — the
// customer-visible handle is the async Operation document.
//
// Created by operationRevokeCredentialsDispatch when a revoke operation
// is dispatched. Consumed by two SystemAdminRevocationWatching controllers:
//
//   - The credential-deletion-initiator walks every live
//     SystemAdminCredential under the cluster and sets
//     Spec.DeletionTimestamp on each, handing them to the
//     credentialDesiresCreator teardown branch.
//   - The revocation-desires controller owns the per-revoke
//     CertificateRevocationRequest ApplyDesire / ReadDesire and the
//     revocation RBAC ApplyDesires. When the CRR mirror reports
//     PreviousCertificatesRevoked=True, it tears down every
//     OutstandingDesire and flips
//     Status.Conditions[RevocationCompleteConditionType] to True.
//
// operationRevokeCredentialsPoll watches that condition to drive the
// ARM operation to Succeeded; it never touches the CRR itself.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type SystemAdminRevocation struct {
	// CosmosMetadata.ResourceID is nested under the parent HCPOpenShiftCluster
	// at .../hcpOpenShiftClusters/<cluster>/systemAdminRevocations/<name>.
	CosmosMetadata `json:"cosmosMetadata"`

	Spec   SystemAdminRevocationSpec   `json:"spec"`
	Status SystemAdminRevocationStatus `json:"status"`
}

// SystemAdminRevocationSpec carries the minimum input the revocation
// controllers need: which ARM Operation requested the revoke, and when.
type SystemAdminRevocationSpec struct {
	// OperationID is the ARM Operation that requested the revocation.
	// The dispatcher uses this as the idempotency key and the poller
	// uses it to correlate back to the customer-visible OperationResult.
	OperationID string `json:"operationID"`
	// RequestedAt is when the dispatcher created this revocation. Used
	// for diagnostics; the revoke poll has its own timeout policy.
	RequestedAt metav1.Time `json:"requestedAt"`
}

// SystemAdminRevocationStatus carries the revocation lifecycle: the
// kube-applier desires the revocation-desires controller has registered
// on the management cluster, and the RevocationComplete condition that
// the operation poller treats as the success signal.
type SystemAdminRevocationStatus struct {
	// Conditions is the standard rolling-status array. The
	// RevocationCompleteConditionType entry, when True, tells
	// operationRevokeCredentialsPoll that the management-cluster work
	// is finished and the operation can move to Succeeded.
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

	// OutstandingDesires names every kube-applier desire the
	// revocation-desires controller has written for this revocation.
	// The teardown branch walks this list once the CRR reports
	// PreviousCertificatesRevoked=True.
	OutstandingDesires []SystemAdminRevocationDesireRef `json:"outstandingDesires,omitempty"`
}

// SystemAdminRevocationDesireRef points at a single kube-applier desire
// document scoped under the revocation's parent cluster.
type SystemAdminRevocationDesireRef struct {
	Kind SystemAdminRevocationDesireKind `json:"kind"`
	Name string                          `json:"name"`
}

type SystemAdminRevocationDesireKind string

const (
	SystemAdminRevocationDesireKindApply  SystemAdminRevocationDesireKind = "ApplyDesire"
	SystemAdminRevocationDesireKindRead   SystemAdminRevocationDesireKind = "ReadDesire"
	SystemAdminRevocationDesireKindDelete SystemAdminRevocationDesireKind = "DeleteDesire"
)

// SystemAdminRevocationCompleteConditionType is the
// SystemAdminRevocationStatus condition that the revocation-desires
// controller flips to True once the CRR confirms revocation on the
// management cluster and every kube-applier desire the revocation owned
// has been torn down. operationRevokeCredentialsPoll reads this to
// drive the ARM operation to Succeeded.
const SystemAdminRevocationCompleteConditionType = "RevocationComplete"

var _ arm.CosmosPersistable = &SystemAdminRevocation{}

// EnsureDefaults is a no-op today but exists to mirror the other internal
// cosmos types so future server-side defaulting has a hook.
func (r *SystemAdminRevocation) EnsureDefaults() {}
