// Copyright 2025 Microsoft Corporation
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

// SystemAdminCredential is a Cosmos document representing a single
// system-admin (break-glass) credential for an ARO HCP cluster.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type SystemAdminCredential struct {
	CosmosMetadata `json:"cosmosMetadata"`

	Spec   SystemAdminCredentialSpec   `json:"spec"`
	Status SystemAdminCredentialStatus `json:"status"`
}

// SystemAdminCredentialSpec contains the desired state of the credential.
type SystemAdminCredentialSpec struct {
	// Username is the K8s username embedded in the cert CN. Defaulted at
	// create; the cluster's ACM cluster-admin role binding picks it up.
	Username string `json:"username,omitempty"`
	// ExpirationTimestamp is when the cert ceases to be valid. Server-set
	// at create (now + 24h) — we never let the customer pick.
	ExpirationTimestamp metav1.Time `json:"expirationTimestamp"`
	// OperationID is the ARM operation that created this credential. Used
	// to link the doc back to the customer-visible OperationResult.
	OperationID string `json:"operationID"`
	// PublicKeyPEM is the public half of the keypair generated at dispatch
	// time, PEM-encoded.
	PublicKeyPEM string `json:"publicKeyPEM"`
	// PrivateKeyPEM is the private half of the keypair, PEM-encoded.
	// Treat as a secret in logs, dumps, and telemetry.
	PrivateKeyPEM string `json:"privateKeyPEM"`
}

// SystemAdminCredentialStatus contains the observed state of the credential.
type SystemAdminCredentialStatus struct {
	// Phase is the lifecycle state.
	Phase SystemAdminCredentialPhase `json:"phase"`
	// SignedCertificate is the base64-DER cert the management-cluster
	// signer produced. Populated when Phase moves to Issued.
	SignedCertificate string `json:"signedCertificate,omitempty"`
	// Conditions is the standard rolling-status array.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// RevokedAt is set when Phase transitions to Revoked. Used by the
	// SystemAdminCredentialRevokedGC controller to delete the doc 48h
	// after revocation.
	RevokedAt *metav1.Time `json:"revokedAt,omitempty"`
	// OutstandingDesires names every per-credential kube-applier desire
	// that still exists in Cosmos for this credential.
	OutstandingDesires []SystemAdminCredentialDesireRef `json:"outstandingDesires,omitempty"`
}

// SystemAdminCredentialDesireRef points at a single kube-applier desire
// document scoped under the credential's parent cluster.
type SystemAdminCredentialDesireRef struct {
	Kind SystemAdminCredentialDesireKind `json:"kind"`
	Name string                          `json:"name"`
}

// SystemAdminCredentialDesireKind selects the kube-applier desire container.
type SystemAdminCredentialDesireKind string

const (
	SystemAdminCredentialDesireKindApply  SystemAdminCredentialDesireKind = "ApplyDesire"
	SystemAdminCredentialDesireKindRead   SystemAdminCredentialDesireKind = "ReadDesire"
	SystemAdminCredentialDesireKindDelete SystemAdminCredentialDesireKind = "DeleteDesire"
)

// SystemAdminCredentialPhase is the lifecycle state of a SystemAdminCredential.
type SystemAdminCredentialPhase string

const (
	SystemAdminCredentialPhaseRequested          SystemAdminCredentialPhase = "Requested"
	SystemAdminCredentialPhaseIssued             SystemAdminCredentialPhase = "Issued"
	SystemAdminCredentialPhaseAwaitingRevocation SystemAdminCredentialPhase = "AwaitingRevocation"
	SystemAdminCredentialPhaseRevoked            SystemAdminCredentialPhase = "Revoked"
	SystemAdminCredentialPhaseFailed             SystemAdminCredentialPhase = "Failed"
)
