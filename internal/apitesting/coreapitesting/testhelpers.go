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

package coreapitesting

import (
	"io"
	"log/slog"
	"path"
	"testing"
	"time"

	"dario.cat/mergo"
	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20240610preview/generated"
)

// The definitions in this file are meant for unit tests.

const (
	TestLocation                                = "westus3"
	TestAPIVersion                              = "2024-06-10-preview"
	TestTenantID                                = "33333333-3333-3333-3333-333333333333"
	TestSubscriptionID                          = "11111111-1111-1111-1111-111111111111"
	TestAltSubscriptionID                       = "22222222-2222-2222-2222-222222222222"
	TestResourceGroupName                       = "testResourceGroup"
	TestClusterName                             = "testCluster"
	TestNodePoolName                            = "testNodePool"
	TestExternalAuthName                        = "testExtAuth"
	TestDeploymentName                          = "testDeployment"
	TestManagedResourceGroupName                = "testManagedResourceGroup"
	TestNetworkSecurityGroupName                = "testNetworkSecurityGroup"
	TestVirtualNetworkName                      = "testVirtualNetwork"
	TestSubnetName                              = "testSubnet"
	TestVnetIntegrationSubnetName               = "testVnetIntegrationSubnet"
	TestKMSKeyName                              = "testKMSKeyName"
	TestKMSKeyVaultName                         = "testKMSKeyVaultName"
	TestKMSKeyVersion                           = "testKMSKeyVersion"
	TestPendingClusterServiceIDPath             = "/api/aro_hcp/v1alpha1/clusters/test-cluster-service-id"
	TestPendingClusterServiceIDClusterIDSegment = "test-cluster-service-id"
)

var (
	TestSubscriptionResourceID                = path.Join("/subscriptions", TestSubscriptionID)
	TestResourceGroupResourceID               = path.Join(TestSubscriptionResourceID, "resourceGroups", TestResourceGroupName)
	TestClusterResourceID                     = path.Join(TestResourceGroupResourceID, "providers", coreapi.ProviderNamespace, coreapi.ClusterResourceTypeName, TestClusterName)
	TestNodePoolResourceID                    = path.Join(TestClusterResourceID, coreapi.NodePoolResourceTypeName, TestNodePoolName)
	TestExternalAuthResourceID                = path.Join(TestClusterResourceID, coreapi.ExternalAuthResourceTypeName, TestExternalAuthName)
	TestDeploymentResourceID                  = path.Join(TestResourceGroupResourceID, "providers", coreapi.ProviderNamespace, "deployments", TestDeploymentName)
	TestNetworkSecurityGroupResourceID        = path.Join(TestResourceGroupResourceID, "providers", "Microsoft.Network", "networkSecurityGroups", TestNetworkSecurityGroupName)
	TestVirtualNetworkResourceID              = path.Join(TestResourceGroupResourceID, "providers", "Microsoft.Network", "virtualNetworks", TestVirtualNetworkName)
	TestSubnetResourceID                      = path.Join(TestVirtualNetworkResourceID, "subnets", TestSubnetName)
	TestVnetIntegrationSubnetResourceID       = path.Join(TestVirtualNetworkResourceID, "subnets", TestVnetIntegrationSubnetName)
	TestManagedIdentitiesDataPlaneIdentityURL = "https://dummyhost.identity.azure.net/otherinformation?aqueryarg=somevalue"
)

func NewTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func NewTestUserAssignedIdentity(name string) *azcorearm.ResourceID {
	return metadataapi.Must(azcorearm.ParseResourceID(path.Join(TestResourceGroupResourceID, "providers", "Microsoft.ManagedIdentity", "userAssignedIdentities", name)))
}

func MinimumValidClusterTestCase() *coreapi.HCPOpenShiftCluster {
	resource := coreapi.NewDefaultHCPOpenShiftCluster(metadataapi.Must(azcorearm.ParseResourceID(TestClusterResourceID)), TestLocation)
	resource.CustomerProperties.Version.ID = "4.20"
	resource.CustomerProperties.DNS.BaseDomainPrefix = "testcluster"
	resource.CustomerProperties.Etcd.DataEncryption.KeyManagementMode = metadataapi.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged
	resource.CustomerProperties.Etcd.DataEncryption.CustomerManaged = &coreapi.CustomerManagedEncryptionProfile{
		EncryptionType: metadataapi.CustomerManagedEncryptionTypeKMS,
		Kms: &coreapi.KmsEncryptionProfile{
			Visibility: metadataapi.KeyVaultVisibilityPublic,
			ActiveKey: coreapi.KmsKey{
				Name:      TestKMSKeyName,
				VaultName: TestKMSKeyVaultName,
				Version:   TestKMSKeyVersion,
			},
		},
	}
	resource.CustomerProperties.Platform.ManagedResourceGroup = TestManagedResourceGroupName
	resource.CustomerProperties.Platform.SubnetID = metadataapi.Must(azcorearm.ParseResourceID(TestSubnetResourceID))
	resource.CustomerProperties.Platform.VnetIntegrationSubnetID = metadataapi.Must(azcorearm.ParseResourceID(TestVnetIntegrationSubnetResourceID))
	resource.CustomerProperties.Platform.NetworkSecurityGroupID = metadataapi.Must(azcorearm.ParseResourceID(TestNetworkSecurityGroupResourceID))
	resource.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL = TestManagedIdentitiesDataPlaneIdentityURL
	// PlatformManaged etcd encryption is not currently supported; require CustomerManaged for a valid cluster.
	resource.CustomerProperties.Etcd.DataEncryption.KeyManagementMode = metadataapi.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged
	resource.CustomerProperties.Etcd.DataEncryption.CustomerManaged = &coreapi.CustomerManagedEncryptionProfile{
		EncryptionType: metadataapi.CustomerManagedEncryptionTypeKMS,
		Kms: &coreapi.KmsEncryptionProfile{
			Visibility: metadataapi.KeyVaultVisibilityPublic,
			ActiveKey: coreapi.KmsKey{
				Name:      "test-key",
				VaultName: "test-vault",
				Version:   "test-version",
			},
		},
	}
	// Add required systemData fields
	createdAt := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	resource.SystemData = &coreapi.SystemData{
		CreatedBy:     "test-user",
		CreatedByType: coreapi.CreatedByTypeUser,
		CreatedAt:     &createdAt,
	}
	resource.ServiceProviderProperties.ClusterUID = "00000000-0000-0000-0000-000000000000"
	pendingID := metadataapi.Must(metadataapi.NewInternalID(TestPendingClusterServiceIDPath))
	resource.ServiceProviderProperties.PendingClusterServiceID = &pendingID
	return resource
}

func ClusterTestCase(t *testing.T, tweaks *coreapi.HCPOpenShiftCluster) *coreapi.HCPOpenShiftCluster {
	resource := MinimumValidClusterTestCase()
	require.NoError(t, mergo.Merge(resource, tweaks, mergo.WithOverride))
	return resource
}

func MinimumValidExternalAuthTestCase() *coreapi.HCPOpenShiftClusterExternalAuth {
	resource := coreapi.NewDefaultHCPOpenShiftClusterExternalAuth(metadataapi.Must(azcorearm.ParseResourceID(TestExternalAuthResourceID)))
	resource.Properties.Issuer.URL = "https://www.redhat.com"
	resource.Properties.Issuer.Audiences = []string{"audience1"}
	resource.Properties.Claim.Mappings.Username.Claim = "my-cool-claim"
	// Add required systemData fields
	createdAt := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	resource.SystemData = &coreapi.SystemData{
		CreatedBy:     "test-user",
		CreatedByType: coreapi.CreatedByTypeUser,
		CreatedAt:     &createdAt,
	}
	return resource
}

func ExternalAuthTestCase(t *testing.T, tweaks *coreapi.HCPOpenShiftClusterExternalAuth) *coreapi.HCPOpenShiftClusterExternalAuth {
	externalAuth := MinimumValidExternalAuthTestCase()
	require.NoError(t, mergo.Merge(externalAuth, tweaks, mergo.WithOverride))
	return externalAuth
}

// +k8s:deepcopy-gen=false
type ExternalTestResource struct {
	ID         *string
	Name       *string
	Type       *string
	SystemData *generated.SystemData
	Location   *string
	Tags       map[string]*string
	Identity   *generated.ManagedServiceIdentity
}

type InternalTestResource struct {
	coreapi.TrackedResource
	Identity *coreapi.ManagedServiceIdentity `json:"identity"`
}

var _ coreapi.VersionedCreatableResource[InternalTestResource] = &ExternalTestResource{}

func (m *ExternalTestResource) NewExternal() any {
	//TODO implement me
	panic("implement me")
}

func (m *ExternalTestResource) GetVersion() coreapi.Version {
	// FIXME Implement if there's a need for it in tests.
	return nil
}

func (m *ExternalTestResource) ConvertToInternal(_ *InternalTestResource) (*InternalTestResource, error) {
	// FIXME Implement if there's a need for it in tests.
	return nil, nil
}

// CreateTestSubscription creates a test subscription with optional registered feature flags.
// Call with no arguments for a standard subscription, or pass feature names to register them.
func CreateTestSubscription(registeredFeatures ...string) *coreapi.Subscription {
	features := make([]coreapi.Feature, len(registeredFeatures))
	for i, feature := range registeredFeatures {
		features[i] = coreapi.Feature{
			Name:  ptr.To(feature),
			State: ptr.To("Registered"),
		}
	}

	return &coreapi.Subscription{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID: metadataapi.Must(azcorearm.ParseResourceID(TestSubscriptionResourceID)),
		},
		ResourceID:       metadataapi.Must(azcorearm.ParseResourceID(TestSubscriptionResourceID)),
		State:            coreapi.SubscriptionStateRegistered,
		RegistrationDate: ptr.To(time.Now().Format(time.RFC1123)),
		Properties: &coreapi.SubscriptionProperties{
			RegisteredFeatures: &features,
		},
	}
}
