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
// NodeMitigationBudget records eviction attempts in one namespaced ledger.
// It must not have a Node owner reference.
type NodeMitigationBudget struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Status            NodeMitigationBudgetStatus `json:"status,omitempty"`
}

type NodeMitigationBudgetStatus struct {
	Version        int             `json:"version"`
	EvictionWindow metav1.Duration `json:"evictionWindow"`
	// +kubebuilder:validation:MaxProperties=1024
	Evictions map[string]EvictionRecord `json:"evictions,omitempty"`
}

// EvictionRecord accounts for an attempt, including an unknown API outcome.
// It contains no Pod status or replacement relationship.
type EvictionRecord struct {
	WorkloadUID types.UID   `json:"workloadUID"`
	NodeUID     types.UID   `json:"nodeUID"`
	PodUID      types.UID   `json:"podUID"`
	AttemptedAt metav1.Time `json:"attemptedAt"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type NodeMitigationBudgetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NodeMitigationBudget `json:"items"`
}
