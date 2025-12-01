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

	"github.com/google/uuid"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// HCPOpenShiftClusterNodePool represents a node pool resource for ARO HCP
// OpenShift clusters.
type HCPOpenShiftClusterNodePool struct {
	arm.TrackedResource
	Properties                HCPOpenShiftClusterNodePoolProperties                `json:"properties,omitempty" validate:"required"`
	ServiceProviderProperties HCPOpenShiftClusterNodePoolServiceProviderProperties `json:"serviceProviderProperties,omitempty" validate:"required"`
	Identity                  *arm.ManagedServiceIdentity                          `json:"identity,omitempty"   validate:"omitempty"`
}

var _ CosmosPersistable = &HCPOpenShiftClusterNodePool{}

func (o *HCPOpenShiftClusterNodePool) GetCosmosData() CosmosData {
	return CosmosData{
		ID:                o.ID,
		ProvisioningState: o.Properties.ProvisioningState,
		ClusterServiceID:  o.ServiceProviderProperties.ClusterServiceID,
	}
}

func (o *HCPOpenShiftClusterNodePool) SetCosmosDocumentData(cosmosUID uuid.UUID) {
	o.ServiceProviderProperties.CosmosUID = cosmosUID.String()
}

// HCPOpenShiftClusterNodePoolProperties represents the property bag of a
// HCPOpenShiftClusterNodePool resource.
type HCPOpenShiftClusterNodePoolProperties struct {
	ProvisioningState       arm.ProvisioningState   `json:"provisioningState,omitempty"       visibility:"read"`
	Version                 NodePoolVersionProfile  `json:"version,omitempty"`
	Platform                NodePoolPlatformProfile `json:"platform,omitempty"                visibility:"read create"`
	Replicas                int32                   `json:"replicas,omitempty"                visibility:"read create update" validate:"min=0,max_if_no_az=200,excluded_with=AutoScaling"`
	AutoRepair              bool                    `json:"autoRepair,omitempty"              visibility:"read create"`
	AutoScaling             *NodePoolAutoScaling    `json:"autoScaling,omitempty"             visibility:"read create update"`
	Labels                  map[string]string       `json:"labels,omitempty"                  visibility:"read create update" validate:"dive,keys,k8s_qualified_name,endkeys,k8s_label_value"`
	Taints                  []Taint                 `json:"taints,omitempty"                  visibility:"read create update" validate:"dive"`
	NodeDrainTimeoutMinutes *int32                  `json:"nodeDrainTimeoutMinutes,omitempty" visibility:"read create update"`
}

type HCPOpenShiftClusterNodePoolServiceProviderProperties struct {
	CosmosUID        string     `json:"cosmosUID,omitempty"`
	ClusterServiceID InternalID `json:"clusterServiceID,omitempty"                visibility:"read"`
}

// NodePoolVersionProfile represents the worker node pool version.
// Visbility for the entire struct is "read create update".
type NodePoolVersionProfile struct {
	ID           string `json:"id,omitempty"           validate:"required_unless=ChannelGroup stable,omitempty,openshift_version"`
	ChannelGroup string `json:"channelGroup,omitempty"`
}

// NodePoolPlatformProfile represents a worker node pool configuration.
// Visibility for the entire struct is "read create".
type NodePoolPlatformProfile struct {
	SubnetID               string        `json:"subnetId,omitempty"         validate:"omitempty,resource_id=Microsoft.Network/virtualNetworks/subnets"`
	VMSize                 string        `json:"vmSize,omitempty"           validate:"required"`
	EnableEncryptionAtHost bool          `json:"enableEncryptionAtHost"`
	OSDisk                 OSDiskProfile `json:"osDisk"`
	AvailabilityZone       string        `json:"availabilityZone,omitempty"`
}

// OSDiskProfile represents a OS Disk configuration.
// Visibility for the entire struct is "read create".
type OSDiskProfile struct {
	SizeGiB                int32                  `json:"sizeGiB,omitempty"                validate:"min=1"`
	DiskStorageAccountType DiskStorageAccountType `json:"diskStorageAccountType,omitempty" validate:"enum_diskstorageaccounttype"`
	EncryptionSetID        string                 `json:"encryptionSetId,omitempty"        validate:"omitempty,resource_id=Microsoft.Compute/diskEncryptionSets"`
}

// NodePoolAutoScaling represents a node pool autoscaling configuration.
// Visibility for the entire struct is "read create update".
// max=200 for both Min and Max when the node pool's Platform.AvailabilityZone is unset.
type NodePoolAutoScaling struct {
	Min int32 `json:"min,omitempty" validate:"min=0,max_if_no_az=200"`
	Max int32 `json:"max,omitempty" validate:"gtefield=Min,max_if_no_az=200"`
}

// Taint represents a Kubernetes taint for a node.
// Visibility for the entire struct is "read create update".
type Taint struct {
	Effect Effect `json:"effect,omitempty" validate:"required,enum_effect"`
	Key    string `json:"key,omitempty"    validate:"required,k8s_qualified_name"`
	Value  string `json:"value,omitempty"  validate:"k8s_label_value"`
}

func NewDefaultHCPOpenShiftClusterNodePool(resourceID *azcorearm.ResourceID) *HCPOpenShiftClusterNodePool {
	return &HCPOpenShiftClusterNodePool{
		TrackedResource: arm.NewTrackedResource(resourceID),
		Properties: HCPOpenShiftClusterNodePoolProperties{
			Version: NodePoolVersionProfile{
				ChannelGroup: "stable",
			},
			Platform: NodePoolPlatformProfile{
				OSDisk: OSDiskProfile{
					SizeGiB:                64,
					DiskStorageAccountType: DiskStorageAccountTypePremium_LRS,
				},
			},
			AutoRepair: true,
		},
	}
}

func (nodePool *HCPOpenShiftClusterNodePool) validateVersion(cluster *HCPOpenShiftCluster) []arm.CloudErrorBody {
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

func (nodePool *HCPOpenShiftClusterNodePool) validateSubnetID(cluster *HCPOpenShiftCluster) []arm.CloudErrorBody {
	var errorDetails []arm.CloudErrorBody

	if nodePool.Properties.Platform.SubnetID == "" {
		return nil
	}

	// Cluster and node pool subnet IDs have already passed syntax validation so
	// parsing should not fail. If parsing does somehow fail then skip the validation.

	clusterSubnetResourceID, err := azcorearm.ParseResourceID(cluster.CustomerProperties.Platform.SubnetID)
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

func (nodePool *HCPOpenShiftClusterNodePool) Validate(cluster *HCPOpenShiftCluster) []arm.CloudErrorBody {
	var errorDetails []arm.CloudErrorBody

	if cluster != nil {
		errorDetails = append(errorDetails, nodePool.validateVersion(cluster)...)
		errorDetails = append(errorDetails, nodePool.validateSubnetID(cluster)...)
	}

	return errorDetails
}
