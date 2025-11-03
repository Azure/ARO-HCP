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
	"context"
	"encoding/json"
	"fmt"
	"strings"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"
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

type NetworkConfig struct {
	NetworkType string
	PodCIDR     string
	ServiceCIDR string
	MachineCIDR string
	HostPrefix  int32
}

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
		ChannelGroup:       "stable",
	}
}

func NewDefaultNodePoolParams(clusterName, nodePoolName string) NodePoolParams {
	return NodePoolParams{
		OpenshiftVersionId:     "4.19.7",
		ClusterName:            clusterName,
		NodePoolName:           nodePoolName,
		Replicas:               int32(2),
		VMSize:                 "Standard_D8s_v3",
		OSDiskSizeGiB:          int32(64),
		DiskStorageAccountType: "StandardSSD_LRS",
		ChannelGroup:           "stable",
	}
}

func GetLatestKeyVaultKeyVersion(ctx context.Context, keyVaultName, keyName string) (string, error) {
	azureCredentials, err := azidentity.NewAzureCLICredential(nil)
	if err != nil {
		return "", fmt.Errorf("failed building development environment CLI credential: %w", err)
	}
	client, err := azkeys.NewClient(fmt.Sprintf("https://%s.vault.azure.net/", keyVaultName), azureCredentials, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create key vault client: %w", err)
	}
	versions := client.NewListKeyPropertiesVersionsPager(keyName, nil)
	page, err := versions.NextPage(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to list key versions: %w", err)
	}
	if len(page.Value) == 0 || page.Value[0].KID == nil {
		return "", fmt.Errorf("no key versions found for key %s", keyName)
	}

	keyID := string(*page.Value[0].KID)
	parts := strings.Split(keyID, "/")
	return parts[len(parts)-1], nil
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
