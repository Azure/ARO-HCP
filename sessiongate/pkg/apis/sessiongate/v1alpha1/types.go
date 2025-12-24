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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type Session struct {
	metav1.TypeMeta `json:",inline"`

	// +optional

	// metadata is a standard object metadata
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// +required

	// spec defines the desired state of Session
	Spec SessionSpec `json:"spec"`

	// +optional

	// status defines the observed state of Session
	Status SessionStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec is immutable"

type SessionSpec struct {
	// +kubebuilder:validation:Required

	// ttl is the time-to-live duration for the session
	TTL metav1.Duration `json:"ttl"`

	// +kubebuilder:validation:Required

	// managementCluster specifies the AKS management cluster
	ManagementCluster ManagementCluster `json:"managementCluster"`

	// +kubebuilder:validation:Required

	// hostedControlPlane specifies the hosted control plane
	HostedControlPlane HostedControlPlane `json:"hostedControlPlane"`

	// +kubebuilder:validation:Required

	// accessLevel defines the access permissions for the session
	AccessLevel AccessLevel `json:"accessLevel"`

	// +kubebuilder:validation:Required

	// owner identifies the principal (user or service account) that owns this session
	Owner Principal `json:"owner"`
}

// ManagementCluster identifies an Azure management cluster
type ManagementCluster struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^/subscriptions/[a-fA-F0-9-]+/resourceGroups/[^/]+/providers/[^/]+/[^/]+/[^/]+$`

	// resourceId is the Azure resource ID of the management cluster
	ResourceID string `json:"resourceId"`
}

// HostedCluster identifies an hosted cluster
type HostedControlPlane struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^/subscriptions/[a-fA-F0-9-]+/resourceGroups/[^/]+/providers/[^/]+/[^/]+/[^/]+$`

	// resourceId is the Azure resource ID of the hosted cluster
	ResourceID string `json:"resourceId"`

	// +kubebuilder:validation:Required

	// namespace of the HostedControlPlane CR
	Namespace string `json:"namespace,omitempty"`
}

// describes what level of access the session has on the hosted cluster
type AccessLevel struct {
	// +kubebuilder:validation:Required

	// the session will have the permissions of this Group.rbac.authorization.k8s.io
	Group string `json:"group"`
}

// +kubebuilder:validation:Enum=User

// PrincipalType defines the type of principal for authentication
type PrincipalType string

const (
	// PrincipalTypeUser represents a human user principal
	PrincipalTypeUser PrincipalType = "User"
)

// +kubebuilder:validation:XValidation:rule="self.type == 'User' ? has(self.userPrincipal) : true",message="userPrincipal must be set when type is User"

// Principal identifies the authenticated entity that owns this session
type Principal struct {
	// +kubebuilder:validation:Required

	// type specifies the authentication method
	Type PrincipalType `json:"type"`

	// +optional

	// userPrincipal identifies the user principal
	// Required when type is User
	UserPrincipal *UserPrincipal `json:"userPrincipal,omitempty"`
}

// UserPrincipal represents a user identity
type UserPrincipal struct {
	// +kubebuilder:validation:Required

	// name is the user principal name (e.g., UPN for Azure AD like user@domain.com)
	Name string `json:"name"`

	// +kubebuilder:validation:Required

	// claim specifies which JWT claim to use for authentication (e.g., "upn", "email", "sub")
	Claim string `json:"claim"`
}

type SessionStatus struct {
	// +optional
	// +listType=map
	// +listMapKey=type
	// +patchMergeKey=type
	// +patchStrategy=merge

	// The status of each condition is one of True, False, or Unknown.
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// +optional

	// timestamp when the session will expire
	// sematics: creation timestamp of the session CR + TTL
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`

	// +optional

	// the public endpoint that can be used to access the session endpoint
	// this endpoint acts as a KAS proxy to reach the HCPs KAS
	Endpoint string `json:"endpoint,omitempty"`

	// +optional

	// authorizationPolicyRef references the AuthorizationPolicy resource
	AuthorizationPolicyRef string `json:"authorizationPolicyRef,omitempty"`

	// +optional

	// credentialsSecretRef references the Secret containing the session credentials
	CredentialsSecretRef string `json:"credentialsSecretRef,omitempty"`

	// csrRef references the CertificateSigningRequest resource
	CSRRef string `json:"csrRef,omitempty"`

	// +optional

	// the URL used by the session for communication with the hosted control planes KAS
	// this might be the public endpoint of the HCP KAS or some sort of local port forwarding
	// endpoint for private HCPs
	BackendKASURL string `json:"backendKASURL,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SessionList is a list of Session resources
type SessionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Session `json:"items"`
}
