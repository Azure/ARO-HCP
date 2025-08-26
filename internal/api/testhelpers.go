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
	"maps"
	"path"
	"reflect"
	"slices"
	"strings"
	"testing"

	"dario.cat/mergo"
	validator "github.com/go-playground/validator/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

func NewTestValidator() *validator.Validate {
	validate := NewValidator()

	validate.RegisterAlias("enum_clusterimageregistryprofilestate", EnumValidateTag(
		ClusterImageRegistryProfileStateEnabled,
		ClusterImageRegistryProfileStateDisabled,
	))
	validate.RegisterAlias("enum_customermanagedencryptiontype", EnumValidateTag(
		CustomerManagedEncryptionTypeKMS,
	))
	validate.RegisterAlias("enum_diskstorageaccounttype", EnumValidateTag(
		DiskStorageAccountTypePremium_LRS,
		DiskStorageAccountTypeStandardSSD_LRS,
		DiskStorageAccountTypeStandard_LRS,
	))
	validate.RegisterAlias("enum_effect", EnumValidateTag(
		EffectNoExecute,
		EffectNoSchedule,
		EffectPreferNoSchedule,
	))
	validate.RegisterAlias("enum_etcddataencryptionkeymanagementmodetype", EnumValidateTag(
		EtcdDataEncryptionKeyManagementModeTypeCustomerManaged,
		EtcdDataEncryptionKeyManagementModeTypePlatformManaged,
	))
	validate.RegisterAlias("enum_externalauthclienttype", EnumValidateTag(
		ExternalAuthClientTypeConfidential,
		ExternalAuthClientTypePublic,
	))
	validate.RegisterAlias("enum_externalauthconditiontype", EnumValidateTag(
		ExternalAuthConditionTypeAvailable,
		ExternalAuthConditionTypeDegraded,
		ExternalAuthConditionTypeProgressing,
	))
	validate.RegisterAlias("enum_externalauthconditionstatustype", EnumValidateTag(
		ConditionStatusTypeFalse,
		ConditionStatusTypeTrue,
		ConditionStatusTypeUnknown,
	))
	validate.RegisterAlias("enum_managedserviceidentitytype", EnumValidateTag(
		arm.ManagedServiceIdentityTypeNone,
		arm.ManagedServiceIdentityTypeSystemAssigned,
		arm.ManagedServiceIdentityTypeSystemAssignedUserAssigned,
		arm.ManagedServiceIdentityTypeUserAssigned,
	))
	validate.RegisterAlias("enum_networktype", EnumValidateTag(
		NetworkTypeOVNKubernetes,
		NetworkTypeOther,
	))
	validate.RegisterAlias("enum_outboundtype", EnumValidateTag(
		OutboundTypeLoadBalancer,
	))
	validate.RegisterAlias("enum_tokenvalidationruletyperequiredclaim", EnumValidateTag(
		TokenValidationRuleTypeRequiredClaim,
	))
	validate.RegisterAlias("enum_usernameclaimprefixpolicy", EnumValidateTag(
		UsernameClaimPrefixPolicyPrefix,
		UsernameClaimPrefixPolicyNoPrefix,
		UsernameClaimPrefixPolicyNone,
	))
	validate.RegisterAlias("enum_visibility", EnumValidateTag(
		VisibilityPublic,
		VisibilityPrivate,
	))

	return validate
}

func NewTestUserAssignedIdentity(name string) string {
	return path.Join(TestResourceGroupResourceID, "providers", "Microsoft.ManagedIdentity", "userAssignedIdentities", name)
}

func MinimumValidClusterTestCase() *HCPOpenShiftCluster {
	resource := NewDefaultHCPOpenShiftCluster()
	resource.ID = TestClusterResourceID
	resource.Name = TestClusterName
	resource.Type = ClusterResourceType.String()
	resource.Properties.Platform.ManagedResourceGroup = TestManagedResourceGroupName
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
	resource.ID = TestNodePoolResourceID
	resource.Name = TestNodePoolName
	resource.Type = NodePoolResourceType.String()
	resource.Properties.Platform.VMSize = "Standard_D8s_v3"
	return resource
}

func NodePoolTestCase(t *testing.T, tweaks *HCPOpenShiftClusterNodePool) *HCPOpenShiftClusterNodePool {
	nodePool := MinimumValidNodePoolTestCase()
	require.NoError(t, mergo.Merge(nodePool, tweaks, mergo.WithOverride))
	return nodePool
}

func MinimumValidExternalAuthTestCase() *HCPOpenShiftClusterExternalAuth {
	resource := NewDefaultHCPOpenShiftClusterExternalAuth()
	resource.ID = TestExternalAuthResourceID
	resource.Name = TestExternalAuthName
	resource.Type = ExternalAuthResourceType.String()
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
var testResourceVisibilityMap = NewVisibilityMap[InternalTestResource]()

func (m *ExternalTestResource) Normalize(v *InternalTestResource) {
	// FIXME Implement if there's a need for it in tests.
}

func (m *ExternalTestResource) GetVisibility(path string) (VisibilityFlags, bool) {
	flags, ok := testResourceVisibilityMap[path]
	return flags, ok
}

func (m *ExternalTestResource) ValidateVisibility(current VersionedCreatableResource[InternalTestResource], updating bool) []arm.CloudErrorBody {
	var structTagMap = GetStructTagMap[InternalTestResource]()
	return ValidateVisibility(m, current.(*ExternalTestResource), testResourceVisibilityMap, structTagMap, updating)
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

// SkipVisibilityTest is for fields that are not validated but
// may set a default visibility value for its descendant fields.
const SkipVisibilityTest = VisibilityFlags(0)

// TestVersionedVisibilityMap is a reusable test that versioned APIs can use
// to verify their expected field visibilities against their VisibilityMaps,
// which may include version-specific overrides.
func TestVersionedVisibilityMap[T any](t *testing.T, actualVisibility VisibilityMap, expectedVisibility VisibilityMap) {
	// Ensure the VisibilityMap keys are in agreement with generated field names.
	assert.Equal(t,
		slices.Sorted(maps.Keys(GetStructTagMap[T]())),
		slices.Sorted(maps.Keys(actualVisibility)),
		"Discrepancies exist between the generated model and its VisibilityMap")

	checklist := maps.Clone(actualVisibility)

	for path, expectedFlags := range expectedVisibility {
		t.Run(path, func(t *testing.T) {
			actualFlags := actualVisibility[path]
			if expectedFlags == SkipVisibilityTest {
				// Skipped cases should not be nullable.
				assert.False(t, actualFlags.IsNullable(), "%s is nullable and should not be skipped", path)
			} else {
				assert.Equalf(t, expectedFlags, actualFlags, "%s: expected %q, actual %q", path, expectedFlags, actualFlags)
			}
			delete(checklist, path)
		})
	}

	// Make sure expectedVisibility didn't miss any fields.
	assert.Empty(t, checklist)
}
