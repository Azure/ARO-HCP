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
	"io"
	"log/slog"
	"path"
	"testing"

	"dario.cat/mergo"
	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
)

// The definitions in this file are meant for unit tests.

const (
	TestLocation                 = "westus3"
	TestAPIVersion               = "2024-06-10-preview"
	TestTenantID                 = "00000000-0000-0000-0000-000000000000"
	TestSubscriptionID           = "11111111-1111-1111-1111-111111111111"
	TestAltSubscriptionID        = "22222222-2222-2222-2222-222222222222"
	TestResourceGroupName        = "testResourceGroup"
	TestClusterName              = "testCluster"
	TestNodePoolName             = "testNodePool"
	TestExternalAuthName         = "testExternalAuth"
	TestDeploymentName           = "testDeployment"
	TestManagedResourceGroupName = "testManagedResourceGroup"
	TestNetworkSecurityGroupName = "testNetworkSecurityGroup"
	TestVirtualNetworkName       = "testVirtualNetwork"
	TestSubnetName               = "testSubnet"
)

var (
	TestSubscriptionResourceID         = path.Join("/subscriptions", TestSubscriptionID)
	TestResourceGroupResourceID        = path.Join(TestSubscriptionResourceID, "resourceGroups", TestResourceGroupName)
	TestClusterResourceID              = path.Join(TestResourceGroupResourceID, "providers", ProviderNamespace, ClusterResourceTypeName, TestClusterName)
	TestNodePoolResourceID             = path.Join(TestClusterResourceID, NodePoolResourceTypeName, TestNodePoolName)
	TestExternalAuthResourceID         = path.Join(TestClusterResourceID, ExternalAuthResourceTypeName, TestExternalAuthName)
	TestDeploymentResourceID           = path.Join(TestResourceGroupResourceID, "providers", ProviderNamespace, "deployments", TestDeploymentName)
	TestNetworkSecurityGroupResourceID = path.Join(TestResourceGroupResourceID, "providers", "Microsoft.Network", "networkSecurityGroups", TestNetworkSecurityGroupName)
	TestVirtualNetworkResourceID       = path.Join(TestResourceGroupResourceID, "providers", "Microsoft.Network", "virtualNetworks", TestVirtualNetworkName)
	TestSubnetResourceID               = path.Join(TestVirtualNetworkResourceID, "subnets", TestSubnetName)
)

func NewTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func NewTestUserAssignedIdentity(name string) string {
	return path.Join(TestResourceGroupResourceID, "providers", "Microsoft.ManagedIdentity", "userAssignedIdentities", name)
}

func MinimumValidClusterTestCase() *HCPOpenShiftCluster {
	resource := NewDefaultHCPOpenShiftCluster(Must(azcorearm.ParseResourceID(TestClusterResourceID)))
	resource.CustomerProperties.Platform.ManagedResourceGroup = TestManagedResourceGroupName
	resource.CustomerProperties.Platform.SubnetID = TestSubnetResourceID
	resource.CustomerProperties.Platform.NetworkSecurityGroupID = TestNetworkSecurityGroupResourceID
	return resource
}

func ClusterTestCase(t *testing.T, tweaks *HCPOpenShiftCluster) *HCPOpenShiftCluster {
	resource := MinimumValidClusterTestCase()
	require.NoError(t, mergo.Merge(resource, tweaks, mergo.WithOverride))
	return resource
}

func MinimumValidExternalAuthTestCase() *HCPOpenShiftClusterExternalAuth {
	resource := NewDefaultHCPOpenShiftClusterExternalAuth(Must(azcorearm.ParseResourceID(TestExternalAuthResourceID)))
	resource.Properties.Issuer.URL = "https://www.redhat.com"
	resource.Properties.Issuer.Audiences = []string{"audience1"}
	resource.Properties.Claim.Mappings.Username.Claim = "my-cool-claim"
	return resource
}

func ExternalAuthTestCase(t *testing.T, tweaks *HCPOpenShiftClusterExternalAuth) *HCPOpenShiftClusterExternalAuth {
	externalAuth := MinimumValidExternalAuthTestCase()
	require.NoError(t, mergo.Merge(externalAuth, tweaks, mergo.WithOverride))
	return externalAuth
}

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
	arm.TrackedResource
	Identity *arm.ManagedServiceIdentity `json:"identity"`
}

var _ VersionedCreatableResource[InternalTestResource] = &ExternalTestResource{}

func (m *ExternalTestResource) NewExternal() any {
	//TODO implement me
	panic("implement me")
}

func (m *ExternalTestResource) SetDefaultValues(a any) error {
	//TODO implement me
	panic("implement me")
}

func (m *ExternalTestResource) GetVersion() Version {
	// FIXME Implement if there's a need for it in tests.
	return nil
}

func (m *ExternalTestResource) Normalize(v *InternalTestResource) {
	// FIXME Implement if there's a need for it in tests.
}

// Must is a helper function that takes a value and error, returns the value if no error occurred,
// or panics if an error occurred. This is useful for test setup where we don't expect errors.
func Must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
