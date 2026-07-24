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

// SystemAdminCredentialRevocation represents a single revocation of all of a
// cluster's system admin credentials, tracked in Cosmos and nested under the
// cluster. When a RevokeCredentials operation fires, the dispatch controller
// creates one of these documents and records its resource ID on the operation's
// InternalID. Dedicated controllers then drive the revocation lifecycle:
//
//   - one controller live-lists every SystemAdminCredentialRequest for the
//     cluster and marks each with a DeleteTimestamp;
//   - one controller manages the CertificateRevocationRequest (CRR) desires that
//     ask the hosted cluster to revoke already-issued certificates;
//   - one completion controller marks the revocation for deletion once the
//     credentials are marked and the HCP confirms revocation;
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
