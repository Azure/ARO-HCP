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

package v20240610preview

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Azure/ARO-HCP/internal/api"
)

// skip is for fields that are not validated but may set
// a default visibility value for its descendant fields.
const skip = api.VisibilityFlags(0)

func getEffectiveVisibility(t *testing.T, structTagMap api.StructTagMap, path string) api.VisibilityFlags {
	t.Helper()

	parts := strings.Split(path, ".")

	for i := len(parts); i > 0; i-- {
		if tag, ok := structTagMap[strings.Join(parts[:i], ".")]; ok {
			if flags, ok := api.GetVisibilityFlags(tag); ok {
				return flags
			}
		}
	}

	return api.VisibilityDefault
}

func testStructTagMap(t *testing.T, structTagMap api.StructTagMap, expectedVisibility map[string]api.VisibilityFlags) {
	t.Helper()

	for path, expectedFlags := range expectedVisibility {
		if expectedFlags != skip {
			actualFlags := getEffectiveVisibility(t, structTagMap, path)
			assert.Equalf(t, expectedFlags, actualFlags, "%s: expected %q, actual %q", path, expectedFlags, actualFlags)
		}
	}

	// Make sure the StructTagMap was fully tested.
	for path := range expectedVisibility {
		delete(structTagMap, path)
	}
	assert.Empty(t, structTagMap)
}

func TestClusterStructTagMap(t *testing.T) {
	// This should include any clusterStructTagMap
	// overrides from the package's init() function.
	expectedVisibility := map[string]api.VisibilityFlags{
		"TrackedResource.Resource.ID":                                        api.VisibilityRead,
		"TrackedResource.Resource.Name":                                      api.VisibilityRead,
		"TrackedResource.Resource.Type":                                      api.VisibilityRead,
		"TrackedResource.Resource.SystemData":                                skip,
		"TrackedResource.Resource.SystemData.CreatedBy":                      api.VisibilityRead,
		"TrackedResource.Resource.SystemData.CreatedByType":                  api.VisibilityRead,
		"TrackedResource.Resource.SystemData.CreatedAt":                      api.VisibilityRead,
		"TrackedResource.Resource.SystemData.LastModifiedBy":                 api.VisibilityRead,
		"TrackedResource.Resource.SystemData.LastModifiedByType":             api.VisibilityRead,
		"TrackedResource.Resource.SystemData.LastModifiedAt":                 api.VisibilityRead,
		"TrackedResource.Location":                                           api.VisibilityRead | api.VisibilityCreate,
		"TrackedResource.Tags":                                               api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate,
		"Properties":                                                         skip,
		"Properties.ProvisioningState":                                       api.VisibilityRead,
		"Properties.Version":                                                 skip,
		"Properties.Version.ID":                                              api.VisibilityRead | api.VisibilityCreate,
		"Properties.Version.ChannelGroup":                                    api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate,
		"Properties.Version.AvailableUpgrades":                               api.VisibilityRead,
		"Properties.DNS":                                                     skip,
		"Properties.DNS.BaseDomain":                                          api.VisibilityRead,
		"Properties.DNS.BaseDomainPrefix":                                    api.VisibilityRead | api.VisibilityCreate,
		"Properties.Network":                                                 skip,
		"Properties.Network.NetworkType":                                     api.VisibilityRead | api.VisibilityCreate,
		"Properties.Network.PodCIDR":                                         api.VisibilityRead | api.VisibilityCreate,
		"Properties.Network.ServiceCIDR":                                     api.VisibilityRead | api.VisibilityCreate,
		"Properties.Network.MachineCIDR":                                     api.VisibilityRead | api.VisibilityCreate,
		"Properties.Network.HostPrefix":                                      api.VisibilityRead | api.VisibilityCreate,
		"Properties.Console":                                                 skip,
		"Properties.Console.URL":                                             api.VisibilityRead,
		"Properties.API":                                                     skip,
		"Properties.API.URL":                                                 skip,
		"Properties.API.Visibility":                                          api.VisibilityRead | api.VisibilityCreate,
		"Properties.Platform":                                                skip,
		"Properties.Platform.ManagedResourceGroup":                           api.VisibilityRead | api.VisibilityCreate,
		"Properties.Platform.SubnetID":                                       api.VisibilityRead | api.VisibilityCreate,
		"Properties.Platform.OutboundType":                                   api.VisibilityRead | api.VisibilityCreate,
		"Properties.Platform.NetworkSecurityGroupID":                         api.VisibilityRead | api.VisibilityCreate,
		"Properties.Platform.OperatorsAuthentication":                        skip,
		"Properties.Platform.OperatorsAuthentication.UserAssignedIdentities": skip,
		"Properties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators":  api.VisibilityRead | api.VisibilityCreate,
		"Properties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators":     api.VisibilityRead | api.VisibilityCreate,
		"Properties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity": api.VisibilityRead | api.VisibilityCreate,
		"Properties.Platform.IssuerURL":               api.VisibilityRead,
		"Properties.Capabilities":                     skip,
		"Properties.Capabilities.Disabled":            api.VisibilityRead | api.VisibilityCreate,
		"Identity":                                    skip,
		"Identity.PrincipalID":                        api.VisibilityRead,
		"Identity.TenantID":                           api.VisibilityRead,
		"Identity.Type":                               skip,
		"Identity.UserAssignedIdentities":             skip,
		"Identity.UserAssignedIdentities.ClientID":    api.VisibilityRead,
		"Identity.UserAssignedIdentities.PrincipalID": api.VisibilityRead,
	}

	testStructTagMap(t, clusterStructTagMap, expectedVisibility)
}

func TestNodePoolStructTagMap(t *testing.T) {
	// This should include any nodePoolStructTagMap
	// overrides from the package's init() function.
	expectedVisibility := map[string]api.VisibilityFlags{
		"TrackedResource.Resource.ID":                            api.VisibilityRead,
		"TrackedResource.Resource.Name":                          api.VisibilityRead,
		"TrackedResource.Resource.Type":                          api.VisibilityRead,
		"TrackedResource.Resource.SystemData":                    skip,
		"TrackedResource.Resource.SystemData.CreatedBy":          api.VisibilityRead,
		"TrackedResource.Resource.SystemData.CreatedByType":      api.VisibilityRead,
		"TrackedResource.Resource.SystemData.CreatedAt":          api.VisibilityRead,
		"TrackedResource.Resource.SystemData.LastModifiedBy":     api.VisibilityRead,
		"TrackedResource.Resource.SystemData.LastModifiedByType": api.VisibilityRead,
		"TrackedResource.Resource.SystemData.LastModifiedAt":     api.VisibilityRead,
		"TrackedResource.Location":                               api.VisibilityRead | api.VisibilityCreate,
		"TrackedResource.Tags":                                   api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate,
		"Properties":                                             skip,
		"Properties.ProvisioningState":                           api.VisibilityRead,
		"Properties.Version":                                     skip,
		"Properties.Version.ID":                                  api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate,
		"Properties.Version.ChannelGroup":                        api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate,
		"Properties.Version.AvailableUpgrades":                   api.VisibilityRead,
		"Properties.Platform":                                    skip,
		"Properties.Platform.ManagedResourceGroup":               api.VisibilityRead | api.VisibilityCreate,
		"Properties.Platform.SubnetID":                           api.VisibilityRead | api.VisibilityCreate,
		"Properties.Platform.VMSize":                             api.VisibilityRead | api.VisibilityCreate,
		"Properties.Platform.DiskSizeGiB":                        api.VisibilityRead | api.VisibilityCreate,
		"Properties.Platform.DiskStorageAccountType":             api.VisibilityRead | api.VisibilityCreate,
		"Properties.Platform.AvailabilityZone":                   api.VisibilityRead | api.VisibilityCreate,
		"Properties.Replicas":                                    api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate,
		"Properties.AutoRepair":                                  api.VisibilityRead | api.VisibilityCreate,
		"Properties.AutoScaling":                                 skip,
		"Properties.AutoScaling.Min":                             api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate,
		"Properties.AutoScaling.Max":                             api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate,
		"Properties.Labels":                                      api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate,
		"Properties.Taints":                                      skip,
		"Properties.Taints.Effect":                               api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate,
		"Properties.Taints.Key":                                  api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate,
		"Properties.Taints.Value":                                api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate,
	}

	testStructTagMap(t, nodePoolStructTagMap, expectedVisibility)
}
