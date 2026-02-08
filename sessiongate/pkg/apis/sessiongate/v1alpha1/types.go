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

// Package v1alpha1 contains API types for the sessiongate API group.
// The Session resource manages authenticated SRE access sessions to Hypershift Hosted Control Planes (HCPs),
// enabling time-limited, scoped access for debugging and administrative operations.
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Session represents a time-limited, authenticated SRE access session to a Hypershift Hosted Control Plane (HCP).
// Sessions are created to grant temporary access to an HCP's Kubernetes API server for debugging,
// administrative operations, or support purposes. Each session is bound to a specific owner
// (identified by JWT claims), targets a specific HCP on a management cluster, and expires
// after the configured TTL. The controller provisions the necessary resources (credentials, and
// proxy endpoint) to enable secure access.
type Session struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// +required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec is immutable"

	// spec defines the desired state of the Session, including the target HCP,
	// access permissions, TTL, and owner identity.
	//
	// This field is immutable after creation to prevent privilege escalation or
	// session hijacking. Once a session is created, its target HCP, access level, and owner
	// cannot be modified. To change any session parameters, delete the session and create
	// a new one.
	Spec SessionSpec `json:"spec"`

	// +optional
	// +kubebuilder:validation:XValidation:rule="!has(oldSelf.expiresAt) || (has(self.expiresAt) && self.expiresAt == oldSelf.expiresAt)",message="expiresAt is immutable once set"
	// status contains the observed state of the Session, including provisioned resources,
	// the session endpoint URL, expiration time, and condition status.
	Status SessionStatus `json:"status,omitempty,omitzero"`
}

// SessionSpec defines the desired state of a Session. All fields are immutable after creation
// to ensure that all interactions remain auditable and to prevent privilege escalation.
// The spec identifies the target HCP, the management cluster hosting it, the access level
// granted, and the owner's identity.
type SessionSpec struct {
	// +kubebuilder:validation:Required
	// ttl is the time-to-live duration for the session. The session will automatically
	// expire after this duration from its creation time. The expiration timestamp is
	// recorded in status.expiresAt. Once expired, the session's provisioned resources
	// are cleaned up by the controller.
	TTL metav1.Duration `json:"ttl"`

	// +kubebuilder:validation:Required
	// managementCluster identifies the AKS management cluster where the target HCP is running.
	// The controller uses this to locate and communicate with the correct management cluster
	// when provisioning session resources.
	ManagementCluster ManagementCluster `json:"managementCluster"`

	// +kubebuilder:validation:Required
	// hostedControlPlane identifies the Hypershift Hosted Control Plane that this session
	// provides access to.
	HostedControlPlane HostedControlPlane `json:"hostedControlPlane"`

	// +kubebuilder:validation:Required
	// accessLevel defines the RBAC permissions granted to the session owner when accessing
	// the target HCP. This determines what operations the session owner can perform on the
	// HCP's Kubernetes API server.
	AccessLevel AccessLevel `json:"accessLevel"`

	// +kubebuilder:validation:Required
	// owner identifies the authenticated principal (user or service principal) that is authorized
	// to use this session. The owner's identity is cryptographically verified by matching JWT
	// claims against tokens presented during authentication.
	//
	// Security: The claims specified here form the sole basis for authentication. Only the entity
	// whose JWT token contains ALL matching claims can access the HCP through this session.
	// The identityName field is informational only and is NOT used for authentication decisions.
	Owner Principal `json:"owner"`
}

// ManagementCluster identifies an Azure Kubernetes Service (AKS) management cluster that hosts
// one or more Hypershift Hosted Control Planes. The management cluster runs the HCP control plane
// components and is the target for session resource provisioning.
type ManagementCluster struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^/subscriptions/[a-fA-F0-9-]+/resourceGroups/[^/]+/providers/[Mm]icrosoft\.[Cc]ontainer[Ss]ervice/[Mm]anaged[Cc]lusters/[^/]+$`
	// resourceId is the fully Azure Resource Manager (ARM) resource ID of the management
	// cluster. This ID uniquely identifies the AKS cluster within the Azure subscription.
	ResourceID string `json:"resourceId"`
}

// HostedControlPlane identifies a Hypershift Hosted Control Plane running on a management cluster.
// The HCP represents a customer's OpenShift control plane whose Kubernetes API server the session
// will provide access to.
type HostedControlPlane struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^/subscriptions/[a-fA-F0-9-]+/resourceGroups/[^/]+/providers/[Mm]icrosoft\.[Rr]ed[Hh]at[Oo]penshift/[Hh]cp[Oo]pen[Ss]hift[Cc]lusters/[^/]+$`
	// resourceId is the qualified Azure Resource Manager (ARM) resource ID of the hosted
	// cluster resource. This ID uniquely identifies the ARO-HCP cluster within the Azure subscription.
	ResourceID string `json:"resourceId"`

	// +kubebuilder:validation:Required
	// namespace is the Kubernetes namespace on the management cluster where the HostedControlPlane
	// custom resource is located. This namespace contains the HCP's control plane components
	// and is used by the controller to locate the target HCP resources.
	Namespace string `json:"namespace"`
}

// AccessLevel defines the RBAC permissions that the session owner is granted when accessing
// the target Hosted Control Plane. The permissions are defined by referencing a Kubernetes
// Group, which maps to a set of ClusterRoles or Roles via RoleBindings on the target HCP.
type AccessLevel struct {
	// +kubebuilder:validation:Required

	// group is the name of the Kubernetes Group (rbac.authorization.k8s.io) whose permissions
	// the session owner will inherit when accessing the HCP. The group must exist on the
	// target HCP and have appropriate RoleBindings or ClusterRoleBindings configured.
	Group string `json:"group"`
}

// PrincipalType specifies the type of Azure identity that owns a session.
// +kubebuilder:validation:Enum=azureUser;azureServicePrincipal
type PrincipalType string

const (
	// IdentityTypeAzureUser represents an Azure AD user identity (e.g., an SRE accessing the system).
	PrincipalTypeAzureUser PrincipalType = "azureUser"

	// IdentityTypeAzureServicePrincipal represents an Azure service principal identity (e.g., automation accounts).
	PrincipalTypeAzureServicePrincipal PrincipalType = "azureServicePrincipal"
)

// Principal identifies the authenticated Azure identity that owns this session.
// based on the identity type and name.
type Principal struct {
	// +kubebuilder:validation:Required
	// type specifies the type of Azure identity that owns this session.
	// Valid values are:
	// - "azureUser": An Azure AD user identity (e.g., an SRE accessing the system)
	// - "azureServicePrincipal": An Azure service principal identity (e.g., automation accounts)
	Type PrincipalType `json:"type"`

	// +kubebuilder:validation:Required
	// name is the identifier of the Azure identity that owns this session, e.g.
	// for azureUser: the user's email address or UPN (e.g., "alice@example.com")
	// for azureServicePrincipal: the service principal's client ID / application ID
	Name string `json:"name"`
}

// SessionStatus reports the observed state of the Session, including the provisioned resources,
// the session endpoint URL for accessing the HCP, and the session's expiration time. The status
// is updated by the controller as it provisions and manages session resources.
type SessionStatus struct {
	// +optional
	// +listType=map
	// +listMapKey=type
	// +patchMergeKey=type
	// +patchStrategy=merge
	// conditions represent the current state of the session. Known condition types are:
	// - "Ready": True when the session is fully provisioned and accessible
	// - "CredentialsAvailable": True when session credentials have been created
	// - "NetworkPathAvailable": True when the network path to the HCP is established
	// The status of each condition is one of True, False, or Unknown.
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// +optional
	// expiresAt is the timestamp when the session will expire and become invalid.
	// This is calculated as the session's creation timestamp plus the TTL specified in spec.ttl.
	// After this time, the controller will clean up session resources and the session
	// endpoint will no longer accept connections. This field is immutable once set.
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`

	// +optional
	// endpoint is the URL that the session owner uses to access the HCP's Kubernetes
	// API server. This endpoint acts as an authenticated proxy, forwarding requests to the
	// target HCP's KAS after validating the session owner's identity. The endpoint is
	// provisioned by the controller and becomes available when the session is ready.
	Endpoint string `json:"endpoint,omitempty"`

	// +optional
	// credentialsSecretRef is a reference to the Kubernetes Secret containing the session's
	// authentication credentials. These credentials are used by the session proxy to
	// authenticate requests and by clients to establish secure connections.
	CredentialsSecretRef string `json:"credentialsSecretRef,omitempty"`

	// +optional
	// backendKASURL is the internal URL used by the session proxy to communicate with the
	// target HCP's Kubernetes API server. For public HCPs, this is the HCP's public KAS
	// endpoint. For private HCPs that are not directly accessible, this may be a local
	// port-forward endpoint or internal service URL. This field is set by the controller
	// based on the HCP's network configuration.
	BackendKASURL string `json:"backendKASURL,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SessionList is a list of Session resources
type SessionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Session `json:"items"`
}
