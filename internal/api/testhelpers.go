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
	"io"
	"log/slog"
	"maps"
	"net/http"
	"path"
	"reflect"
	"slices"
	"strings"
	"testing"

	"dario.cat/mergo"
	validator "github.com/go-playground/validator/v10"
	"github.com/google/go-cmp/cmp"
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
	validate.RegisterAlias("enum_persistence", EnumValidateTag(
		PersistenceTypePersistent, PersistenceTypeEphemeral))

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

// TestVersionedNullPatch is a reusable test that versioned APIs can use
// to verify the validation result of attempting to patch each and every
// field of a resource type to null. The test uses the resource type's
// StructTagMap to generate test cases.
func TestVersionedNullPatch[T any](t *testing.T, newResource func() VersionedCreatableResource[T]) {
	var buildJSONMergePatch func(reflect.Type, []string, string) string

	structTagMap := GetStructTagMap[T]()

	// Helper function unique to this test.
	getJSONSegments := func(path string) []string {
		var jsonSegments []string

		pathSegments := strings.Split(path, ".")

		for i := range pathSegments {
			mapKey := strings.Join(pathSegments[:i+1], ".")

			structTag := structTagMap[mapKey]
			jsonTagName := GetJSONTagName(structTag)
			// Embedded structs will not have a json tag.
			if jsonTagName != "" {
				jsonSegments = append(jsonSegments, jsonTagName)
			}
		}

		return jsonSegments
	}

	// Recursive helper function unique to this test.
	buildJSONMergePatch = func(rt reflect.Type, jsonSegments []string, path string) string {
		if len(jsonSegments) == 0 {
			return "null"
		}

		switch rt.Kind() {
		case reflect.Map:
			// This should be the final segment.
			require.Equal(t, len(jsonSegments), 1)
			return fmt.Sprintf("{ %q: null }", jsonSegments[0])

		case reflect.Pointer:
			return buildJSONMergePatch(rt.Elem(), jsonSegments, path)

		case reflect.Slice:
			return fmt.Sprintf("[ %s ]", buildJSONMergePatch(rt.Elem(), jsonSegments, path))

		case reflect.Struct:
			for i := 0; i < rt.NumField(); i++ {
				field := rt.Field(i)

				if field.Anonymous {
					// Anonymous fields will not appear in jsonSegments,
					// so we try recursing on it and see if we get a result.
					// If not, keep looping over the remaining struct fields.
					candidate := buildJSONMergePatch(field.Type, jsonSegments, path)
					if candidate != "" {
						return candidate
					}
				} else {
					subpath := join(path, field.Name)
					jsonTagName := GetJSONTagName(structTagMap[subpath])
					require.NotEmptyf(t, jsonTagName, "No JSON tag for %q", subpath)
					if jsonTagName == jsonSegments[0] {
						mergePatch := buildJSONMergePatch(field.Type, jsonSegments[1:], subpath)
						return fmt.Sprintf("{ %q: %s }", jsonTagName, mergePatch)
					}
				}
			}

		default:
			t.Fatalf("Unhandled type %q (%q)", rt.Name(), rt.Kind())
		}

		t.Fatalf("Failed buildJSONMergePatch at %q (type %s)", strings.Join(jsonSegments, "."), rt)

		return "" // just to make the compiler happy
	}

	for path := range structTagMap {
		var omitZero func(reflect.Value, string)
		var errorPath = path

		// Recursive helper function unique to this test.
		//
		// This modifies the value as though the "omitzero" JSON marshalling
		// option had been applied. Autorest-generated models do not use nor
		// mimic "omitzero" in their custom MarshalJSON methods but Version
		// interface methods like NewHCPOpenShiftCluster do.
		//
		// If a modification is made, it also sets errorPath to the expected
		// field path should this test case fail visibility validation.
		//
		// Why this works is best explained with an example.
		//
		// If we have a struct with a single non-zero default value like
		//
		//   &generated.HcpOpenShiftCluster{
		//       Properties: &generated.HcpOpenShiftClusterProperties{
		//           API: &generated.APIProfile{
		//               Visibility: &"Public",
		//           },
		//       },
		//   }
		//
		// then a PATCH request containing
		//
		//   { "properties": { "api": { "visibility": null } } }
		//
		// will cause the API field to change to
		//
		//   API: &generated.APIProfile{}
		//
		// which is the zero-value for type generated.APIProfile.
		//
		// This function will then reduce it further to
		//
		//   API: nil
		//
		// Because the API pointer value has changed from non-nil to nil,
		// and because the API field does not have the VisibilityNullable
		// flag, we expect the validation error to occur here:
		//
		//   {
		//       code: "InvalidRequestContent",
		//       message: "Field 'api' cannot be removed",
		//       target: "properties.api"
		//   }
		//
		// Thus, errorPath is set to "Properties.API" (and later converted
		// to use JSON field names).
		//
		omitZero = func(rv reflect.Value, path string) {
			switch rv.Kind() {
			case reflect.Pointer:
				if !rv.IsNil() {
					omitZero(rv.Elem(), path)
				}
				if !rv.IsNil() && rv.Elem().IsZero() {
					// Avoid trying to set the resource itself to nil
					// if it doesn't have any non-zero default values.
					if len(path) > 0 {
						rv.SetZero()
						errorPath = path
					}
				}

			case reflect.Map:
				iter := rv.MapRange()
				for iter.Next() {
					omitZero(rv.MapIndex(iter.Key()), path)
				}
				if !rv.IsZero() && rv.Len() == 0 {
					rv.SetZero()
					errorPath = path
				}

			case reflect.Slice:
				for i := 0; i < rv.Len(); i++ {
					omitZero(rv.Index(i), path)
				}
				if rv.Type().Elem().Kind() == reflect.Pointer {
					// Clear the slice of nil pointers.
					for i := 0; i < rv.Len(); {
						if rv.Index(i).IsNil() {
							rv.Set(reflect.AppendSlice(rv.Slice(0, i), rv.Slice(i+1, rv.Len())))
						} else {
							i++
						}
					}
				}
				if !rv.IsZero() && rv.Len() == 0 {
					rv.SetZero()
					errorPath = path
				}

			case reflect.Struct:
				for i := 0; i < rv.NumField(); i++ {
					var subpath string

					field := rv.Type().Field(i)
					if field.Anonymous {
						subpath = path
					} else {
						subpath = join(path, field.Name)
					}

					omitZero(rv.Field(i), subpath)
				}
			}
		}

		t.Run(path, func(t *testing.T) {
			var expectErrors []arm.CloudErrorBody

			curVal := newResource()
			newVal := newResource()

			request, err := http.NewRequest(http.MethodPatch, "http://localhost", nil)
			require.NoError(t, err)

			// Build a JSON merge patch that sets path to null.
			mergePatch := buildJSONMergePatch(reflect.TypeOf(newVal), getJSONSegments(path), "")

			require.Nil(t, ApplyRequestBody(request, []byte(mergePatch), newVal))

			// Modify the new value as if the "omitzero"
			// JSON marshaling option had been applied.
			rv := reflect.ValueOf(newVal)
			omitZero(rv, "")
			newVal, _ = rv.Interface().(VersionedCreatableResource[T])

			actualErrors := newVal.ValidateVisibility(curVal, true)

			flags, ok := newVal.GetVisibility(path)
			require.True(t, ok)

			if !reflect.DeepEqual(newVal, curVal) && !flags.IsNullable() {
				jsonSegments := getJSONSegments(errorPath)
				expectErrors = append(expectErrors, arm.CloudErrorBody{
					Code:    arm.CloudErrorCodeInvalidRequestContent,
					Message: fmt.Sprintf("Field '%s' cannot be removed", jsonSegments[len(jsonSegments)-1]),
					Target:  strings.Join(jsonSegments, "."),
				})
			}

			if !assert.Equal(t, expectErrors, actualErrors) {
				t.Logf("mergePatch: %s", mergePatch)
				t.Log(cmp.Diff(curVal, newVal))
			}
		})
	}
}
