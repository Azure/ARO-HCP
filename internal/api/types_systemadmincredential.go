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
type SystemAdminCredentialRequestSpec struct {
	// Username is the K8s username embedded in the cert CN.
	Username string `json:"username,omitempty"`
	// ExpirationTimestamp is when the cert ceases to be valid (server-set, now + 24h).
	ExpirationTimestamp metav1.Time `json:"expirationTimestamp"`
	// OperationID is the ARM operation that created this credential request.
	OperationID string `json:"operationID"`
	// PublicKeyPEM is the public half of the keypair, PEM-encoded.
	PublicKeyPEM string `json:"publicKeyPEM"`
	// PrivateKeyPEM is the private half of the keypair, PEM-encoded.
	// Treat as a secret in logs, dumps, and telemetry.
	PrivateKeyPEM string `json:"privateKeyPEM"`
}

// SystemAdminCredentialRequestStatus contains the observed state of the credential request.
type SystemAdminCredentialRequestStatus struct {
	// SignedCertificate is the base64-DER cert the management-cluster signer produced.
	SignedCertificate string `json:"signedCertificate,omitempty"`
	// Conditions tracks lifecycle state using standard metav1.Conditions.
	// Known condition types:
	//   - "Issued": True when the CSR has been signed and the cert is available.
	//   - "Failed": True when the CSR was denied or issuance otherwise failed.
	//   - "AwaitingRevocation": True when revocation has been requested but not yet confirmed.
	//   - "Revoked": True when the credential has been revoked.
	//   - "ContentDeleted": True when all MC-side objects have been cleaned up.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// RevokedAt is set when the Revoked condition transitions to True.
	RevokedAt *metav1.Time `json:"revokedAt,omitempty"`
	// DeleteTimestamp is set when deletion has been requested for this credential
	// request. Controllers use this to drive teardown of associated kube-applier
	// desires before removing the credential request document itself.
	DeleteTimestamp *metav1.Time `json:"deleteTimestamp,omitempty"`
}

// SystemAdminCredentialRequest condition types.
const (
	SystemAdminCredentialRequestConditionIssued             = "Issued"
	SystemAdminCredentialRequestConditionFailed             = "Failed"
	SystemAdminCredentialRequestConditionAwaitingRevocation = "AwaitingRevocation"
	SystemAdminCredentialRequestConditionRevoked            = "Revoked"
	SystemAdminCredentialRequestConditionContentDeleted     = "ContentDeleted"
)

// SystemAdminCredentialsRevocation represents a revocation event tracked in
// Cosmos. When a RevokeCredentials operation fires, one of these documents is
// created. Desires related to the revocation (CRR Apply/Read) are scoped under
// this type rather than on individual credential requests.
//
// +k8s:deepcopy-gen=true
type SystemAdminCredentialsRevocation struct {
	CosmosMetadata `json:"cosmosMetadata"`

	Spec   SystemAdminCredentialsRevocationSpec   `json:"spec"`
	Status SystemAdminCredentialsRevocationStatus `json:"status"`
}

// SystemAdminCredentialsRevocationSpec contains the desired state of the revocation.
type SystemAdminCredentialsRevocationSpec struct {
	// OperationID is the ARM operation that triggered the revocation.
	OperationID string `json:"operationID"`
	// RevokeOpSuffix is the shortened operation ID used in CRR object names.
	RevokeOpSuffix string `json:"revokeOpSuffix"`
}

// SystemAdminCredentialsRevocationStatus contains the observed state of the revocation.
type SystemAdminCredentialsRevocationStatus struct {
	// Conditions tracks revocation lifecycle.
	// Known condition types:
	//   - "CertificatesRevoked": True when the CRR confirms revocation is complete.
	//   - "CredentialsMarkedRevoked": True when all credential requests have been flipped.
	//   - "Complete": True when the entire revocation flow is done.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// SystemAdminCredentialContentDeletedCondition returns a metav1.Condition
// that signals all credential-related MC content has been removed and
// the cluster-deletion finalizer can advance.
func SystemAdminCredentialContentDeletedCondition() metav1.Condition {
	return metav1.Condition{
		Type:               "SystemAdminCredentialContentDeleted",
		Status:             metav1.ConditionTrue,
		LastTransitionTime: metav1.Now(),
		Reason:             "ContentDeleted",
		Message:            "All SystemAdminCredentialRequest content has been deleted",
	}
}

// Condition helper functions for SystemAdminCredentialRequest.

// SetCondition sets or updates a condition on the credential request status.
func (s *SystemAdminCredentialRequestStatus) SetCondition(conditionType string, status metav1.ConditionStatus, reason, message string) {
	now := metav1.Now()
	for i := range s.Conditions {
		if s.Conditions[i].Type == conditionType {
			if s.Conditions[i].Status != status {
				s.Conditions[i].LastTransitionTime = now
			}
			s.Conditions[i].Status = status
			s.Conditions[i].Reason = reason
			s.Conditions[i].Message = message
			return
		}
	}
	s.Conditions = append(s.Conditions, metav1.Condition{
		Type:               conditionType,
		Status:             status,
		LastTransitionTime: now,
		Reason:             reason,
		Message:            message,
	})
}

// GetCondition returns the condition with the given type, or nil if not found.
func (s *SystemAdminCredentialRequestStatus) GetCondition(conditionType string) *metav1.Condition {
	for i := range s.Conditions {
		if s.Conditions[i].Type == conditionType {
			return &s.Conditions[i]
		}
	}
	return nil
}

// IsIssued returns true if the Issued condition is True.
func (s *SystemAdminCredentialRequestStatus) IsIssued() bool {
	c := s.GetCondition(SystemAdminCredentialRequestConditionIssued)
	return c != nil && c.Status == metav1.ConditionTrue
}

// IsFailed returns true if the Failed condition is True.
func (s *SystemAdminCredentialRequestStatus) IsFailed() bool {
	c := s.GetCondition(SystemAdminCredentialRequestConditionFailed)
	return c != nil && c.Status == metav1.ConditionTrue
}

// IsAwaitingRevocation returns true if the AwaitingRevocation condition is True.
func (s *SystemAdminCredentialRequestStatus) IsAwaitingRevocation() bool {
	c := s.GetCondition(SystemAdminCredentialRequestConditionAwaitingRevocation)
	return c != nil && c.Status == metav1.ConditionTrue
}

// IsRevoked returns true if the Revoked condition is True.
func (s *SystemAdminCredentialRequestStatus) IsRevoked() bool {
	c := s.GetCondition(SystemAdminCredentialRequestConditionRevoked)
	return c != nil && c.Status == metav1.ConditionTrue
}

// IsTerminal returns true if the credential request has reached a terminal state
// (either Failed or Revoked).
func (s *SystemAdminCredentialRequestStatus) IsTerminal() bool {
	return s.IsFailed() || s.IsRevoked()
}

// IsPending returns true if the credential request has been created but has not
// yet been issued, failed, or entered revocation.
func (s *SystemAdminCredentialRequestStatus) IsPending() bool {
	return !s.IsIssued() && !s.IsFailed() && !s.IsAwaitingRevocation() && !s.IsRevoked()
}
