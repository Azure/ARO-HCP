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
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"

	"k8s.io/apimachinery/pkg/util/rand"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/internal/api"
	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	hcpsdk20251223preview "github.com/Azure/ARO-HCP/test/sdk/v20251223preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

type RBACScope string

const (
	RBACScopeResourceGroup RBACScope = "resourceGroup"
	RBACScopeResource      RBACScope = "resource"

	// Default OpenShift channel group, version, and node pool version for the E2E test
	DefaultOCPChannelGroup         = "stable"
	DefaultOCPVersionId            = "4.20"
	DefaultOCPNodePoolVersionId    = "4.20.15"
	DefaultOCPNodePoolChannelGroup = "stable"
)

type ClusterParams struct {
	OpenshiftVersionId            string
	ClusterName                   string
	ManagedResourceGroupName      string
	NsgResourceID                 string
	NsgName                       string
	SubnetResourceID              string
	SubnetName                    string
	VnetName                      string
	UserAssignedIdentitiesProfile *hcpsdk20240610preview.UserAssignedIdentitiesProfile
	Identity                      *hcpsdk20240610preview.ManagedServiceIdentity
	KeyVaultName                  string
	EtcdEncryptionKeyName         string
	EtcdEncryptionKeyVersion      string
	EncryptionKeyManagementMode   string
	EncryptionType                string
	VnetIntegrationSubnetID       string
	KeyVaultVisibility            string
	Network                       NetworkConfig
	APIVisibility                 string
	ImageRegistryState            string
	ChannelGroup                  string
	AuthorizedCIDRs               []*string
	Autoscaling                   *hcpsdk20240610preview.ClusterAutoscalingProfile
	Tags                          map[string]*string
}

type NetworkConfig struct {
	NetworkType string
	PodCIDR     string
	ServiceCIDR string
	MachineCIDR string
	HostPrefix  int32
}

func DefaultOpenshiftControlPlaneVersionId() string {
	version := os.Getenv("ARO_HCP_OPENSHIFT_CONTROLPLANE_VERSION")
	if version == "" {
		version = DefaultOCPVersionId
	}
	GinkgoLogr.Info("Using OpenShift control plane version", "version", version)
	return version
}

func DefaultOpenshiftChannelGroup() string {
	channelGroup := os.Getenv("ARO_HCP_OPENSHIFT_CHANNEL_GROUP")
	if channelGroup == "" {
		channelGroup = DefaultOCPChannelGroup
	}
	GinkgoLogr.Info("Using OpenShift channel group", "channelGroup", channelGroup)
	return channelGroup
}

func DefaultOpenshiftNodePoolVersionId() string {
	version := os.Getenv("ARO_HCP_OPENSHIFT_NODEPOOL_VERSION")
	if version == "" {
		version = DefaultOCPNodePoolVersionId
	}
	GinkgoLogr.Info("Using OpenShift node pool version", "version", version)
	return version
}

func DefaultOpenshiftNodePoolChannelGroup() string {
	channelGroup := os.Getenv("ARO_HCP_OPENSHIFT_NODEPOOL_CHANNEL_GROUP")
	if channelGroup == "" {
		channelGroup = DefaultOCPNodePoolChannelGroup
	}
	GinkgoLogr.Info("Using OpenShift node pool channel group", "channelGroup", channelGroup)
	return channelGroup
}

func NewDefaultClusterParams() ClusterParams {
	return ClusterParams{
		OpenshiftVersionId: DefaultOpenshiftControlPlaneVersionId(),
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
		ChannelGroup:                DefaultOpenshiftChannelGroup(),
		// NOTE: The E2E subscription must have the ExperimentalReleaseFeatures AFEC
		// registered for this tag to be honored.
		Tags: map[string]*string{
			api.TagClusterSizeOverride: to.Ptr(string(api.MinimalControlPlanePodSizing)),
		},
	}
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
	// AutoScaling enables nodepool autoscaling. When set, Replicas is ignored.
	AutoScaling *NodePoolAutoScalingParams
}

// NodePoolAutoScalingParams contains min/max node counts for nodepool autoscaling
type NodePoolAutoScalingParams struct {
	Min int32
	Max int32
}

func NewDefaultNodePoolParams() NodePoolParams {
	return NodePoolParams{
		OpenshiftVersionId:     DefaultOpenshiftNodePoolVersionId(),
		Replicas:               int32(2),
		VMSize:                 "Standard_D8s_v3",
		OSDiskSizeGiB:          int32(64),
		DiskStorageAccountType: "StandardSSD_LRS",
		ChannelGroup:           DefaultOpenshiftNodePoolChannelGroup(),
	}
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

func PopulateClusterParamsFromCustomerInfraDeployment(
	params ClusterParams,
	customerInfraDeploymentResult *armresources.DeploymentExtended,
) (ClusterParams, error) {
	if customerInfraDeploymentResult == nil {
		return params, fmt.Errorf("customerInfraDeploymentResult cannot be nil")
	}

	keyVaultName, err := GetOutputValueString(customerInfraDeploymentResult, "keyVaultName")
	if err != nil {
		return params, fmt.Errorf("failed to get keyVaultName from customer infra deployment: %w", err)
	}
	etcdEncryptionKeyVersion, err := GetOutputValueString(customerInfraDeploymentResult, "etcdEncryptionKeyVersion")
	if err != nil {
		return params, fmt.Errorf("failed to get etcdEncryptionKeyVersion from customer infra deployment: %w", err)
	}
	etcdEncryptionKeyName, err := GetOutputValueString(customerInfraDeploymentResult, "etcdEncryptionKeyName")
	if err != nil {
		return params, fmt.Errorf("failed to get etcdEncryptionKeyName from customer infra deployment: %w", err)
	}
	nsgResourceID, err := GetOutputValueString(customerInfraDeploymentResult, "nsgID")
	if err != nil {
		return params, fmt.Errorf("failed to get nsgID from customer infra deployment: %w", err)
	}
	subnetResourceID, err := GetOutputValueString(customerInfraDeploymentResult, "vnetSubnetID")
	if err != nil {
		return params, fmt.Errorf("failed to get vnetSubnetID from customer infra deployment: %w", err)
	}
	vnetIntegrationSubnetID, err := GetOutputValueString(customerInfraDeploymentResult, "vnetIntegrationSubnetID")
	if err != nil {
		return params, fmt.Errorf("failed to get vnetIntegrationSubnetID from customer infra deployment: %w", err)
	}
	vnetName, err := GetOutputValueString(customerInfraDeploymentResult, "vnetName")
	if err != nil {
		return params, fmt.Errorf("failed to get vnetName from customer infra deployment: %w", err)
	}
	nsgName, err := GetOutputValueString(customerInfraDeploymentResult, "nsgName")
	if err != nil {
		return params, fmt.Errorf("failed to get nsgName from customer infra deployment: %w", err)
	}
	subnetName, err := GetOutputValueString(customerInfraDeploymentResult, "vnetSubnetName")
	if err != nil {
		return params, fmt.Errorf("failed to get vnetSubnetName from customer infra deployment: %w", err)
	}
	params.KeyVaultName = keyVaultName
	params.EtcdEncryptionKeyVersion = etcdEncryptionKeyVersion
	params.EtcdEncryptionKeyName = etcdEncryptionKeyName
	params.NsgResourceID = nsgResourceID
	params.SubnetResourceID = subnetResourceID
	params.VnetIntegrationSubnetID = vnetIntegrationSubnetID
	params.VnetName = vnetName
	params.NsgName = nsgName
	params.SubnetName = subnetName
	return params, nil
}

func PopulateClusterParamsFromManagedIdentitiesDeployment(
	params ClusterParams,
	managedIdentitiesDeploymentResult *armresources.DeploymentExtended,
) (ClusterParams, error) {
	if managedIdentitiesDeploymentResult == nil {
		return params, fmt.Errorf("managedIdentitiesDeploymentResult cannot be nil")
	}

	userAssignedIdentities, err := GetOutputValue(managedIdentitiesDeploymentResult, "userAssignedIdentitiesValue")
	if err != nil {
		return params, fmt.Errorf("failed to get userAssignedIdentitiesValue from managed identity deployment: %w", err)
	}
	userAssignedIdentitiesProfile, err := ConvertToUserAssignedIdentitiesProfile(userAssignedIdentities)
	if err != nil {
		return params, fmt.Errorf("failed to convert userAssignedIdentitiesValue: %w", err)
	}

	identityValue, err := GetOutputValue(managedIdentitiesDeploymentResult, "identityValue")
	if err != nil {
		return params, fmt.Errorf("failed to get identityValue from managed identity deployment: %w", err)
	}
	identityProfile, err := ConvertToManagedServiceIdentity(identityValue)
	if err != nil {
		return params, fmt.Errorf("failed to convert identityValue: %w", err)
	}

	params.UserAssignedIdentitiesProfile = userAssignedIdentitiesProfile
	params.Identity = identityProfile

	return params, nil
}

func (tc *perItOrDescribeTestContext) CreateClusterCustomerResources(ctx context.Context,
	resourceGroup *armresources.ResourceGroup,
	clusterParams ClusterParams,
	infraParameters map[string]interface{},
	artifactsFS embed.FS,
	rbacScope RBACScope,
) (ClusterParams, error) {
	startTime := time.Now()
	defer func() {
		finishTime := time.Now()
		tc.RecordTestStep(fmt.Sprintf("Deploy customer resources in resource group %s", *resourceGroup.Name), startTime, finishTime)
	}()

	// Generate unique deployment names by combining cluster name with random suffix
	randomSuffix := rand.String(6)
	customerInfraDeploymentName := fmt.Sprintf("customer-infra-%s-%s", clusterParams.ClusterName, randomSuffix)
	managedIdentitiesDeploymentName := fmt.Sprintf("mi-%s-%s", clusterParams.ClusterName, randomSuffix)

	// ensure customer-infra resource names are unique per cluster
	infraParameters["clusterName"] = clusterParams.ClusterName

	customerInfraDeploymentResult, err := tc.CreateBicepTemplateAndWait(ctx,
		WithTemplateFromFS(artifactsFS, "test-artifacts/generated-test-artifacts/modules/customer-infra.json"),
		WithDeploymentName(customerInfraDeploymentName),
		WithScope(BicepDeploymentScopeResourceGroup),
		WithClusterResourceGroup(*resourceGroup.Name),
		WithParameters(infraParameters),
		WithTimeout(45*time.Minute),
	)
	if err != nil {
		return clusterParams, fmt.Errorf("failed to create customer-infra: %w", err)
	}
	clusterParams, err = PopulateClusterParamsFromCustomerInfraDeployment(clusterParams, customerInfraDeploymentResult)
	if err != nil {
		return clusterParams, fmt.Errorf("failed to populate cluster params from customer-infra: %w", err)
	}

	managedIdentityDeploymentResult, err := tc.DeployManagedIdentities(ctx,
		clusterParams.ClusterName,
		rbacScope,
		WithTemplateFromFS(artifactsFS, "test-artifacts/generated-test-artifacts/modules/managed-identities.json"),
		WithDeploymentName(managedIdentitiesDeploymentName),
		WithClusterResourceGroup(*resourceGroup.Name),
		WithParameters(map[string]interface{}{
			"nsgName":      clusterParams.NsgName,
			"vnetName":     clusterParams.VnetName,
			"subnetName":   clusterParams.SubnetName,
			"keyVaultName": clusterParams.KeyVaultName,
		}),
	)

	if err != nil {
		return clusterParams, fmt.Errorf("failed to create managed identities: %w", err)
	}
	clusterParams, err = PopulateClusterParamsFromManagedIdentitiesDeployment(clusterParams, managedIdentityDeploymentResult)
	if err != nil {
		return clusterParams, fmt.Errorf("failed to populate cluster params from managed identities: %w", err)
	}
	return clusterParams, nil
}

// ClusterParamsV20251223 contains parameters for v20251223preview cluster creation
type ClusterParamsV20251223 struct {
	OpenshiftVersionId            string
	ClusterName                   string
	ManagedResourceGroupName      string
	NsgResourceID                 string
	NsgName                       string
	SubnetResourceID              string
	SubnetName                    string
	VnetName                      string
	UserAssignedIdentitiesProfile *hcpsdk20251223preview.UserAssignedIdentitiesProfile
	Identity                      *hcpsdk20251223preview.ManagedServiceIdentity
	KeyVaultName                  string
	EtcdEncryptionKeyName         string
	EtcdEncryptionKeyVersion      string
	EncryptionKeyManagementMode   string
	EncryptionType                string
	KeyVaultVisibility            string
	Network                       NetworkConfig
	APIVisibility                 string
	ImageRegistryState            string
	ChannelGroup                  string
	AuthorizedCIDRs               []*string
	Autoscaling                   *hcpsdk20251223preview.ClusterAutoscalingProfile
	Tags                          map[string]*string
}

// NewDefaultClusterParamsV20251223 returns default v20251223preview cluster parameters
func NewDefaultClusterParamsV20251223() ClusterParamsV20251223 {
	return ClusterParamsV20251223{
		OpenshiftVersionId:          DefaultOpenshiftControlPlaneVersionId(),
		ChannelGroup:                DefaultOpenshiftChannelGroup(),
		EncryptionKeyManagementMode: "CustomerManaged",
		EncryptionType:              "KMS",
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

// CreateClusterCustomerResourcesV20251223 creates customer resources and returns v20251223preview types
func (tc *perItOrDescribeTestContext) CreateClusterCustomerResourcesV20251223(ctx context.Context,
	resourceGroup *armresources.ResourceGroup,
	clusterParams ClusterParamsV20251223,
	infraParameters map[string]interface{},
	artifactsFS embed.FS,
	rbacScope RBACScope,
) (ClusterParamsV20251223, error) {
	startTime := time.Now()
	defer func() {
		finishTime := time.Now()
		tc.RecordTestStep(fmt.Sprintf("Deploy customer resources in resource group %s", *resourceGroup.Name), startTime, finishTime)
	}()

	// Generate unique deployment names
	randomSuffix := rand.String(6)
	customerInfraDeploymentName := fmt.Sprintf("customer-infra-%s-%s", clusterParams.ClusterName, randomSuffix)
	managedIdentitiesDeploymentName := fmt.Sprintf("mi-%s-%s", clusterParams.ClusterName, randomSuffix)

	// Ensure customer-infra resource names are unique per cluster
	infraParameters["clusterName"] = clusterParams.ClusterName

	customerInfraDeploymentResult, err := tc.CreateBicepTemplateAndWait(ctx,
		WithTemplateFromFS(artifactsFS, "test-artifacts/generated-test-artifacts/modules/customer-infra.json"),
		WithDeploymentName(customerInfraDeploymentName),
		WithScope(BicepDeploymentScopeResourceGroup),
		WithClusterResourceGroup(*resourceGroup.Name),
		WithParameters(infraParameters),
		WithTimeout(45*time.Minute),
	)
	if err != nil {
		return clusterParams, fmt.Errorf("failed to create customer-infra: %w", err)
	}

	// Populate infrastructure outputs
	clusterParams.KeyVaultName, err = GetOutputValueString(customerInfraDeploymentResult, "keyVaultName")
	if err != nil {
		return clusterParams, fmt.Errorf("failed to get keyVaultName: %w", err)
	}
	clusterParams.EtcdEncryptionKeyVersion, err = GetOutputValueString(customerInfraDeploymentResult, "etcdEncryptionKeyVersion")
	if err != nil {
		return clusterParams, fmt.Errorf("failed to get etcdEncryptionKeyVersion: %w", err)
	}
	clusterParams.EtcdEncryptionKeyName, err = GetOutputValueString(customerInfraDeploymentResult, "etcdEncryptionKeyName")
	if err != nil {
		return clusterParams, fmt.Errorf("failed to get etcdEncryptionKeyName: %w", err)
	}
	clusterParams.NsgResourceID, err = GetOutputValueString(customerInfraDeploymentResult, "nsgID")
	if err != nil {
		return clusterParams, fmt.Errorf("failed to get nsgID: %w", err)
	}
	clusterParams.SubnetResourceID, err = GetOutputValueString(customerInfraDeploymentResult, "vnetSubnetID")
	if err != nil {
		return clusterParams, fmt.Errorf("failed to get vnetSubnetID: %w", err)
	}
	clusterParams.VnetName, err = GetOutputValueString(customerInfraDeploymentResult, "vnetName")
	if err != nil {
		return clusterParams, fmt.Errorf("failed to get vnetName: %w", err)
	}
	clusterParams.NsgName, err = GetOutputValueString(customerInfraDeploymentResult, "nsgName")
	if err != nil {
		return clusterParams, fmt.Errorf("failed to get nsgName: %w", err)
	}
	clusterParams.SubnetName, err = GetOutputValueString(customerInfraDeploymentResult, "vnetSubnetName")
	if err != nil {
		return clusterParams, fmt.Errorf("failed to get vnetSubnetName: %w", err)
	}

	// Deploy managed identities
	managedIdentityDeploymentResult, err := tc.DeployManagedIdentities(ctx,
		clusterParams.ClusterName,
		rbacScope,
		WithTemplateFromFS(artifactsFS, "test-artifacts/generated-test-artifacts/modules/managed-identities.json"),
		WithDeploymentName(managedIdentitiesDeploymentName),
		WithClusterResourceGroup(*resourceGroup.Name),
		WithParameters(map[string]interface{}{
			"nsgName":      clusterParams.NsgName,
			"vnetName":     clusterParams.VnetName,
			"subnetName":   clusterParams.SubnetName,
			"keyVaultName": clusterParams.KeyVaultName,
		}),
	)
	if err != nil {
		return clusterParams, fmt.Errorf("failed to create managed identities: %w", err)
	}

	// Get identity outputs and convert to v20251223preview types
	userAssignedIdentitiesValue, err := GetOutputValue(managedIdentityDeploymentResult, "userAssignedIdentitiesValue")
	if err != nil {
		return clusterParams, fmt.Errorf("failed to get userAssignedIdentitiesValue: %w", err)
	}
	identityValue, err := GetOutputValue(managedIdentityDeploymentResult, "identityValue")
	if err != nil {
		return clusterParams, fmt.Errorf("failed to get identityValue: %w", err)
	}

	// Convert to v20251223preview types using JSON marshal/unmarshal
	b, err := json.Marshal(userAssignedIdentitiesValue)
	if err != nil {
		return clusterParams, fmt.Errorf("failed to marshal userAssignedIdentitiesValue: %w", err)
	}
	var uamis hcpsdk20251223preview.UserAssignedIdentitiesProfile
	if err := json.Unmarshal(b, &uamis); err != nil {
		return clusterParams, fmt.Errorf("failed to unmarshal userAssignedIdentitiesValue: %w", err)
	}
	clusterParams.UserAssignedIdentitiesProfile = &uamis

	b, err = json.Marshal(identityValue)
	if err != nil {
		return clusterParams, fmt.Errorf("failed to marshal identityValue: %w", err)
	}
	var msi hcpsdk20251223preview.ManagedServiceIdentity
	if err := json.Unmarshal(b, &msi); err != nil {
		return clusterParams, fmt.Errorf("failed to unmarshal identityValue: %w", err)
	}
	clusterParams.Identity = &msi

	return clusterParams, nil
}
