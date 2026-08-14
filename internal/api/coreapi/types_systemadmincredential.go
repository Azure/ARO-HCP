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

package coreapi

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SystemAdminCredentialRequest represents a temporary admin credential request
// tracked in Cosmos, replacing the cluster-service break-glass credential flow.
//
// Lifecycle is tracked via metav1.Conditions rather than an explicit Phase enum
// so that individual aspects of the request (issuance, revocation, cleanup) can
// progress independently and callers can reason about each concern separately.
//
// +k8s:deepcopy-gen=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type SystemAdminCredentialRequest struct {
	CosmosMetadata `json:"cosmosMetadata"`

	Spec   SystemAdminCredentialRequestSpec   `json:"spec"`
	Status SystemAdminCredentialRequestStatus `json:"status"`
}

// SystemAdminCredentialRequestSpec contains the desired state of the credential request.
// Written by: SystemAdminCredentialDispatchRequestCredential (all fields set at creation)
type SystemAdminCredentialRequestSpec struct {
	// CreationTimestamp is when the credential request was created (server-set).
	// The garbage collector deletes each request once it is older than the
	// retention window, regardless of the request's status.
	// Written by: SystemAdminCredentialDispatchRequestCredential
	CreationTimestamp metav1.Time `json:"creationTimestamp"`
	// ExpirationTimestamp is when the cert ceases to be valid (server-set, now + 24h).
	// Written by: SystemAdminCredentialDispatchRequestCredential
	ExpirationTimestamp metav1.Time `json:"expirationTimestamp"`
	// OperationID is the ARM operation that created this credential request.
	// Written by: SystemAdminCredentialDispatchRequestCredential
	OperationID string `json:"operationID"`
	// CertificateSigningRequestPEM is the PEM-encoded PKCS#10 certificate signing
	// request provided by the caller. The backend embeds this directly in
	// the Kubernetes CertificateSigningRequest it creates on the management
	// cluster. The corresponding private key is held only by the caller.
	// Written by: SystemAdminCredentialDispatchRequestCredential
	CertificateSigningRequestPEM string `json:"certificateSigningRequestPEM"`
}

// SystemAdminCredentialRequestStatus contains the observed state of the credential request.
type SystemAdminCredentialRequestStatus struct {
	// SignedCertificate is the base64-encoded PEM certificate the
	// management-cluster signer produced (the CSR's Status.Certificate, which
	// the Kubernetes API guarantees to be PEM-encoded).
	// Written by: SystemAdminCredentialIssuanceObserver
	SignedCertificate string `json:"signedCertificate,omitempty"`
	// Conditions tracks lifecycle state using standard metav1.Conditions.
	// Known condition types:
	//   - "Issued": True when the CSR has been signed and the cert is available.
	//   - "Failed": True when the CSR was denied or issuance otherwise failed.
	//   - "AwaitingRevocation": True when revocation has been requested but not yet confirmed.
	//   - "Revoked": True when the credential has been revoked.
	//   - "ContentDeleted": True when all MC-side objects have been cleaned up.
	// Written by: SystemAdminCredentialIssuanceObserver (Issued, Failed)
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// RevokedAt is set when the Revoked condition transitions to True.
	RevokedAt *metav1.Time `json:"revokedAt,omitempty"`
	// DeletionTimestamp is set when deletion has been requested for this credential
	// request. Controllers use this to drive teardown of associated kube-applier
	// desires before removing the credential request document itself.
	// Written by: SystemAdminCredentialRevocationMarkRequests
	DeletionTimestamp *metav1.Time `json:"deletionTimestamp,omitempty"`
}

// SystemAdminCredentialRequest condition types.
const (
	SystemAdminCredentialRequestConditionIssued             = "Issued"
	SystemAdminCredentialRequestConditionFailed             = "Failed"
	SystemAdminCredentialRequestConditionAwaitingRevocation = "AwaitingRevocation"
	SystemAdminCredentialRequestConditionRevoked            = "Revoked"
	SystemAdminCredentialRequestConditionContentDeleted     = "ContentDeleted"
)
