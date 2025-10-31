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

// ClusterParams holds parameters for HCP cluster deployment
type ClusterParams struct {
	OpenshiftVersionId          string
	ClusterName                 string
	ManagedResourceGroupName    string
	NsgName                     string
	SubnetName                  string
	VnetName                    string
	UserAssignedIdentitiesValue interface{}
	IdentityValue               interface{}
	KeyVaultName                string
	EtcdEncryptionKeyName       string
	Network                     NetworkConfig
	APIVisibility               string
	ImageRegistryState          string
}

// NodePoolParams holds parameters for node pool deployment
type NodePoolParams struct {
	OpenshiftVersionId     string
	ClusterName            string
	NodePoolName           string
	Replicas               int32
	VMSize                 string
	OSDiskSizeGiB          int32
	DiskStorageAccountType string
}

// NetworkConfig mirrors the shape from the cluster module parameters
type NetworkConfig struct {
	NetworkType string
	PodCIDR     string
	ServiceCIDR string
	MachineCIDR string
	HostPrefix  int32
}

// NewDefaultClusterParams builds ClusterParams with defaults derived from the cluster module.
// Defaults sourced from test/e2e-setup/bicep/modules/cluster.bicep
// - openshiftVersionId: '4.19'
func NewDefaultClusterParams(clusterName string) ClusterParams {
	return ClusterParams{
		OpenshiftVersionId: "4.19",
		ClusterName:        clusterName,
		Network: NetworkConfig{
			NetworkType: "OVNKubernetes",
			PodCIDR:     "10.128.0.0/14",
			ServiceCIDR: "172.30.0.0/16",
			MachineCIDR: "10.0.0.0/16",
			HostPrefix:  23,
		},
		APIVisibility:      "Public",
		ImageRegistryState: "Enabled",
	}
}

// NewDefaultNodePoolParams builds NodePoolParams with defaults derived from the nodepool module.
// Defaults sourced from test/e2e-setup/bicep/modules/nodepool.bicep
// - replicas: 2
// - openshiftVersionId: '4.19.7'
// - osDiskSizeGiB: 64
// - vmSize: 'Standard_D8s_v3'
func NewDefaultNodePoolParams(clusterName, nodePoolName string) NodePoolParams {
	return NodePoolParams{
		OpenshiftVersionId:     "4.19.7",
		ClusterName:            clusterName,
		NodePoolName:           nodePoolName,
		Replicas:               int32(2),
		VMSize:                 "Standard_D8s_v3",
		OSDiskSizeGiB:          int32(64),
		DiskStorageAccountType: "StandardSSD_LRS",
	}
}
