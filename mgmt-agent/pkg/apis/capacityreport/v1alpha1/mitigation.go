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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// +kubebuilder:metadata:annotations=helm.sh/resource-policy=keep
// MitigationEpisode retains ownership and intent independently of Node lifetime.
type MitigationEpisode struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              MitigationEpisodeSpec   `json:"spec"`
	Status            MitigationEpisodeStatus `json:"status,omitempty"`
}

type MitigationEpisodeSpec struct {
	NodeName   string    `json:"nodeName"`
	NodeUID    types.UID `json:"nodeUID"`
	ProviderID string    `json:"providerID"`
	InstanceID string    `json:"instanceID"`
	PoolID     string    `json:"poolID"`
	Zone       string    `json:"zone"`
	Detector   string    `json:"detector"`
	Mitigator  string    `json:"mitigator"`
	// +kubebuilder:validation:MaxLength=65536
	Policy string `json:"policy"`
	// +kubebuilder:validation:MaxItems=1024
	PodUIDs []types.UID `json:"podUIDs,omitempty"`
}

type MitigationEpisodeStatus struct {
	Phase              string            `json:"phase,omitempty"`
	CurrentNodeUID     types.UID         `json:"currentNodeUID,omitempty"`
	Intent             *MitigationAction `json:"intent,omitempty"`
	NodeDeletionAction *MitigationAction `json:"nodeDeletionAction,omitempty"`
	Recovery           *WorkloadRecovery `json:"recovery,omitempty"`
	NodeDeletedAt      *metav1.Time      `json:"nodeDeletedAt,omitempty"`
	CompletedAt        *metav1.Time      `json:"completedAt,omitempty"`
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// MitigationAction is saved before making an identity-preconditioned API call.
type MitigationAction struct {
	ID                 string       `json:"id"`
	Kind               string       `json:"kind"`
	Phase              string       `json:"phase"`
	Namespace          string       `json:"namespace,omitempty"`
	Name               string       `json:"name"`
	UID                types.UID    `json:"uid"`
	ResourceVersion    string       `json:"resourceVersion"`
	StartedAt          metav1.Time  `json:"startedAt"`
	LastAttemptAt      *metav1.Time `json:"lastAttemptAt,omitempty"`
	PDBName            string       `json:"pdbName,omitempty"`
	PDBUID             types.UID    `json:"pdbUID,omitempty"`
	PDBResourceVersion string       `json:"pdbResourceVersion,omitempty"`
}

type WorkloadRecovery struct {
	StartedAt metav1.Time `json:"startedAt"`
	// +kubebuilder:validation:MaxItems=1024
	ExistingPodUIDs []types.UID `json:"existingPodUIDs,omitempty"`
	Namespace       string      `json:"namespace"`
	Deployment      string      `json:"deployment"`
	DeploymentUID   types.UID   `json:"deploymentUID"`
	PodUID          types.UID   `json:"podUID"`
	Replicas        int32       `json:"replicas"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type MitigationEpisodeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MitigationEpisode `json:"items"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// +kubebuilder:metadata:annotations=helm.sh/resource-policy=keep
// NodeMitigationBudget atomically reserves cluster, pool and zone allowance in
// one namespaced ledger. It must not have a Node owner reference.
type NodeMitigationBudget struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Status            NodeMitigationBudgetStatus `json:"status,omitempty"`
}

type NodeMitigationBudgetStatus struct {
	Window metav1.Duration `json:"window"`
	// +kubebuilder:validation:MaxProperties=128
	Pools map[string]PoolBaseline `json:"pools,omitempty"`
	// +kubebuilder:validation:MaxProperties=1024
	Reservations map[string]MitigationReservation `json:"reservations,omitempty"`
}

type PoolBaseline struct {
	Size        int32        `json:"size"`
	Target      int32        `json:"target"`
	ObservedAt  metav1.Time  `json:"observedAt"`
	StableSince *metav1.Time `json:"stableSince,omitempty"`
	Observer    string       `json:"observer"`
}

type MitigationReservation struct {
	EpisodeUID      types.UID    `json:"episodeUID"`
	NodeUID         types.UID    `json:"nodeUID"`
	InstanceID      string       `json:"instanceID"`
	PoolID          string       `json:"poolID"`
	Zone            string       `json:"zone"`
	ReservedAt      metav1.Time  `json:"reservedAt"`
	DeleteStartedAt *metav1.Time `json:"deleteStartedAt,omitempty"`
	ReleasedAt      *metav1.Time `json:"releasedAt,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type NodeMitigationBudgetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NodeMitigationBudget `json:"items"`
}
