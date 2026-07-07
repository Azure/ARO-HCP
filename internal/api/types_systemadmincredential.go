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
	// SignedCertificate is the base64-encoded PEM certificate the
	// management-cluster signer produced (the CSR's Status.Certificate, which
	// the Kubernetes API guarantees to be PEM-encoded).
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

// SystemAdminCredentialRevocation represents a single revocation of all of a
// cluster's system admin credentials, tracked in Cosmos and nested under the
// cluster. When a RevokeCredentials operation fires, the dispatch controller
// creates one of these documents and records its resource ID on the operation's
// InternalID. Dedicated controllers then drive the revocation lifecycle:
//
//   - one controller live-lists every SystemAdminCredentialRequest for the
//     cluster and marks each with a DeleteTimestamp;
//   - one controller manages the CertificateRevocationRequest (CRR) desires that
//     ask the hosted cluster to revoke already-issued certificates, and — once
//     the credentials are marked and the HCP confirms revocation — marks this
//     revocation for deletion;
//   - one deletion controller tears down the revocation's desires and, when they
//     are all gone, deletes this document.
//
// The RevokeCredentials operation completes once this document no longer exists.
//
// +k8s:deepcopy-gen=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type SystemAdminCredentialRevocation struct {
	CosmosMetadata `json:"cosmosMetadata"`

	Spec   SystemAdminCredentialRevocationSpec   `json:"spec"`
	Status SystemAdminCredentialRevocationStatus `json:"status"`
}

// SystemAdminCredentialRevocationSpec contains the desired state of the revocation.
type SystemAdminCredentialRevocationSpec struct {
	// OperationID is the ARM operation that triggered the revocation.
	OperationID string `json:"operationID"`
	// RevokeOpSuffix is the shortened operation ID used in CRR object names.
	RevokeOpSuffix string `json:"revokeOpSuffix"`
}

// SystemAdminCredentialRevocationStatus contains the observed state of the revocation.
type SystemAdminCredentialRevocationStatus struct {
	// Conditions tracks revocation lifecycle using standard metav1.Conditions.
	// Known condition types:
	//   - "CredentialsMarkedForDeletion": True when every SystemAdminCredentialRequest
	//     for the cluster has been marked with a DeleteTimestamp.
	//   - "CertificatesRevoked": True when the CRR confirms previously-issued
	//     certificates have been revoked on the hosted cluster.
	//   - "Complete": True when the whole revocation flow is done and this document
	//     may be deleted.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// DeleteTimestamp is set once revocation is complete and the deletion
	// controller should tear down the revocation's desires and remove this
	// document.
	DeleteTimestamp *metav1.Time `json:"deleteTimestamp,omitempty"`
}

// SystemAdminCredentialRevocation condition types.
const (
	SystemAdminCredentialRevocationConditionCredentialsMarkedForDeletion = "CredentialsMarkedForDeletion"
	SystemAdminCredentialRevocationConditionCertificatesRevoked          = "CertificatesRevoked"
	SystemAdminCredentialRevocationConditionComplete                     = "Complete"
)

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

// Condition helper functions for SystemAdminCredentialRevocation.

// SetCondition sets or updates a condition on the revocation status.
func (s *SystemAdminCredentialRevocationStatus) SetCondition(conditionType string, status metav1.ConditionStatus, reason, message string) {
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
func (s *SystemAdminCredentialRevocationStatus) GetCondition(conditionType string) *metav1.Condition {
	for i := range s.Conditions {
		if s.Conditions[i].Type == conditionType {
			return &s.Conditions[i]
		}
	}
	return nil
}

func (s *SystemAdminCredentialRevocationStatus) isConditionTrue(conditionType string) bool {
	c := s.GetCondition(conditionType)
	return c != nil && c.Status == metav1.ConditionTrue
}

// IsCredentialsMarkedForDeletion returns true when every credential request has
// been marked with a DeleteTimestamp.
func (s *SystemAdminCredentialRevocationStatus) IsCredentialsMarkedForDeletion() bool {
	return s.isConditionTrue(SystemAdminCredentialRevocationConditionCredentialsMarkedForDeletion)
}

// IsCertificatesRevoked returns true when the hosted cluster has confirmed the
// previously-issued certificates are revoked.
func (s *SystemAdminCredentialRevocationStatus) IsCertificatesRevoked() bool {
	return s.isConditionTrue(SystemAdminCredentialRevocationConditionCertificatesRevoked)
}

// IsComplete returns true when the revocation flow has finished and the document
// may be deleted.
func (s *SystemAdminCredentialRevocationStatus) IsComplete() bool {
	return s.isConditionTrue(SystemAdminCredentialRevocationConditionComplete)
}
