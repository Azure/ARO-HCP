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

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// ManagementClusterContent represents K8s resources in the Management Cluster
type ManagementClusterContent struct {
	// CosmosMetadata ResourceID is nested under the cluster so that association and cleanup work as expected
	CosmosMetadata `json:"cosmosMetadata"`

	// resourceID exists to match cosmosMetadata.resourceID until we're able to transition all types to use cosmosMetadata,
	// at which point we will stop using properties.resourceId in our queries. That will be about a month from now.
	ResourceID azcorearm.ResourceID `json:"resourceId"`

	// KubeContent contains a list of K8s resources that represent content of the Management Cluster associated to this
	// resource ID.
	KubeContent *metav1.List `json:"kubeContent"`

	// Status is a list of conditions that track the status of the Management Cluster Content.
	Status ManagementClusterContentStatus `json:"status,omitempty"`
}

type ManagementClusterContentStatus struct {
	// Conditions is a list of conditions that track the status of the Management Cluster Content.
	// Each Condition Type represents a condition and it should be unique among all conditions.
	// A Condition Status of True means that the condition is met, and a Condition Status of False means that the condition is not met.
	// The Condition Reason and Message are used to provide more details about the condition status.
	// The Condition LastTransitionTime is used to track the last time the condition transitioned from one status to another.
	Conditions []Condition `json:"conditions,omitempty"`
}
