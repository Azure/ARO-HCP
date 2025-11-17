/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type Session struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of Session
	// +required
	Spec SessionSpec `json:"spec"`

	// status defines the observed state of Session
	// +optional
	Status SessionStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec is immutable"
type SessionSpec struct {
	// ttl is the time-to-live duration for the session
	// +kubebuilder:validation:Required
	TTL metav1.Duration `json:"ttl"`

	// managementCluster specifies the AKS management cluster
	// +kubebuilder:validation:Required
	ManagementCluster ManagementCluster `json:"managementCluster"`

	// hostedControlPlane specifies the hosted control plane
	// +kubebuilder:validation:Required
	HostedControlPlane HostedControlPlane `json:"hostedControlPlane"`

	// accessLevel defines the access permissions for the session
	// +kubebuilder:validation:Required
	AccessLevel AccessLevel `json:"accessLevel"`

	// owner identifies the principal (user or service account) that owns this session
	// +kubebuilder:validation:Required
	Owner Principal `json:"owner"`
}

// ManagementCluster identifies an Azure management cluster
type ManagementCluster struct {
	// resourceId is the Azure resource ID of the management cluster
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^/subscriptions/[a-fA-F0-9-]+/resourceGroups/[^/]+/providers/[^/]+/[^/]+/[^/]+$`
	ResourceID string `json:"resourceId"`
}

// HostedCluster identifies an hosted cluster
type HostedControlPlane struct {
	// resourceId is the Azure resource ID of the hosted cluster
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^/subscriptions/[a-fA-F0-9-]+/resourceGroups/[^/]+/providers/[^/]+/[^/]+/[^/]+$`
	ResourceID string `json:"resourceId"`

	// namespace of the HostedControlPlane CR
	// +kubebuilder:validation:Required
	Namespace string `json:"namespace,omitempty"`
}

type AccessLevel struct {
	// group is the name of the access group
	// +kubebuilder:validation:Required
	Group string `json:"group"`
}

// PrincipalType defines the type of principal for authentication
// +kubebuilder:validation:Enum=User
type PrincipalType string

const (
	// PrincipalTypeUser represents a human user principal
	PrincipalTypeUser PrincipalType = "User"
)

// Principal identifies the authenticated entity that owns this session
// +kubebuilder:validation:XValidation:rule="self.type == 'User' ? has(self.userPrincipal) : true",message="userPrincipal must be set when type is User"
type Principal struct {
	// type specifies the authentication method
	// +kubebuilder:validation:Required
	Type PrincipalType `json:"type"`

	// userPrincipal identifies the user principal
	// Required when type is User
	// +optional
	UserPrincipal *UserPrincipal `json:"userPrincipal,omitempty"`
}

// UserPrincipal represents a user identity
type UserPrincipal struct {
	// name is the user principal name (e.g., UPN for Azure AD like user@domain.com)
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// claim specifies which JWT claim to use for authentication (e.g., "upn", "email", "sub")
	// +kubebuilder:validation:Required
	// +kubebuilder:default="upn"
	Claim string `json:"claim"`
}

type SessionStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// conditions represent the current state of the Session resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	// - "Credentials": credentials are being provisioned or ready
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// expiresAt is the timestamp when the session will expire
	// +optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`

	// endpoint is the URL endpoint for accessing the session
	// +optional
	Endpoint string `json:"endpoint,omitempty"`

	// credentialsSecretRef references the Secret containing the session credentials
	// +optional
	CredentialsSecretRef string `json:"credentialsSecretRef,omitempty"`

	// backendKASURL is the Kubernetes API server URL for the backend cluster
	// +optional
	BackendKASURL string `json:"backendKASURL,omitempty"`
}

func (s *Session) IsReady() bool {
	c := sessionConditionSet.Manage(s).GetReadyCondition()
	return c != nil && c.Status == metav1.ConditionTrue
}

func (s *Session) InitializeConditions() {
	sessionConditionSet.Manage(s).InitializeConditions()
}

func (s *Session) MarkSessionActive() {
	sessionConditionSet.Manage(s).MarkTrue(ConditionTypeSessionActive, ReasonAsExpected, "Session is active")
}

func (s *Session) MarkSessionInactive(reason, message string) {
	sessionConditionSet.Manage(s).MarkFalse(ConditionTypeSessionActive, reason, message)
}

func (s *Session) MarkCredentialsNotReady(reason, message string) {
	sessionConditionSet.Manage(s).MarkFalse(ConditionTypeCredentialsAvailable, reason, message)
}

func (s *Session) MarkCredentialsReady() {
	sessionConditionSet.Manage(s).MarkTrue(ConditionTypeCredentialsAvailable, ReasonAsExpected, "Credentials Secret exists")
}

func (s *Session) MarkAuthorizationPolicyNotReady(reason, message string) {
	sessionConditionSet.Manage(s).MarkFalse(ConditionTypeAuthorizationPolicyAvailable, reason, message)
}

func (s *Session) MarkAuthorizationPolicyReady() {
	sessionConditionSet.Manage(s).MarkTrue(ConditionTypeAuthorizationPolicyAvailable, ReasonAsExpected, "Authorization policy exists")
}

func (s *Session) MarkNetworkPathNotReady(reason, message string) {
	sessionConditionSet.Manage(s).MarkFalse(ConditionTypeNetworkPathAvailable, reason, message)
}

func (s *Session) MarkNetworkPathReady() {
	sessionConditionSet.Manage(s).MarkTrue(ConditionTypeNetworkPathAvailable, ReasonAsExpected, "Network path exists")
}

func (s *Session) Progressing(reason, message string) {
	sessionConditionSet.Manage(s).MarkTrue(ConditionTypeProgressing, reason, message)
}

func (s *Session) StopProgressing(reason, message string) {
	sessionConditionSet.Manage(s).MarkFalse(ConditionTypeProgressing, reason, message)
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SessionList is a list of Session resources
type SessionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Session `json:"items"`
}
