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
	"fmt"
	"net/http"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	validator "github.com/go-playground/validator/v10"

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
	EnableEncryptionAtHost bool                   `json:"enableEncryptionAtHost"`
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

func (nodePool *HCPOpenShiftClusterNodePool) validateVersion(cluster *HCPOpenShiftCluster) []arm.CloudErrorBody {
	var errorDetails []arm.CloudErrorBody

	if nodePool.Properties.Version.ChannelGroup != cluster.Properties.Version.ChannelGroup {
		errorDetails = append(errorDetails, arm.CloudErrorBody{
			Code: arm.CloudErrorCodeInvalidRequestContent,
			Message: fmt.Sprintf(
				"Node pool channel group '%s' must be the same as control plane channel group '%s'",
				nodePool.Properties.Version.ChannelGroup,
				cluster.Properties.Version.ChannelGroup),
			Target: "properties.version.channelGroup",
		})
	}

	return errorDetails
}

func (nodePool *HCPOpenShiftClusterNodePool) validateSubnetID(cluster *HCPOpenShiftCluster) []arm.CloudErrorBody {
	var errorDetails []arm.CloudErrorBody

	if nodePool.Properties.Platform.SubnetID == "" {
		return nil
	}

	// Cluster and node pool subnet IDs have already passed syntax validation so
	// parsing should not fail. If parsing does somehow fail then skip the validation.

	clusterSubnetResourceID, err := azcorearm.ParseResourceID(cluster.Properties.Platform.SubnetID)
	if err != nil {
		return nil
	}

	nodePoolSubnetResourceID, err := azcorearm.ParseResourceID(nodePool.Properties.Platform.SubnetID)
	if err != nil {
		return nil
	}

	clusterVNet := clusterSubnetResourceID.Parent.String()
	nodePoolVNet := nodePoolSubnetResourceID.Parent.String()

	if !strings.EqualFold(nodePoolVNet, clusterVNet) {
		errorDetails = append(errorDetails, arm.CloudErrorBody{
			Code:    arm.CloudErrorCodeInvalidRequestContent,
			Message: fmt.Sprintf("Subnet '%s' must belong to the same VNet as the parent cluster VNet '%s'", nodePoolSubnetResourceID, clusterVNet),
			Target:  "properties.platform.subnetId",
		})
	}

	return errorDetails
}

func (nodePool *HCPOpenShiftClusterNodePool) Validate(validate *validator.Validate, request *http.Request, cluster *HCPOpenShiftCluster) []arm.CloudErrorBody {
	errorDetails := ValidateRequest(validate, request, nodePool)

	// Proceed with complex, multi-field validation only if single-field
	// validation has passed. This avoids running further checks on data
	// we already know to be invalid and prevents the response body from
	// becoming overwhelming.
	if len(errorDetails) == 0 {
		if cluster != nil {
			errorDetails = append(errorDetails, nodePool.validateVersion(cluster)...)
			errorDetails = append(errorDetails, nodePool.validateSubnetID(cluster)...)
		}
	}

	return errorDetails
}
