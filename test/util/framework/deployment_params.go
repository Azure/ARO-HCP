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

package framework

import (
	"encoding/json"
	"fmt"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

type ClusterParams struct {
	OpenshiftVersionId            string
	ClusterName                   string
	ManagedResourceGroupName      string
	NsgResourceID                 string
	SubnetResourceID              string
	VnetName                      string
	UserAssignedIdentitiesProfile *hcpsdk20240610preview.UserAssignedIdentitiesProfile
	Identity                      *hcpsdk20240610preview.ManagedServiceIdentity
	KeyVaultName                  string
	EtcdEncryptionKeyName         string
	EtcdEncryptionKeyVersion      string
	EncryptionKeyManagementMode   string
	EncryptionType                string
	Network                       NetworkConfig
	APIVisibility                 string
	ImageRegistryState            string
	ChannelGroup                  string
}

type NetworkConfig struct {
	NetworkType string
	PodCIDR     string
	ServiceCIDR string
	MachineCIDR string
	HostPrefix  int32
}

type ClusterParamsBuilder struct {
	params ClusterParams
}

func NewClusterParams() *ClusterParamsBuilder {
	return &ClusterParamsBuilder{
		params: ClusterParams{
			OpenshiftVersionId: "4.19",
			Network: NetworkConfig{
				NetworkType: "OVNKubernetes",
				PodCIDR:     "10.128.0.0/14",
				ServiceCIDR: "172.30.0.0/16",
				MachineCIDR: "10.0.0.0/16",
				HostPrefix:  23,
			},
			EncryptionKeyManagementMode: "CustomerManaged",
			EncryptionType:              "KMS",
			APIVisibility:               "Public",
			ImageRegistryState:          "Enabled",
			ChannelGroup:                "stable",
		},
	}
}

func (b *ClusterParamsBuilder) OpenshiftVersionId(version string) *ClusterParamsBuilder {
	b.params.OpenshiftVersionId = version
	return b
}
func (b *ClusterParamsBuilder) ClusterName(name string) *ClusterParamsBuilder {
	b.params.ClusterName = name
	return b
}
func (b *ClusterParamsBuilder) ManagedResourceGroupName(name string) *ClusterParamsBuilder {
	b.params.ManagedResourceGroupName = name
	return b
}
func (b *ClusterParamsBuilder) NsgResourceID(id string) *ClusterParamsBuilder {
	b.params.NsgResourceID = id
	return b
}
func (b *ClusterParamsBuilder) SubnetResourceID(id string) *ClusterParamsBuilder {
	b.params.SubnetResourceID = id
	return b
}
func (b *ClusterParamsBuilder) VnetName(name string) *ClusterParamsBuilder {
	b.params.VnetName = name
	return b
}
func (b *ClusterParamsBuilder) UserAssignedIdentitiesProfile(profile *hcpsdk20240610preview.UserAssignedIdentitiesProfile) *ClusterParamsBuilder {
	b.params.UserAssignedIdentitiesProfile = profile
	return b
}
func (b *ClusterParamsBuilder) Identity(identity *hcpsdk20240610preview.ManagedServiceIdentity) *ClusterParamsBuilder {
	b.params.Identity = identity
	return b
}
func (b *ClusterParamsBuilder) KeyVaultName(name string) *ClusterParamsBuilder {
	b.params.KeyVaultName = name
	return b
}
func (b *ClusterParamsBuilder) EtcdEncryptionKeyName(name string) *ClusterParamsBuilder {
	b.params.EtcdEncryptionKeyName = name
	return b
}
func (b *ClusterParamsBuilder) EtcdEncryptionKeyVersion(version string) *ClusterParamsBuilder {
	b.params.EtcdEncryptionKeyVersion = version
	return b
}
func (b *ClusterParamsBuilder) EncryptionKeyManagementMode(mode string) *ClusterParamsBuilder {
	b.params.EncryptionKeyManagementMode = mode
	return b
}
func (b *ClusterParamsBuilder) EncryptionType(encType string) *ClusterParamsBuilder {
	b.params.EncryptionType = encType
	return b
}
func (b *ClusterParamsBuilder) Network(config NetworkConfig) *ClusterParamsBuilder {
	b.params.Network = config
	return b
}
func (b *ClusterParamsBuilder) APIVisibility(visibility string) *ClusterParamsBuilder {
	b.params.APIVisibility = visibility
	return b
}
func (b *ClusterParamsBuilder) ImageRegistryState(state string) *ClusterParamsBuilder {
	b.params.ImageRegistryState = state
	return b
}
func (b *ClusterParamsBuilder) ChannelGroup(group string) *ClusterParamsBuilder {
	b.params.ChannelGroup = group
	return b
}
func (b *ClusterParamsBuilder) Build() ClusterParams {
	return b.params
}

type NodePoolParams struct {
	OpenshiftVersionId     string
	ClusterName            string
	NodePoolName           string
	Replicas               int32
	VMSize                 string
	OSDiskSizeGiB          int32
	DiskStorageAccountType string
	ChannelGroup           string
}

type NodePoolParamsBuilder struct {
	params NodePoolParams
}

func NewNodePoolParams() *NodePoolParamsBuilder {
	return &NodePoolParamsBuilder{
		params: NodePoolParams{
			OpenshiftVersionId:     "4.19.7",
			Replicas:               int32(2),
			VMSize:                 "Standard_D8s_v3",
			OSDiskSizeGiB:          int32(64),
			DiskStorageAccountType: "StandardSSD_LRS",
			ChannelGroup:           "stable",
		},
	}
}
func (b *NodePoolParamsBuilder) OpenshiftVersionId(version string) *NodePoolParamsBuilder {
	b.params.OpenshiftVersionId = version
	return b
}
func (b *NodePoolParamsBuilder) ClusterName(name string) *NodePoolParamsBuilder {
	b.params.ClusterName = name
	return b
}
func (b *NodePoolParamsBuilder) NodePoolName(name string) *NodePoolParamsBuilder {
	b.params.NodePoolName = name
	return b
}
func (b *NodePoolParamsBuilder) Replicas(replicas int32) *NodePoolParamsBuilder {
	b.params.Replicas = replicas
	return b
}
func (b *NodePoolParamsBuilder) VMSize(size string) *NodePoolParamsBuilder {
	b.params.VMSize = size
	return b
}
func (b *NodePoolParamsBuilder) OSDiskSizeGiB(size int32) *NodePoolParamsBuilder {
	b.params.OSDiskSizeGiB = size
	return b
}
func (b *NodePoolParamsBuilder) DiskStorageAccountType(accountType string) *NodePoolParamsBuilder {
	b.params.DiskStorageAccountType = accountType
	return b
}
func (b *NodePoolParamsBuilder) ChannelGroup(group string) *NodePoolParamsBuilder {
	b.params.ChannelGroup = group
	return b
}
func (b *NodePoolParamsBuilder) Build() NodePoolParams {
	return b.params
}

func ConvertToUserAssignedIdentitiesProfile(value interface{}) (*hcpsdk20240610preview.UserAssignedIdentitiesProfile, error) {
	if value == nil {
		return nil, nil
	}
	b, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal UserAssignedIdentitiesValue: %w", err)
	}
	var uamis hcpsdk20240610preview.UserAssignedIdentitiesProfile
	if err := json.Unmarshal(b, &uamis); err != nil {
		return nil, fmt.Errorf("failed to unmarshal UserAssignedIdentitiesValue: %w", err)
	}
	return &uamis, nil
}

func ConvertToManagedServiceIdentity(value interface{}) (*hcpsdk20240610preview.ManagedServiceIdentity, error) {
	if value == nil {
		return nil, nil
	}
	b, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal IdentityValue: %w", err)
	}
	var msi hcpsdk20240610preview.ManagedServiceIdentity
	if err := json.Unmarshal(b, &msi); err != nil {
		return nil, fmt.Errorf("failed to unmarshal IdentityValue: %w", err)
	}
	return &msi, nil
}
