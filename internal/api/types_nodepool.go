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
	"strings"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// NodePool represents a node pool resource for ARO HCP
// OpenShift clusters.
type NodePool struct {
	arm.TrackedResource
	Properties                NodePoolProperties                `json:"properties,omitempty"`
	ServiceProviderProperties NodePoolServiceProviderProperties `json:"serviceProviderProperties,omitempty"`
	Identity                  *arm.ManagedServiceIdentity       `json:"identity,omitempty"`
}

var _ CosmosPersistable = &NodePool{}

func (o *NodePool) GetCosmosData() *CosmosData {
	return &CosmosData{
		ResourceID: o.ID,
	}
}

// NodePoolProperties represents the property bag of a
// NodePool resource.
type NodePoolProperties struct {
	ProvisioningState       arm.ProvisioningState   `json:"provisioningState,omitempty"`
	Version                 NodePoolVersionProfile  `json:"version,omitempty"`
	Platform                NodePoolPlatformProfile `json:"platform,omitempty"`
	Replicas                int32                   `json:"replicas,omitempty"`
	AutoRepair              bool                    `json:"autoRepair,omitempty"`
	AutoScaling             *NodePoolAutoScaling    `json:"autoScaling,omitempty"`
	Labels                  map[string]string       `json:"labels,omitempty"`
	Taints                  []Taint                 `json:"taints,omitempty"`
	NodeDrainTimeoutMinutes *int32                  `json:"nodeDrainTimeoutMinutes,omitempty"`
}

type NodePoolServiceProviderProperties struct {
	ClusterServiceID  InternalID `json:"clusterServiceID,omitempty"`
	ActiveOperationID string     `json:"activeOperationId,omitempty"`
}

// NodePoolVersionProfile represents the worker node pool version.
// Visbility for the entire struct is "read create update".
type NodePoolVersionProfile struct {
	ID           string `json:"id,omitempty"`
	ChannelGroup string `json:"channelGroup,omitempty"`
}

// NodePoolPlatformProfile represents a worker node pool configuration.
// Visibility for the entire struct is "read create".
type NodePoolPlatformProfile struct {
	SubnetID               *azcorearm.ResourceID `json:"subnetId,omitempty"`
	VMSize                 string                `json:"vmSize,omitempty"`
	EnableEncryptionAtHost bool                  `json:"enableEncryptionAtHost"`
	OSDisk                 OSDiskProfile         `json:"osDisk"`
	AvailabilityZone       string                `json:"availabilityZone,omitempty"`
}

// OSDiskProfile represents a OS Disk configuration.
// Visibility for the entire struct is "read create".
type OSDiskProfile struct {
	SizeGiB                *int32                 `json:"sizeGiB,omitempty"`
	DiskStorageAccountType DiskStorageAccountType `json:"diskStorageAccountType,omitempty"`
	EncryptionSetID        *azcorearm.ResourceID  `json:"encryptionSetId,omitempty"`
}

// NodePoolAutoScaling represents a node pool autoscaling configuration.
// Visibility for the entire struct is "read create update".
// max=200 for both Min and Max when the node pool's Platform.AvailabilityZone is unset.
type NodePoolAutoScaling struct {
	Min int32 `json:"min,omitempty"`
	Max int32 `json:"max,omitempty"`
}

// Taint represents a Kubernetes taint for a node.
// Visibility for the entire struct is "read create update".
type Taint struct {
	Effect Effect `json:"effect,omitempty"`
	Key    string `json:"key,omitempty"`
	Value  string `json:"value,omitempty"`
}

func NewDefaultHCPOpenShiftClusterNodePool(resourceID *azcorearm.ResourceID, azureLocation string) *NodePool {
	return &NodePool{
		TrackedResource: arm.NewTrackedResource(resourceID, azureLocation),
		Properties: NodePoolProperties{
			Version: NodePoolVersionProfile{
				ChannelGroup: "stable",
			},
			Platform: NodePoolPlatformProfile{
				OSDisk: OSDiskProfile{
					SizeGiB:                ptr.To[int32](64),
					DiskStorageAccountType: DiskStorageAccountTypePremium_LRS,
				},
			},
			AutoRepair: true,
		},
	}
}

func (nodePool *NodePool) validateVersion(cluster *Cluster) []arm.CloudErrorBody {
	var errorDetails []arm.CloudErrorBody

	if nodePool.Properties.Version.ChannelGroup != cluster.CustomerProperties.Version.ChannelGroup {
		errorDetails = append(errorDetails, arm.CloudErrorBody{
			Code: arm.CloudErrorCodeInvalidRequestContent,
			Message: fmt.Sprintf(
				"Node pool channel group '%s' must be the same as control plane channel group '%s'",
				nodePool.Properties.Version.ChannelGroup,
				cluster.CustomerProperties.Version.ChannelGroup),
			Target: "properties.version.channelGroup",
		})
	}

	return errorDetails
}

func (nodePool *NodePool) validateSubnetID(cluster *Cluster) []arm.CloudErrorBody {
	var errorDetails []arm.CloudErrorBody

	if nodePool.Properties.Platform.SubnetID == nil {
		return nil
	}

	// Cluster and node pool subnet IDs have already passed syntax validation so
	// parsing should not fail. If parsing does somehow fail then skip the validation.

	clusterVNet := cluster.CustomerProperties.Platform.SubnetID.Parent.String()
	nodePoolVNet := nodePool.Properties.Platform.SubnetID.Parent.String()

	if !strings.EqualFold(nodePoolVNet, clusterVNet) {
		errorDetails = append(errorDetails, arm.CloudErrorBody{
			Code:    arm.CloudErrorCodeInvalidRequestContent,
			Message: fmt.Sprintf("Subnet '%s' must belong to the same VNet as the parent cluster VNet '%s'", nodePool.Properties.Platform.SubnetID, clusterVNet),
			Target:  "properties.platform.subnetId",
		})
	}

	return errorDetails
}

func (nodePool *NodePool) Validate(cluster *Cluster) []arm.CloudErrorBody {
	var errorDetails []arm.CloudErrorBody

	if cluster != nil {
		errorDetails = append(errorDetails, nodePool.validateVersion(cluster)...)
		errorDetails = append(errorDetails, nodePool.validateSubnetID(cluster)...)
	}

	return errorDetails
}
