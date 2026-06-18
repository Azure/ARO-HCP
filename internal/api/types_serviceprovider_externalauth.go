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

const (
	// ServiceProviderExternalAuthResourceName is the name of the ServiceProviderExternalAuth resource.
	// ServiceProviderExternalAuth is a singleton resource and ARM convention is to
	// use the name "default" for singleton resources.
	ServiceProviderExternalAuthResourceName = "default"
)

// ServiceProviderExternalAuth is used internally by controllers to track and pass information between them.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ServiceProviderExternalAuth struct {
	// CosmosMetadata ResourceID is nested under the external auth so that association and cleanup work as expected
	// it will be the ServiceProviderExternalAuth type and the name default
	CosmosMetadata `json:"cosmosMetadata"`

	// Status contains the observed state of the external auth
	Status ServiceProviderExternalAuthStatus `json:"status,omitempty"`
}

// ServiceProviderExternalAuthStatus contains the observed state of the external auth.
type ServiceProviderExternalAuthStatus struct {
	// Conditions are the top-level ServiceProviderExternalAuthStatus status conditions.
	// Each Condition Type represents a condition and it should be unique among all conditions.
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}
