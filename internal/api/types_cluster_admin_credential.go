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
	"time"
)

// ClusterAdminCredential is an internal Cosmos document that tracks a
// Clusters Service break-glass credential for an HCP cluster.
// The document name is the CS break-glass credential ID.
//
// This is distinct from HCPOpenShiftClusterAdminCredential, which is the
// customer-facing ARM response DTO returned by requestAdminCredential.
//
// TODO is the API design of this, including data types, json struct tags and so on what we want?
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ClusterAdminCredential struct {
	// CosmosMetadata ResourceID is nested under the cluster so that association and cleanup work as expected.
	// PartitionKey holds the lowercased subscriptionID.
	CosmosMetadata `json:"cosmosMetadata"`

	// OperationID is the ARM operation name (UUID) that requested this credential.
	// Written by: DispatchRequestCredential
	OperationID string `json:"operationId,omitempty"`

	// ClusterServiceInternalID is the full CS HREF for this break-glass credential.
	// Written by: DispatchRequestCredential
	ClusterServiceInternalID InternalID `json:"clusterServiceInternalId,omitempty"`

	// Status is the RP-owned credential lifecycle status.
	// Written by: DispatchRequestCredential, SyncClusterAdminCredentials
	Status ClusterAdminCredentialStatus `json:"status,omitempty"`

	// ExpirationTimestamp is when the credential expires. It mirrors the CS BreakGlassCredential expiration timestamp.
	// Written by: DispatchRequestCredential, SyncClusterAdminCredentials
	ExpirationTimestamp time.Time `json:"expirationTimestamp,omitempty"`

	// Kubeconfig is the temporary admin kubeconfig. Present once the credential is issued.
	// Written by: SyncClusterAdminCredentials
	Kubeconfig string `json:"kubeconfig,omitempty"`
}

// ClusterAdminCredentialStatus is the RP-owned lifecycle status for a
// ClusterAdminCredential. Values are converted from Clusters Service at the
// OCM boundary.
type ClusterAdminCredentialStatus string

const (
	ClusterAdminCredentialStatusCreated            ClusterAdminCredentialStatus = "Created"
	ClusterAdminCredentialStatusIssued             ClusterAdminCredentialStatus = "Issued"
	ClusterAdminCredentialStatusFailed             ClusterAdminCredentialStatus = "Failed"
	ClusterAdminCredentialStatusExpired            ClusterAdminCredentialStatus = "Expired"
	ClusterAdminCredentialStatusAwaitingRevocation ClusterAdminCredentialStatus = "AwaitingRevocation"
	ClusterAdminCredentialStatusRevoked            ClusterAdminCredentialStatus = "Revoked"
)
