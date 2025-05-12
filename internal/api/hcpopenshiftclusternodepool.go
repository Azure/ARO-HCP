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
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// HCPOpenShiftClusterNodePool represents a node pool resource for ARO HCP
// OpenShift clusters.
type HCPOpenShiftClusterNodePool struct {
	arm.TrackedResource
	Properties HCPOpenShiftClusterNodePoolProperties `json:"properties,omitempty" validate:"required_for_put"`
}

// HCPOpenShiftClusterNodePoolProperties represents the property bag of a
// HCPOpenShiftClusterNodePool resource.
type HCPOpenShiftClusterNodePoolProperties struct {
	ProvisioningState arm.ProvisioningState   `json:"provisioningState,omitempty" visibility:"read"`
	Version           NodePoolVersionProfile  `json:"version,omitempty"`
	Platform          NodePoolPlatformProfile `json:"platform,omitempty"          visibility:"read create"`
	Replicas          int32                   `json:"replicas,omitempty"          visibility:"read create update" validate:"min=0,excluded_with=AutoScaling"`
	AutoRepair        bool                    `json:"autoRepair,omitempty"        visibility:"read create"`
	AutoScaling       *NodePoolAutoScaling    `json:"autoScaling,omitempty"       visibility:"read create update"`
	Labels            map[string]string       `json:"labels,omitempty"            visibility:"read create update" validate:"dive,keys,k8s_qualified_name,endkeys,k8s_label_value"`
	Taints            []Taint                 `json:"taints,omitempty"            visibility:"read create update" validate:"dive"`
}

// NodePoolVersionProfile represents the worker node pool version.
type NodePoolVersionProfile struct {
	ID                string   `json:"id,omitempty"                visibility:"read create update" validate:"required_unless=ChannelGroup stable,omitempty,openshift_version"`
	ChannelGroup      string   `json:"channelGroup,omitempty"      visibility:"read create update"`
	AvailableUpgrades []string `json:"availableUpgrades,omitempty" visibility:"read"`
}

// NodePoolPlatformProfile represents a worker node pool configuration.
// Visibility for the entire struct is "read create".
type NodePoolPlatformProfile struct {
	SubnetID               string                 `json:"subnetId,omitempty"               validate:"omitempty,resource_id=Microsoft.Network/virtualNetworks/subnets"`
	VMSize                 string                 `json:"vmSize,omitempty"                 validate:"required_for_put"`
	DiskSizeGiB            int32                  `json:"diskSizeGiB,omitempty"            validate:"min=1"`
	DiskStorageAccountType DiskStorageAccountType `json:"diskStorageAccountType,omitempty" validate:"omitempty,enum_diskstorageaccounttype"`
	AvailabilityZone       string                 `json:"availabilityZone,omitempty"`
}

// NodePoolAutoScaling represents a node pool autoscaling configuration.
// Visibility for the entire struct is "read create update".
type NodePoolAutoScaling struct {
	Min int32 `json:"min,omitempty" validate:"min=1"`
	Max int32 `json:"max,omitempty" validate:"gtefield=Min"`
}

// Taint represents a Kubernetes taint for a node.
// Visibility for the entire struct is "read create update".
type Taint struct {
	Effect Effect `json:"effect,omitempty" validate:"required_for_put,enum_effect"`
	Key    string `json:"key,omitempty"    validate:"required_for_put,k8s_qualified_name"`
	Value  string `json:"value,omitempty"  validate:"k8s_label_value"`
}

func NewDefaultHCPOpenShiftClusterNodePool() *HCPOpenShiftClusterNodePool {
	return &HCPOpenShiftClusterNodePool{
		Properties: HCPOpenShiftClusterNodePoolProperties{
			Version: NodePoolVersionProfile{
				ChannelGroup: "stable",
			},
			Platform: NodePoolPlatformProfile{
				DiskSizeGiB:            64,
				DiskStorageAccountType: DiskStorageAccountTypePremium_LRS,
			},
			AutoRepair: true,
		},
	}
}
