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
	"reflect"
	"strings"
	"testing"

	"dario.cat/mergo"
	validator "github.com/go-playground/validator/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// The definitions in this file are meant for unit tests.

const (
	TestAPIVersion               = "2024-06-10-preview"
	TestTenantID                 = "00000000-0000-0000-0000-000000000000"
	TestSubscriptionID           = "11111111-1111-1111-1111-111111111111"
	TestAltSubscriptionID        = "22222222-2222-2222-2222-222222222222"
	TestResourceGroupName        = "testResourceGroup"
	TestClusterName              = "testCluster"
	TestNodePoolName             = "testNodePool"
	TestDeploymentName           = "testDeployment"
	TestNetworkSecurityGroupName = "testNetworkSecurityGroup"
	TestVirtualNetworkName       = "testVirtualNetwork"
	TestSubnetName               = "testSubnet"
)

var (
	TestSubscriptionResourceID         = path.Join("/subscriptions", TestSubscriptionID)
	TestResourceGroupResourceID        = path.Join(TestSubscriptionResourceID, "resourceGroups", TestResourceGroupName)
	TestClusterResourceID              = path.Join(TestResourceGroupResourceID, "providers", ProviderNamespace, ClusterResourceTypeName, TestClusterName)
	TestNodePoolResourceID             = path.Join(TestClusterResourceID, NodePoolResourceTypeName, TestNodePoolName)
	TestDeploymentResourceID           = path.Join(TestResourceGroupResourceID, "providers", ProviderNamespace, "deployments", TestDeploymentName)
	TestNetworkSecurityGroupResourceID = path.Join(TestResourceGroupResourceID, "providers", "Microsoft.Network", "networkSecurityGroups", TestNetworkSecurityGroupName)
	TestVirtualNetworkResourceID       = path.Join(TestResourceGroupResourceID, "providers", "Microsoft.Network", "virtualNetworks", TestVirtualNetworkName)
	TestSubnetResourceID               = path.Join(TestVirtualNetworkResourceID, "subnets", TestSubnetName)
)

func NewTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func NewTestValidator() *validator.Validate {
	validate := NewValidator()

	validate.RegisterAlias("enum_diskstorageaccounttype", EnumValidateTag(
		DiskStorageAccountTypePremium_LRS,
		DiskStorageAccountTypeStandardSSD_LRS,
		DiskStorageAccountTypeStandard_LRS))
	validate.RegisterAlias("enum_effect", EnumValidateTag(
		EffectNoExecute,
		EffectNoSchedule,
		EffectPreferNoSchedule))
	validate.RegisterAlias("enum_networktype", EnumValidateTag(
		NetworkTypeOVNKubernetes,
		NetworkTypeOther))
	validate.RegisterAlias("enum_outboundtype", EnumValidateTag(
		OutboundTypeLoadBalancer))
	validate.RegisterAlias("enum_visibility", EnumValidateTag(
		VisibilityPublic,
		VisibilityPrivate))
	validate.RegisterAlias("enum_managedserviceidentitytype", EnumValidateTag(
		arm.ManagedServiceIdentityTypeNone,
		arm.ManagedServiceIdentityTypeSystemAssigned,
		arm.ManagedServiceIdentityTypeSystemAssignedUserAssigned,
		arm.ManagedServiceIdentityTypeUserAssigned))
	validate.RegisterAlias("enum_optionalclustercapability", EnumValidateTag(
		OptionalClusterCapabilityImageRegistry))

	return validate
}

func NewTestUserAssignedIdentity(name string) string {
	return path.Join(TestResourceGroupResourceID, "providers", "Microsoft.ManagedIdentity", "userAssignedIdentities", name)
}

func MinimumValidClusterTestCase() *HCPOpenShiftCluster {
	resource := NewDefaultHCPOpenShiftCluster()
	resource.Properties.Platform.SubnetID = TestSubnetResourceID
	resource.Properties.Platform.NetworkSecurityGroupID = TestNetworkSecurityGroupResourceID
	return resource
}

func ClusterTestCase(t *testing.T, tweaks *HCPOpenShiftCluster) *HCPOpenShiftCluster {
	resource := MinimumValidClusterTestCase()
	require.NoError(t, mergo.Merge(resource, tweaks, mergo.WithOverride))
	return resource
}

func MinimumValidNodePoolTestCase() *HCPOpenShiftClusterNodePool {
	resource := NewDefaultHCPOpenShiftClusterNodePool()
	resource.Properties.Platform.VMSize = "Standard_D8s_v3"
	return resource
}

func NodePoolTestCase(t *testing.T, tweaks *HCPOpenShiftClusterNodePool) *HCPOpenShiftClusterNodePool {
	nodePool := MinimumValidNodePoolTestCase()
	require.NoError(t, mergo.Merge(nodePool, tweaks, mergo.WithOverride))
	return nodePool
}

// AssertJSONPath ensures path is valid for struct type T by following
// its "json" struct tags. This is intended for validation errors where
// the Target field must be hard-coded to a JSON path.
func AssertJSONPath[T any](t *testing.T, path string) bool {
	t.Helper()

	structType := reflect.TypeFor[T]()
	pathSegments := strings.Split(path, ".")

	for depth, jsonTagName := range pathSegments {
		currentPath := strings.Join(pathSegments[:depth+1], ".")
		require.Equalf(t, reflect.Struct.String(), structType.Kind().String(), "Unexpected type at '%s'", currentPath)

		// Discard any subscript in the path segment.
		index := strings.Index(jsonTagName, "[")
		if index >= 0 {
			jsonTagName = jsonTagName[:index]
		}

		field, ok := structType.FieldByNameFunc(func(name string) bool {
			field, ok := structType.FieldByName(name)
			return ok && GetJSONTagName(field.Tag) == jsonTagName
		})
		if !assert.Truef(t, ok, "Invalid JSON path '%s'", currentPath) {
			return false
		}

		switch field.Type.Kind() {
		case reflect.Map, reflect.Pointer, reflect.Slice:
			structType = field.Type.Elem()
		default:
			structType = field.Type
		}
	}

	return true
}
