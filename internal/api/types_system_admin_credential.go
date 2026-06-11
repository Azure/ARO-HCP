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

// SystemAdminCredential is an internal, per-cluster, per-credential-request
// tracking document for break-glass-style admin kubeconfigs. It is NOT
// exposed through the ARM API surface; the customer-visible handle is the
// async Operation document. See docs/system-admin-credentials/PLAN.md for
// the full design.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type SystemAdminCredential struct {
	// CosmosMetadata.ResourceID is nested under the parent HCPOpenShiftCluster
	// so that listing the partition by parent prefix naturally returns the
	// credentials associated with that cluster, and cluster deletion can
	// sweep them.
	CosmosMetadata `json:"cosmosMetadata"`

	Spec   SystemAdminCredentialSpec   `json:"spec"`
	Status SystemAdminCredentialStatus `json:"status"`
}

// SystemAdminCredentialSpec carries the customer-visible declarative input
// plus the keypair the dispatch controller generates at create time. The
// private key lives here in PEM form; see the "Deliberate departure from
// cluster-service's security model" section of PLAN.md for the trade-off.
type SystemAdminCredentialSpec struct {
	// Username is the K8s username embedded in the cert CN. Defaulted at
	// create; the cluster's ACM cluster-admin role binding picks it up.
	Username string `json:"username,omitempty"`
	// ExpirationTimestamp is when the cert ceases to be valid. Server-set
	// at create (now + 24h) — never customer-chosen.
	ExpirationTimestamp metav1.Time `json:"expirationTimestamp"`
	// OperationID is the ARM Operation that created this credential. Used
	// to link the doc back to the customer-visible OperationResult, and
	// also as the dispatcher's idempotency key.
	OperationID string `json:"operationID"`
	// PublicKeyPEM is the public half of the RSA keypair generated at
	// dispatch time. The CSR's request payload carries the DER form of
	// this same key; PEM here is for diagnostics and golden-file fixtures.
	PublicKeyPEM string `json:"publicKeyPEM"`
	// PrivateKeyPEM is the private half of the RSA keypair. It is the
	// input to OperationResult's kubeconfig assembly and never leaves
	// Cosmos. Treat as a secret in logs, dumps, and telemetry. Zeroed by
	// the revoke poller once Phase moves to Revoked so a stale Cosmos
	// read can no longer recover the key.
	PrivateKeyPEM string `json:"privateKeyPEM"`
}

// SystemAdminCredentialStatus holds the lifecycle phase, the signed
// certificate once issuance completes, the revocation anchor, and the
// list of per-credential kube-applier desires still outstanding for this
// credential.
type SystemAdminCredentialStatus struct {
	// Phase is the lifecycle state. Mirrors the cluster-service `status`
	// column we are replacing.
	Phase SystemAdminCredentialPhase `json:"phase,omitempty"`
	// SignedCertificate is the base64-DER cert the management-cluster
	// signer produced. Populated when Phase moves to Issued. Mirrored
	// here so OperationResult does not have to chase the MC for
	// CSR.status.certificate on the hot path.
	SignedCertificate string `json:"signedCertificate,omitempty"`
	// Conditions is the standard rolling-status array.
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
	// RevokedAt is set when Phase transitions to Revoked. It is the
	// anchor used by the SystemAdminCredentialRevokedGC controller to
	// delete the doc 48h after revocation lands — chosen to outlast the
	// certificate's 24h TTL so a stale row can never describe a
	// still-valid kubeconfig.
	RevokedAt *metav1.Time `json:"revokedAt,omitempty"`
	// OutstandingDesires names every per-credential kube-applier desire
	// that still exists in Cosmos for this credential. The dispatch
	// controller seeds this list; the post-issuance cleanup and the
	// cluster-deletion gate walk it to drive teardown; the revoke poller
	// uses it to handle credentials that were never issued. Empty list
	// means the credential has no live MC content of its own.
	OutstandingDesires []SystemAdminCredentialDesireRef `json:"outstandingDesires,omitempty"`
}

// SystemAdminCredentialDesireRef points at a single kube-applier desire
// document scoped under the credential's parent cluster. Kind selects the
// container (ApplyDesires / ReadDesires / DeleteDesires); Name is the
// desire document's last-segment name within that container. Together
// they form the desire's full resource ID via the standard
// kubeapplier.To<…>ScopedDesireResourceIDString helpers.
type SystemAdminCredentialDesireRef struct {
	Kind SystemAdminCredentialDesireKind `json:"kind"`
	Name string                          `json:"name"`
}

type SystemAdminCredentialDesireKind string

const (
	SystemAdminCredentialDesireKindApply  SystemAdminCredentialDesireKind = "ApplyDesire"
	SystemAdminCredentialDesireKindRead   SystemAdminCredentialDesireKind = "ReadDesire"
	SystemAdminCredentialDesireKindDelete SystemAdminCredentialDesireKind = "DeleteDesire"
)

type SystemAdminCredentialPhase string

const (
	// SystemAdminCredentialPhaseRequested means the credential doc exists
	// and the CSR/CSRA/RBAC ApplyDesires have been written but the cert
	// has not yet been observed signed.
	SystemAdminCredentialPhaseRequested SystemAdminCredentialPhase = "Requested"
	// SystemAdminCredentialPhaseIssued means the IssuanceObserver has
	// observed the signed certificate on the mirrored CSR and copied it
	// into Status.SignedCertificate. The kubeconfig is now servable.
	SystemAdminCredentialPhaseIssued SystemAdminCredentialPhase = "Issued"
	// SystemAdminCredentialPhaseAwaitingRevocation means the revoke
	// dispatcher has flipped this credential as part of a revoke
	// operation; the per-cluster CRR ApplyDesire is what actually drives
	// revocation on the MC.
	SystemAdminCredentialPhaseAwaitingRevocation SystemAdminCredentialPhase = "AwaitingRevocation"
	// SystemAdminCredentialPhaseRevoked means the CRR confirmed
	// PreviousCertificatesRevoked=True and the credential's MC content
	// (if any remained) is gone. The doc itself will be GC'd 48h after
	// Status.RevokedAt by controller #9.
	SystemAdminCredentialPhaseRevoked SystemAdminCredentialPhase = "Revoked"
	// SystemAdminCredentialPhaseFailed means the issuance pipeline
	// terminated with an error (e.g. the signer denied the CSR). The
	// per-credential teardown still runs.
	SystemAdminCredentialPhaseFailed SystemAdminCredentialPhase = "Failed"
)

var _ arm.CosmosPersistable = &SystemAdminCredential{}

// EnsureDefaults is a no-op today but exists to mirror the other internal
// types' shape. Add defaulting rules here for any field where the zero
// value is never valid user input. See
// docs/api-version-defaults-and-storage.md for the contract.
func (c *SystemAdminCredential) EnsureDefaults() {}
