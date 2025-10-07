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
	"testing"

	"github.com/Azure/ARO-HCP/internal/api"
)

func TestClusterVisibilityMap(t *testing.T) {
	// This should include any clusterVisibilityMap
	// overrides from the package's init() function.
	expectedVisibility := map[string]api.VisibilityFlags{
		"ID":                                          api.VisibilityRead | api.VisibilityCaseInsensitive,
		"Name":                                        api.VisibilityRead | api.VisibilityCaseInsensitive,
		"Type":                                        api.VisibilityRead | api.VisibilityCaseInsensitive,
		"SystemData":                                  api.SkipVisibilityTest,
		"SystemData.CreatedBy":                        api.VisibilityRead,
		"SystemData.CreatedByType":                    api.VisibilityRead,
		"SystemData.CreatedAt":                        api.VisibilityRead,
		"SystemData.LastModifiedBy":                   api.VisibilityRead,
		"SystemData.LastModifiedByType":               api.VisibilityRead,
		"SystemData.LastModifiedAt":                   api.VisibilityRead,
		"Location":                                    api.VisibilityRead | api.VisibilityCreate | api.VisibilityCaseInsensitive,
		"Tags":                                        api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate | api.VisibilityNullable,
		"Properties":                                  api.SkipVisibilityTest,
		"Properties.ProvisioningState":                api.VisibilityRead,
		"Properties.Version":                          api.SkipVisibilityTest,
		"Properties.Version.ID":                       api.VisibilityRead | api.VisibilityCreate,
		"Properties.Version.ChannelGroup":             api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate,
		"Properties.DNS":                              api.SkipVisibilityTest,
		"Properties.DNS.BaseDomain":                   api.VisibilityRead,
		"Properties.DNS.BaseDomainPrefix":             api.VisibilityRead | api.VisibilityCreate,
		"Properties.Network":                          api.SkipVisibilityTest,
		"Properties.Network.NetworkType":              api.VisibilityRead | api.VisibilityCreate,
		"Properties.Network.PodCIDR":                  api.VisibilityRead | api.VisibilityCreate,
		"Properties.Network.ServiceCIDR":              api.VisibilityRead | api.VisibilityCreate,
		"Properties.Network.MachineCIDR":              api.VisibilityRead | api.VisibilityCreate,
		"Properties.Network.HostPrefix":               api.VisibilityRead | api.VisibilityCreate,
		"Properties.Console":                          api.SkipVisibilityTest,
		"Properties.Console.URL":                      api.VisibilityRead,
		"Properties.API":                              api.SkipVisibilityTest,
		"Properties.API.URL":                          api.SkipVisibilityTest,
		"Properties.API.Visibility":                   api.VisibilityRead | api.VisibilityCreate,
		"Properties.API.AuthorizedCIDRs":              api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate | api.VisibilityNullable,
		"Properties.Platform":                         api.SkipVisibilityTest,
		"Properties.Platform.ManagedResourceGroup":    api.VisibilityRead | api.VisibilityCreate,
		"Properties.Platform.SubnetID":                api.VisibilityRead | api.VisibilityCreate,
		"Properties.Platform.OutboundType":            api.VisibilityRead | api.VisibilityCreate,
		"Properties.Platform.NetworkSecurityGroupID":  api.VisibilityRead | api.VisibilityCreate,
		"Properties.Platform.OperatorsAuthentication": api.SkipVisibilityTest,
		"Properties.Platform.OperatorsAuthentication.UserAssignedIdentities":                        api.SkipVisibilityTest,
		"Properties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators":  api.VisibilityRead | api.VisibilityCreate,
		"Properties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators":     api.VisibilityRead | api.VisibilityCreate,
		"Properties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity": api.VisibilityRead | api.VisibilityCreate,
		"Properties.Platform.IssuerURL":                                          api.VisibilityRead,
		"Properties.Autoscaling":                                                 api.SkipVisibilityTest,
		"Properties.Autoscaling.MaxNodesTotal":                                   api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate,
		"Properties.Autoscaling.MaxPodGracePeriodSeconds":                        api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate,
		"Properties.Autoscaling.MaxNodeProvisionTimeSeconds":                     api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate,
		"Properties.Autoscaling.PodPriorityThreshold":                            api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate,
		"Properties.NodeDrainTimeoutMinutes":                                     api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate,
		"Properties.ClusterImageRegistry":                                        api.VisibilityRead | api.VisibilityCreate,
		"Properties.ClusterImageRegistry.State":                                  api.VisibilityRead | api.VisibilityCreate,
		"Properties.Etcd":                                                        api.SkipVisibilityTest,
		"Properties.Etcd.DataEncryption":                                         api.SkipVisibilityTest,
		"Properties.Etcd.DataEncryption.CustomerManaged":                         api.SkipVisibilityTest,
		"Properties.Etcd.DataEncryption.CustomerManaged.EncryptionType":          api.VisibilityRead | api.VisibilityCreate,
		"Properties.Etcd.DataEncryption.KeyManagementMode":                       api.VisibilityRead | api.VisibilityCreate,
		"Properties.Etcd.DataEncryption.CustomerManaged.Kms":                     api.SkipVisibilityTest,
		"Properties.Etcd.DataEncryption.CustomerManaged.Kms.ActiveKey":           api.SkipVisibilityTest,
		"Properties.Etcd.DataEncryption.CustomerManaged.Kms.ActiveKey.Name":      api.VisibilityRead | api.VisibilityCreate,
		"Properties.Etcd.DataEncryption.CustomerManaged.Kms.ActiveKey.VaultName": api.VisibilityRead | api.VisibilityCreate,
		"Properties.Etcd.DataEncryption.CustomerManaged.Kms.ActiveKey.Version":   api.VisibilityRead | api.VisibilityCreate,
		"Identity":                                    api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate | api.VisibilityNullable,
		"Identity.PrincipalID":                        api.VisibilityRead,
		"Identity.TenantID":                           api.VisibilityRead,
		"Identity.Type":                               api.SkipVisibilityTest,
		"Identity.UserAssignedIdentities":             api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate | api.VisibilityNullable,
		"Identity.UserAssignedIdentities.ClientID":    api.VisibilityRead,
		"Identity.UserAssignedIdentities.PrincipalID": api.VisibilityRead,
	}

	api.TestVersionedVisibilityMap[HcpOpenShiftCluster](t, clusterVisibilityMap, expectedVisibility)
}

func TestNodePoolVisibilityMap(t *testing.T) {
	// This should include any nodePoolVisibilityMap
	// overrides from the package's init() function.
	expectedVisibility := map[string]api.VisibilityFlags{
		"ID":                              api.VisibilityRead | api.VisibilityCaseInsensitive,
		"Name":                            api.VisibilityRead | api.VisibilityCaseInsensitive,
		"Type":                            api.VisibilityRead | api.VisibilityCaseInsensitive,
		"SystemData":                      api.SkipVisibilityTest,
		"SystemData.CreatedBy":            api.VisibilityRead,
		"SystemData.CreatedByType":        api.VisibilityRead,
		"SystemData.CreatedAt":            api.VisibilityRead,
		"SystemData.LastModifiedBy":       api.VisibilityRead,
		"SystemData.LastModifiedByType":   api.VisibilityRead,
		"SystemData.LastModifiedAt":       api.VisibilityRead,
		"Location":                        api.VisibilityRead | api.VisibilityCreate | api.VisibilityCaseInsensitive,
		"Tags":                            api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate | api.VisibilityNullable,
		"Properties":                      api.SkipVisibilityTest,
		"Properties.ProvisioningState":    api.VisibilityRead,
		"Properties.Version":              api.SkipVisibilityTest,
		"Properties.Version.ID":           api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate,
		"Properties.Version.ChannelGroup": api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate,
		"Properties.Platform":             api.SkipVisibilityTest,
		"Properties.Platform.SubnetID":    api.VisibilityRead | api.VisibilityCreate,
		"Properties.Platform.VMSize":      api.VisibilityRead | api.VisibilityCreate,
		"Properties.Platform.EnableEncryptionAtHost":        api.VisibilityRead | api.VisibilityCreate,
		"Properties.Platform.OSDisk":                        api.SkipVisibilityTest,
		"Properties.Platform.OSDisk.SizeGiB":                api.VisibilityRead | api.VisibilityCreate,
		"Properties.Platform.OSDisk.DiskStorageAccountType": api.VisibilityRead | api.VisibilityCreate,
		"Properties.Platform.OSDisk.EncryptionSetID":        api.VisibilityRead | api.VisibilityCreate,
		"Properties.Platform.OSDisk.Persistence":            api.VisibilityRead | api.VisibilityCreate,
		"Properties.Platform.AvailabilityZone":              api.VisibilityRead | api.VisibilityCreate,
		"Properties.Replicas":                               api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate,
		"Properties.AutoRepair":                             api.VisibilityRead | api.VisibilityCreate,
		"Properties.AutoScaling":                            api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate | api.VisibilityNullable,
		"Properties.AutoScaling.Min":                        api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate,
		"Properties.AutoScaling.Max":                        api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate,
		"Properties.Labels":                                 api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate | api.VisibilityNullable,
		"Properties.Labels.Key":                             api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate,
		"Properties.Labels.Value":                           api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate,
		"Properties.Taints":                                 api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate | api.VisibilityNullable,
		"Properties.Taints.Effect":                          api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate,
		"Properties.Taints.Key":                             api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate,
		"Properties.Taints.Value":                           api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate,
		"Properties.NodeDrainTimeoutMinutes":                api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate | api.VisibilityNullable,
		"Identity":                                          api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate | api.VisibilityNullable,
		"Identity.PrincipalID":                              api.VisibilityRead,
		"Identity.TenantID":                                 api.VisibilityRead,
		"Identity.Type":                                     api.SkipVisibilityTest,
		"Identity.UserAssignedIdentities":                   api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate | api.VisibilityNullable,
		"Identity.UserAssignedIdentities.ClientID":          api.VisibilityRead,
		"Identity.UserAssignedIdentities.PrincipalID":       api.VisibilityRead,
	}

	api.TestVersionedVisibilityMap[NodePool](t, nodePoolVisibilityMap, expectedVisibility)
}

func TestExternalAuthVisibilityMap(t *testing.T) {
	// This should include any externalAuthVisibilityMap
	// overrides from the package's init() function.
	expectedVisibility := map[string]api.VisibilityFlags{
		"ID":                                                           api.VisibilityRead | api.VisibilityCaseInsensitive,
		"Name":                                                         api.VisibilityRead | api.VisibilityCaseInsensitive,
		"Type":                                                         api.VisibilityRead | api.VisibilityCaseInsensitive,
		"SystemData":                                                   api.SkipVisibilityTest,
		"SystemData.CreatedBy":                                         api.VisibilityRead,
		"SystemData.CreatedByType":                                     api.VisibilityRead,
		"SystemData.CreatedAt":                                         api.VisibilityRead,
		"SystemData.LastModifiedBy":                                    api.VisibilityRead,
		"SystemData.LastModifiedByType":                                api.VisibilityRead,
		"SystemData.LastModifiedAt":                                    api.VisibilityRead,
		"Properties":                                                   api.SkipVisibilityTest,
		"Properties.ProvisioningState":                                 api.VisibilityRead,
		"Properties.Condition":                                         api.SkipVisibilityTest,
		"Properties.Condition.Type":                                    api.VisibilityRead,
		"Properties.Condition.Status":                                  api.VisibilityRead,
		"Properties.Condition.LastTransitionTime":                      api.VisibilityRead,
		"Properties.Condition.Reason":                                  api.VisibilityRead,
		"Properties.Condition.Message":                                 api.VisibilityRead,
		"Properties.Issuer":                                            api.SkipVisibilityTest,
		"Properties.Issuer.URL":                                        api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate,
		"Properties.Issuer.Audiences":                                  api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate | api.VisibilityNullable,
		"Properties.Issuer.CA":                                         api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate,
		"Properties.Clients":                                           api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate | api.VisibilityNullable,
		"Properties.Clients.Component":                                 api.SkipVisibilityTest,
		"Properties.Clients.Component.Name":                            api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate,
		"Properties.Clients.Component.AuthClientNamespace":             api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate,
		"Properties.Clients.ClientID":                                  api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate,
		"Properties.Clients.ExtraScopes":                               api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate | api.VisibilityNullable,
		"Properties.Clients.Type":                                      api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate,
		"Properties.Claim":                                             api.SkipVisibilityTest,
		"Properties.Claim.Mappings":                                    api.SkipVisibilityTest,
		"Properties.Claim.Mappings.Username":                           api.SkipVisibilityTest,
		"Properties.Claim.Mappings.Username.Claim":                     api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate,
		"Properties.Claim.Mappings.Username.Prefix":                    api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate,
		"Properties.Claim.Mappings.Username.PrefixPolicy":              api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate,
		"Properties.Claim.Mappings.Groups":                             api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate | api.VisibilityNullable,
		"Properties.Claim.Mappings.Groups.Claim":                       api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate,
		"Properties.Claim.Mappings.Groups.Prefix":                      api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate,
		"Properties.Claim.ValidationRules":                             api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate | api.VisibilityNullable,
		"Properties.Claim.ValidationRules.Type":                        api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate,
		"Properties.Claim.ValidationRules.RequiredClaim":               api.SkipVisibilityTest,
		"Properties.Claim.ValidationRules.RequiredClaim.Claim":         api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate,
		"Properties.Claim.ValidationRules.RequiredClaim.RequiredValue": api.VisibilityRead | api.VisibilityCreate | api.VisibilityUpdate,
	}

	api.TestVersionedVisibilityMap[ExternalAuth](t, externalAuthVisibilityMap, expectedVisibility)
}

func TestClusterNullPatch(t *testing.T) {
	api.TestVersionedNullPatch(t, func() api.VersionedCreatableResource[api.HCPOpenShiftCluster] {
		return versionedInterface.NewHCPOpenShiftCluster(api.MinimumValidClusterTestCase())
	})
}

func TestNodePoolNullPatch(t *testing.T) {
	api.TestVersionedNullPatch(t, func() api.VersionedCreatableResource[api.HCPOpenShiftClusterNodePool] {
		return versionedInterface.NewHCPOpenShiftClusterNodePool(api.MinimumValidNodePoolTestCase())
	})
}

func TestExternalAuthNullPatch(t *testing.T) {
	api.TestVersionedNullPatch(t, func() api.VersionedCreatableResource[api.HCPOpenShiftClusterExternalAuth] {
		return versionedInterface.NewHCPOpenShiftClusterExternalAuth(api.MinimumValidExternalAuthTestCase())
	})
}
