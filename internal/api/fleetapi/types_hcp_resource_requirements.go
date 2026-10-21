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

package fleetapi

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

const (
	// ConditionTypeDataCurrent indicates whether the HCP resource
	// requirements data is up to date. Set by the
	// HCPResourceRequirementsController after aggregating CapacityReport
	// data across all management clusters.
	ConditionTypeDataCurrent = "DataCurrent"
)

// HCPResourceRequirements is a top-level singleton in the Fleet container
// holding aggregated resource requirement data across all management clusters.
// The resource name "default" represents the fleet-wide average; future
// per-sizing-tier documents (e.g. "small", "medium") use the same type.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type HCPResourceRequirements struct {
	coreapi.CosmosMetadata `json:"cosmosMetadata"`

	// Status contains the observed aggregate resource requirements.
	Status HCPResourceRequirementsStatus `json:"status"`
}

// HCPResourceRequirementsStatus contains the observed aggregate resource
// requirements computed from CapacityReport data across management clusters.
type HCPResourceRequirementsStatus struct {
	// Conditions reports the health of the aggregation process.
	//
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// LastReportedAt is the time the data was last computed.
	//
	// +optional
	LastReportedAt *metav1.Time `json:"lastReportedAt,omitempty"`

	// AverageUsage is the average actual resource consumption per ready HCP,
	// computed from metrics-server PodMetrics across all management clusters.
	//
	// +optional
	AverageUsage corev1.ResourceList `json:"averageUsage,omitempty"`

	// AverageRequests is the average sum of resource requests per ready HCP,
	// computed from pod specs across all management clusters.
	//
	// +optional
	AverageRequests corev1.ResourceList `json:"averageRequests,omitempty"`

	// SampleSize is the total number of ready HCPs across all management
	// clusters that contributed to the averages.
	SampleSize int `json:"sampleSize"`
}
