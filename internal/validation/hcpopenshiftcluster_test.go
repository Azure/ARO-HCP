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

package validation

import (
	"context"
	"path"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/operation"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

var (
	managedIdentity1 = api.NewTestUserAssignedIdentity("myManagedIdentity1")
	managedIdentity2 = api.NewTestUserAssignedIdentity("myManagedIdentity2")
	managedIdentity3 = api.NewTestUserAssignedIdentity("myManagedIdentity3")
)

func TestClusterRequired(t *testing.T) {
	tests := []struct {
		name         string
		resource     *api.HCPOpenShiftCluster
		tweaks       *api.HCPOpenShiftCluster
		opOptions    []string
		expectErrors []utils.ExpectedError
	}{
		{
			name:     "Empty cluster",
			resource: &api.HCPOpenShiftCluster{},
			expectErrors: []utils.ExpectedError{
				{
					Message:   "Required value",
					FieldPath: "trackedResource.resource.id",
				},
				{
					Message:   "Required value",
					FieldPath: "trackedResource.resource.systemData",
				},
				{
					Message:   "Required value",
					FieldPath: "trackedResource.location",
				},
				{
					Message:   "Required value",
					FieldPath: "customerProperties.version.channelGroup",
				},
				{
					Message:   "Unsupported value",
					FieldPath: "customerProperties.version.channelGroup",
				},
				{
					Message:   "Required value",
					FieldPath: "customerProperties.version.id",
				},
				{
					Message:   "Required value",
					FieldPath: "customerProperties.network.networkType",
				},
				{
					Message:   "Unsupported value",
					FieldPath: "customerProperties.network.networkType",
				},
				{
					Message:   "Required value",
					FieldPath: "customerProperties.network.podCidr",
				},
				{
					Message:   "Required value",
					FieldPath: "customerProperties.network.serviceCidr",
				},
				{
					Message:   "Required value",
					FieldPath: "customerProperties.network.machineCidr",
				},
				{
					Message:   "Required value",
					FieldPath: "customerProperties.network.hostPrefix",
				},
				{
					Message:   "must be greater than or equal to 23",
					FieldPath: "customerProperties.network.hostPrefix",
				},
				{
					Message:   "Required value",
					FieldPath: "customerProperties.api.visibility",
				},
				{
					Message:   "Unsupported value",
					FieldPath: "customerProperties.api.visibility",
				},
				{
					Message:   "Required value",
					FieldPath: "customerProperties.platform.managedResourceGroup",
				},
				{
					Message:   "Required value",
					FieldPath: "customerProperties.platform.subnetId",
				},
				{
					Message:   "Required value",
					FieldPath: "customerProperties.platform.outboundType",
				},
				{
					Message:   "Unsupported value",
					FieldPath: "customerProperties.platform.outboundType",
				},
				{
					Message:   "Required value",
					FieldPath: "customerProperties.platform.networkSecurityGroupId",
				},
				{
					Message:   "Required value",
					FieldPath: "customerProperties.autoscaling.maxPodGracePeriodSeconds",
				},
				{
					Message:   "Invalid value: 0: must be greater than or equal to 1",
					FieldPath: "customerProperties.autoscaling.maxPodGracePeriodSeconds",
				},
				{
					Message:   "Required value",
					FieldPath: "customerProperties.autoscaling.maxNodeProvisionTimeSeconds",
				},
				{
					Message:   "Invalid value: 0: must be greater than or equal to 1",
					FieldPath: "customerProperties.autoscaling.maxNodeProvisionTimeSeconds",
				},
				{
					Message:   "Required value",
					FieldPath: "customerProperties.autoscaling.podPriorityThreshold",
				},
				{
					Message:   "Required value",
					FieldPath: "customerProperties.etcd.dataEncryption.keyManagementMode",
				},
				{
					Message:   "Unsupported value",
					FieldPath: "customerProperties.etcd.dataEncryption.keyManagementMode",
				},
				{
					Message:   "Required value",
					FieldPath: "customerProperties.clusterImageRegistry.state",
				},
				{
					Message:   "Unsupported value",
					FieldPath: "customerProperties.clusterImageRegistry.state",
				},
				{
					Message:   "Required value",
					FieldPath: "serviceProviderProperties.managedIdentitiesDataPlaneIdentityURL",
				},
				{
					Message:   "Required value",
					FieldPath: "serviceProviderProperties.clusterUID",
				},
			},
		},
		{
			name: "Default cluster",
			resource: api.NewDefaultHCPOpenShiftCluster(
				api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster")),
				api.TestLocation,
			),
			expectErrors: []utils.ExpectedError{
				{
					Message:   "Required value",
					FieldPath: "trackedResource.resource.systemData",
				},
				{
					Message:   "Required value",
					FieldPath: "customerProperties.version.id",
				},
				{
					Message:   "Required value",
					FieldPath: "customerProperties.platform.managedResourceGroup",
				},
				{
					Message:   "Required value",
					FieldPath: "customerProperties.platform.subnetId",
				},
				{
					Message:   "Required value",
					FieldPath: "customerProperties.platform.networkSecurityGroupId",
				},
				{
					Message:   "Required value",
					FieldPath: "serviceProviderProperties.managedIdentitiesDataPlaneIdentityURL",
				},
				{
					Message:   "Required value",
					FieldPath: "serviceProviderProperties.clusterUID",
				},
			},
		},
		{
			name:         "Minimum valid cluster",
			resource:     api.MinimumValidClusterTestCase(),
			expectErrors: []utils.ExpectedError{},
		},
		{
			name: "Cluster with identity",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Platform: api.CustomerPlatformProfile{
						OperatorsAuthentication: api.OperatorsAuthenticationProfile{
							UserAssignedIdentities: api.UserAssignedIdentitiesProfile{
								ControlPlaneOperators: map[string]*azcorearm.ResourceID{
									"operatorX": api.NewTestUserAssignedIdentity("MyManagedIdentity"),
								},
							},
						},
					},
				},
				Identity: &arm.ManagedServiceIdentity{
					Type: arm.ManagedServiceIdentityTypeUserAssigned,
					UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
						api.NewTestUserAssignedIdentity("MyManagedIdentity").String(): {},
					},
				},
			},
			expectErrors: []utils.ExpectedError{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resource := tt.resource
			if resource == nil {
				resource = api.ClusterTestCase(t, tt.tweaks)
			}

			op := operation.Operation{Type: operation.Create, Options: tt.opOptions}
			actualErrors := ValidateCluster(context.TODO(), op, resource, nil, nil)
			utils.VerifyErrorsMatch(t, tt.expectErrors, actualErrors)
		})
	}
}

func TestClusterValidate(t *testing.T) {
	// Note "required" validation tests are above.
	// This function tests all the other validators in use.
	tests := []struct {
		name         string
		resource     *api.HCPOpenShiftCluster
		tweaks       *api.HCPOpenShiftCluster
		opOptions    []string
		expectErrors []utils.ExpectedError
	}{
		{
			name:         "Minimum valid cluster",
			tweaks:       &api.HCPOpenShiftCluster{},
			expectErrors: []utils.ExpectedError{},
		},
		{
			name: "Bad cidrv4",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Network: api.NetworkProfile{
						PodCIDR: "Mmm... apple cider",
					},
				},
			},
			expectErrors: []utils.ExpectedError{
				{
					Message:   "invalid CIDR address",
					FieldPath: "customerProperties.network.podCidr",
				},
			},
		},
		{
			name: "Bad dns_rfc1035_label",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					DNS: api.CustomerDNSProfile{
						BaseDomainPrefix: "0badlabel",
					},
				},
			},
			expectErrors: []utils.ExpectedError{
				{
					Message:   "must be a valid DNS RFC 1035 label",
					FieldPath: "customerProperties.dns.baseDomainPrefix",
				},
			},
		},
		{
			name: "Bad enum_outboundtype",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Platform: api.CustomerPlatformProfile{
						OutboundType: "loadJuggler",
					},
				},
			},
			expectErrors: []utils.ExpectedError{
				{
					Message:   "supported values: \"LoadBalancer\"",
					FieldPath: "customerProperties.platform.outboundType",
				},
			},
		},
		{
			name: "Version ID is required",
			resource: func() *api.HCPOpenShiftCluster {
				r := api.MinimumValidClusterTestCase()
				r.CustomerProperties.Version.ID = ""
				return r
			}(),
			expectErrors: []utils.ExpectedError{
				{
					Message:   "Required value",
					FieldPath: "customerProperties.version.id",
				},
			},
		},
		{
			name: "Version ID with micro version is rejected without experimental flag",
			resource: func() *api.HCPOpenShiftCluster {
				r := api.MinimumValidClusterTestCase()
				r.CustomerProperties.Version.ID = "4.20.8"
				return r
			}(),
			expectErrors: []utils.ExpectedError{
				{
					Message:   "must be specified as MAJOR.MINOR",
					FieldPath: "customerProperties.version.id",
				},
			},
		},
		{
			name: "Version ID with micro version is allowed with experimental flag",
			resource: func() *api.HCPOpenShiftCluster {
				r := api.MinimumValidClusterTestCase()
				r.CustomerProperties.Version.ID = "4.20.8"
				return r
			}(),
			opOptions:    testFeatureOptions(api.FeatureExperimentalReleaseFeatures),
			expectErrors: []utils.ExpectedError{},
		},
		{
			name: "ChannelGroup candidate is rejected without experimental flag",
			resource: func() *api.HCPOpenShiftCluster {
				r := api.MinimumValidClusterTestCase()
				r.CustomerProperties.Version.ChannelGroup = "candidate"
				return r
			}(),
			expectErrors: []utils.ExpectedError{
				{
					Message:   "supported values: \"fast\", \"stable\"",
					FieldPath: "customerProperties.version.channelGroup",
				},
			},
		},
		{
			name: "ChannelGroup candidate is allowed with experimental flag",
			resource: func() *api.HCPOpenShiftCluster {
				r := api.MinimumValidClusterTestCase()
				r.CustomerProperties.Version.ChannelGroup = "candidate"
				return r
			}(),
			opOptions:    testFeatureOptions(api.FeatureExperimentalReleaseFeatures),
			expectErrors: []utils.ExpectedError{},
		},
		{
			name: "Version ID with prerelease is rejected without experimental flag",
			resource: func() *api.HCPOpenShiftCluster {
				r := api.MinimumValidClusterTestCase()
				r.CustomerProperties.Version.ID = "4.21.0-rc.1"
				return r
			}(),
			expectErrors: []utils.ExpectedError{
				{
					Message:   "must be specified as MAJOR.MINOR",
					FieldPath: "customerProperties.version.id",
				},
			},
		},
		{
			name: "Version ID with prerelease is allowed with experimental flag",
			resource: func() *api.HCPOpenShiftCluster {
				r := api.MinimumValidClusterTestCase()
				r.CustomerProperties.Version.ID = "4.21.0-rc.1"
				return r
			}(),
			opOptions:    testFeatureOptions(api.FeatureExperimentalReleaseFeatures),
			expectErrors: []utils.ExpectedError{},
		},
		{
			name: "Version ID with nightly format is allowed with experimental flag",
			resource: func() *api.HCPOpenShiftCluster {
				r := api.MinimumValidClusterTestCase()
				r.CustomerProperties.Version.ChannelGroup = "nightly"
				r.CustomerProperties.Version.ID = "4.21.0-0.nightly-2024-01-15-123456"
				return r
			}(),
			opOptions:    testFeatureOptions(api.FeatureExperimentalReleaseFeatures),
			expectErrors: []utils.ExpectedError{},
		},
		{
			name: "ChannelGroup fast is allowed without experimental flag",
			resource: func() *api.HCPOpenShiftCluster {
				r := api.MinimumValidClusterTestCase()
				r.CustomerProperties.Version.ChannelGroup = "fast"
				return r
			}(),
			expectErrors: []utils.ExpectedError{},
		},
		{
			name: "Version must be at least 4.20 without experimental flag",
			resource: func() *api.HCPOpenShiftCluster {
				r := api.MinimumValidClusterTestCase()
				r.CustomerProperties.Version.ID = "4.20"
				return r
			}(),
			expectErrors: []utils.ExpectedError{},
		},
		{
			name: "Version must be at least 4.19 with experimental flag",
			resource: func() *api.HCPOpenShiftCluster {
				r := api.MinimumValidClusterTestCase()
				r.CustomerProperties.Version.ID = "4.19"
				return r
			}(),
			opOptions:    testFeatureOptions(api.FeatureExperimentalReleaseFeatures),
			expectErrors: []utils.ExpectedError{},
		},
		{
			name: "ChannelGroup nightly is rejected without experimental flag",
			resource: func() *api.HCPOpenShiftCluster {
				r := api.MinimumValidClusterTestCase()
				r.CustomerProperties.Version.ChannelGroup = "nightly"
				return r
			}(),
			expectErrors: []utils.ExpectedError{
				{
					Message:   "supported values: \"fast\", \"stable\"",
					FieldPath: "customerProperties.version.channelGroup",
				},
			},
		},
		{
			name: "ChannelGroup nightly is allowed with experimental flag",
			resource: func() *api.HCPOpenShiftCluster {
				r := api.MinimumValidClusterTestCase()
				r.CustomerProperties.Version.ChannelGroup = "nightly"
				return r
			}(),
			opOptions:    testFeatureOptions(api.FeatureExperimentalReleaseFeatures),
			expectErrors: []utils.ExpectedError{},
		},
		{
			name: "ChannelGroup blah is rejected even with experimental flag",
			resource: func() *api.HCPOpenShiftCluster {
				r := api.MinimumValidClusterTestCase()
				r.CustomerProperties.Version.ChannelGroup = "blah"
				return r
			}(),
			opOptions: testFeatureOptions(api.FeatureExperimentalReleaseFeatures),
			expectErrors: []utils.ExpectedError{
				{
					Message:   "supported values: \"candidate\", \"fast\", \"nightly\", \"stable\"",
					FieldPath: "customerProperties.version.channelGroup",
				},
			},
		},
		{
			name: "Bad enum_visibility",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					API: api.CustomerAPIProfile{
						Visibility: "it's a secret to everybody",
					},
				},
			},
			expectErrors: []utils.ExpectedError{
				{
					Message:   "supported values: \"Private\", \"Public\"",
					FieldPath: "customerProperties.api.visibility",
				},
			},
		},
		{
			name: "Bad enum_managedserviceidentitytype",
			tweaks: &api.HCPOpenShiftCluster{
				Identity: &arm.ManagedServiceIdentity{
					Type: "brokenServiceType",
				},
			},
			expectErrors: []utils.ExpectedError{
				{
					Message:   "supported values: \"None\", \"SystemAssigned\", \"SystemAssigned,UserAssigned\", \"UserAssigned\"",
					FieldPath: "identity.state",
				},
			},
		},
		{
			name: "Bad enum_clusterimageregistryprofilestate",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					ClusterImageRegistry: api.ClusterImageRegistryProfile{
						State: api.ClusterImageRegistryState("not enabled"),
					},
				},
			},
			expectErrors: []utils.ExpectedError{
				{
					Message:   "supported values: \"Disabled\", \"Enabled\"",
					FieldPath: "customerProperties.clusterImageRegistry.state",
				},
			},
		},
		{
			name: "Base domain prefix is too long",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					DNS: api.CustomerDNSProfile{
						BaseDomainPrefix: "this-domain-is-too-long",
					},
				},
			},
			expectErrors: []utils.ExpectedError{
				{
					Message:   "may not be more than 15 bytes",
					FieldPath: "customerProperties.dns.baseDomainPrefix",
				},
			},
		},
		{
			name: "Host prefix is too small",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Network: api.NetworkProfile{
						HostPrefix: 22,
					},
				},
			},
			expectErrors: []utils.ExpectedError{
				{
					Message:   "must be greater than or equal to 23",
					FieldPath: "customerProperties.network.hostPrefix",
				},
			},
		},
		{
			name: "Host prefix is too large",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Network: api.NetworkProfile{
						HostPrefix: 27,
					},
				},
			},
			expectErrors: []utils.ExpectedError{
				{
					Message:   "must be less than or equal to 26",
					FieldPath: "customerProperties.network.hostPrefix",
				},
			},
		},
		{
			name: "Control plane operator name cannot be empty",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Platform: api.CustomerPlatformProfile{
						OperatorsAuthentication: api.OperatorsAuthenticationProfile{
							UserAssignedIdentities: api.UserAssignedIdentitiesProfile{
								ControlPlaneOperators: map[string]*azcorearm.ResourceID{
									"": managedIdentity1,
								},
							},
						},
					},
				},
			},
			expectErrors: []utils.ExpectedError{
				{
					Message:   "Required value",
					FieldPath: "customerProperties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators",
				},
				{
					Message:   "identity is not assigned to this resource",
					FieldPath: "customerProperties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators[]",
				},
			},
		},

		{
			name: "Data plane operator name cannot be empty",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Platform: api.CustomerPlatformProfile{
						OperatorsAuthentication: api.OperatorsAuthenticationProfile{
							UserAssignedIdentities: api.UserAssignedIdentitiesProfile{
								DataPlaneOperators: map[string]*azcorearm.ResourceID{
									"": managedIdentity1,
								},
							},
						},
					},
				},
			},
			expectErrors: []utils.ExpectedError{
				{
					Message:   "Required value",
					FieldPath: "customerProperties.platform.operatorsAuthentication.userAssignedIdentities.dataPlaneOperators",
				},
			},
		},
		{
			name: "Customer managed ETCD key management mode requires CustomerManaged fields",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Etcd: api.EtcdProfile{
						DataEncryption: api.EtcdDataEncryptionProfile{
							KeyManagementMode: api.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged,
						},
					},
				},
			},
			expectErrors: []utils.ExpectedError{
				{
					Message:   "must be specified when `keyManagementMode` is \"CustomerManaged\"",
					FieldPath: "customerProperties.etcd.dataEncryption.customerManaged",
				},
			},
		},
		{
			name: "Platform managed ETCD key management mode excludes CustomerManaged fields",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Etcd: api.EtcdProfile{
						DataEncryption: api.EtcdDataEncryptionProfile{
							KeyManagementMode: api.EtcdDataEncryptionKeyManagementModeTypePlatformManaged,
							CustomerManaged:   &api.CustomerManagedEncryptionProfile{},
						},
					},
				},
			},
			expectErrors: []utils.ExpectedError{
				{
					Message:   "may only be specified when `keyManagementMode` is \"CustomerManaged\"",
					FieldPath: "customerProperties.etcd.dataEncryption.customerManaged",
				},
				{
					Message:   "supported values: \"KMS\"",
					FieldPath: "customerProperties.etcd.dataEncryption.customerManaged.encryptionType",
				},
			},
		},
		{
			name: "Customer managed Key Management Service (KMS) requires Kms fields",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Etcd: api.EtcdProfile{
						DataEncryption: api.EtcdDataEncryptionProfile{
							KeyManagementMode: api.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged,
							CustomerManaged: &api.CustomerManagedEncryptionProfile{
								EncryptionType: api.CustomerManagedEncryptionTypeKMS,
							},
						},
					},
				},
			},
			expectErrors: []utils.ExpectedError{
				{
					Message:   "must be specified when `encryptionType` is \"KMS\"",
					FieldPath: "customerProperties.etcd.dataEncryption.customerManaged.kms",
				},
			},
		},
		{
			// FIXME Use a valid alternate EncryptionType once we have one.
			name: "Alternate customer managed ETCD encyption type excludes Kms fields",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Etcd: api.EtcdProfile{
						DataEncryption: api.EtcdDataEncryptionProfile{
							KeyManagementMode: api.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged,
							CustomerManaged: &api.CustomerManagedEncryptionProfile{
								EncryptionType: "Alternate",
								Kms:            &api.KmsEncryptionProfile{},
							},
						},
					},
				},
			},
			expectErrors: []utils.ExpectedError{
				{
					Message:   "supported values: \"KMS\"",
					FieldPath: "customerProperties.etcd.dataEncryption.customerManaged.encryptionType",
				},
				{
					Message:   "may only be specified when `encryptionType` is \"KMS\"",
					FieldPath: "customerProperties.etcd.dataEncryption.customerManaged.kms",
				},
				{
					Message:   "Required value",
					FieldPath: "customerProperties.etcd.dataEncryption.customerManaged.kms.activeKey.name",
				},
				{
					Message:   "Required value",
					FieldPath: "customerProperties.etcd.dataEncryption.customerManaged.kms.activeKey.vaultName",
				},
				{
					Message:   "Required value",
					FieldPath: "customerProperties.etcd.dataEncryption.customerManaged.kms.activeKey.version",
				},
				{
					Message:   "Required value",
					FieldPath: "customerProperties.etcd.dataEncryption.customerManaged.kms.visibility",
				},
				{
					Message:   "supported values: \"Private\", \"Public\"",
					FieldPath: "customerProperties.etcd.dataEncryption.customerManaged.kms.visibility",
				},
			},
		},

		//--------------------------------
		// Complex multi-field validation
		//--------------------------------

		{
			name: "Cluster with overlapping machine and service CIDRs",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Network: api.NetworkProfile{
						ServiceCIDR: "10.0.0.0/23",
						MachineCIDR: "10.0.0.0/16",
					},
				},
			},
			expectErrors: []utils.ExpectedError{
				{
					Message:   "machine CIDR '10.0.0.0/16' and service CIDR '10.0.0.0/23' overlap",
					FieldPath: "customerProperties.network",
				},
			},
		},
		{
			name: "Cluster with overlapping machine and pod CIDRs",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Network: api.NetworkProfile{
						PodCIDR:     "10.1.0.0/18",
						MachineCIDR: "10.1.0.0/23",
					},
				},
			},
			expectErrors: []utils.ExpectedError{
				{
					Message:   "machine CIDR '10.1.0.0/23' and pod CIDR '10.1.0.0/18' overlap",
					FieldPath: "customerProperties.network",
				},
			},
		},
		{
			name: "Cluster with overlapping service and pod CIDRs",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Network: api.NetworkProfile{
						PodCIDR:     "10.2.0.0/18",
						ServiceCIDR: "10.2.0.0/24",
					},
				},
			},
			expectErrors: []utils.ExpectedError{
				{
					Message:   "service CIDR '10.2.0.0/24' and pod CIDR '10.2.0.0/18' overlap",
					FieldPath: "customerProperties.network",
				},
			},
		},
		{
			name: "Cluster with invalid managed resource group",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Platform: api.CustomerPlatformProfile{
						ManagedResourceGroup: api.TestResourceGroupName,
						// Use a different resource group name to avoid a subnet ID error.
						SubnetID:                api.Must(azcorearm.ParseResourceID(path.Join("/subscriptions", api.TestSubscriptionID, "resourceGroups", "anotherResourceGroup", "providers", "Microsoft.Network", "virtualNetworks", api.TestVirtualNetworkName, "subnets", api.TestSubnetName))),
						VnetIntegrationSubnetID: api.Must(azcorearm.ParseResourceID(path.Join("/subscriptions", api.TestSubscriptionID, "resourceGroups", "anotherResourceGroup", "providers", "Microsoft.Network", "virtualNetworks", api.TestVirtualNetworkName, "subnets", api.TestVnetIntegrationSubnetName))),
					},
				},
			},
			expectErrors: []utils.ExpectedError{
				{
					Message:   "must not be the same resource group name",
					FieldPath: "customerProperties.platform.managedResourceGroup",
				},
			},
		},
		{
			name: "Cluster with invalid subnet ID",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Platform: api.CustomerPlatformProfile{
						ManagedResourceGroup:    "MRG",
						SubnetID:                api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/MRG/providers/Microsoft.Network/virtualNetworks/testVirtualNetwork/subnets/testSubnet")),
						VnetIntegrationSubnetID: api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/anotherResourceGroup/providers/Microsoft.Network/virtualNetworks/testVirtualNetwork/subnets/testVnetIntegrationSubnet")),
					},
				},
			},
			expectErrors: []utils.ExpectedError{
				{
					Message:   "must not be the same resource group name: \"MRG\"",
					FieldPath: "customerProperties.platform.subnetId",
				},
				{
					Message:   "must be in the same Azure subscription: \"11111111-1111-1111-1111-111111111111\"",
					FieldPath: "customerProperties.platform.subnetId",
				},
				{
					Message:   "must be in the same Azure subscription: \"11111111-1111-1111-1111-111111111111\"",
					FieldPath: "customerProperties.platform.vnetIntegrationSubnetId",
				},
			},
		},
		{
			name: "Cluster with differently-cased identities",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Platform: api.CustomerPlatformProfile{
						OperatorsAuthentication: api.OperatorsAuthenticationProfile{
							UserAssignedIdentities: api.UserAssignedIdentitiesProfile{
								ControlPlaneOperators: map[string]*azcorearm.ResourceID{
									"operatorX": api.Must(azcorearm.ParseResourceID(strings.ToLower(managedIdentity1.String()))),
								},
								ServiceManagedIdentity: api.Must(azcorearm.ParseResourceID(strings.ToLower(managedIdentity2.String()))),
							},
						},
					},
				},
				Identity: &arm.ManagedServiceIdentity{
					Type: arm.ManagedServiceIdentityTypeUserAssigned,
					UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
						strings.ToUpper(managedIdentity1.String()): {},
						strings.ToUpper(managedIdentity2.String()): {},
					},
				},
			},
		},
		{
			name: "Cluster with broken identities",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Platform: api.CustomerPlatformProfile{
						OperatorsAuthentication: api.OperatorsAuthenticationProfile{
							UserAssignedIdentities: api.UserAssignedIdentitiesProfile{
								ControlPlaneOperators: map[string]*azcorearm.ResourceID{
									"operatorX": managedIdentity1,
								},
								ServiceManagedIdentity: managedIdentity2,
							},
						},
					},
				},
				Identity: &arm.ManagedServiceIdentity{
					Type: arm.ManagedServiceIdentityTypeUserAssigned,
					UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
						managedIdentity3.String(): {},
					},
				},
			},
			expectErrors: []utils.ExpectedError{
				{
					Message:   "identity is not assigned to this resource",
					FieldPath: "customerProperties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators[operatorX]",
				},
				{
					Message:   "identity is assigned to this resource but not used",
					FieldPath: "identity.userAssignedIdentities",
				},
				{
					Message:   "identity is not assigned to this resource",
					FieldPath: "customerProperties.platform.operatorsAuthentication.userAssignedIdentities.serviceManagedIdentity",
				},
			},
		},
		{
			name: "Cluster with multiple identities",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Platform: api.CustomerPlatformProfile{
						OperatorsAuthentication: api.OperatorsAuthenticationProfile{
							UserAssignedIdentities: api.UserAssignedIdentitiesProfile{
								ControlPlaneOperators: map[string]*azcorearm.ResourceID{
									"operatorX": managedIdentity1,
									"operatorY": managedIdentity1,
								},
								ServiceManagedIdentity: managedIdentity1,
							},
						},
					},
				},
				Identity: &arm.ManagedServiceIdentity{
					Type: arm.ManagedServiceIdentityTypeUserAssigned,
					UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
						managedIdentity1.String(): {},
					},
				},
			},
			expectErrors: []utils.ExpectedError{
				{
					Message:   "identity is used multiple times",
					FieldPath: "identity.userAssignedIdentities",
				},
			},
		},
		{
			name: "Cluster with invalid data plane operator identities",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Platform: api.CustomerPlatformProfile{
						OperatorsAuthentication: api.OperatorsAuthenticationProfile{
							UserAssignedIdentities: api.UserAssignedIdentitiesProfile{
								DataPlaneOperators: map[string]*azcorearm.ResourceID{
									"operatorX": managedIdentity1,
								},
							},
						},
					},
				},
				Identity: &arm.ManagedServiceIdentity{
					Type: arm.ManagedServiceIdentityTypeUserAssigned,
					UserAssignedIdentities: map[string]*arm.UserAssignedIdentity{
						managedIdentity1.String(): {},
					},
				},
			},
			expectErrors: []utils.ExpectedError{
				{
					Message:   "identity is assigned to this resource but not used",
					FieldPath: "identity.userAssignedIdentities",
				},
				{
					Message:   "cannot use identity assigned to this resource by .identities.userAssignedIdentities",
					FieldPath: "customerProperties.platform.operatorsAuthentication.userAssignedIdentities.dataPlaneOperators[operatorX]",
				},
			},
		},
		// Managed resource group name validation
		{
			name: "Managed resource group name is missing",
			resource: func() *api.HCPOpenShiftCluster {
				r := api.MinimumValidClusterTestCase()
				r.CustomerProperties.Platform.ManagedResourceGroup = ""
				return r
			}(),
			expectErrors: []utils.ExpectedError{
				{
					Message:   "Required value",
					FieldPath: "customerProperties.platform.managedResourceGroup",
				},
			},
		},
		{
			name: "Managed resource group name ends with period",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Platform: api.CustomerPlatformProfile{
						ManagedResourceGroup: "invalid-name.",
					},
				},
			},
			expectErrors: []utils.ExpectedError{
				{
					Message:   "max 90 characters",
					FieldPath: "customerProperties.platform.managedResourceGroup",
				},
			},
		},
		{
			name: "Managed resource group name with invalid characters",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Platform: api.CustomerPlatformProfile{
						ManagedResourceGroup: "invalid$name",
					},
				},
			},
			expectErrors: []utils.ExpectedError{
				{
					Message:   "max 90 characters",
					FieldPath: "customerProperties.platform.managedResourceGroup",
				},
			},
		},
		{
			name: "Managed resource group name too long",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Platform: api.CustomerPlatformProfile{
						ManagedResourceGroup: "a123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890",
					},
				},
			},
			expectErrors: []utils.ExpectedError{
				{
					Message:   "max 90 characters",
					FieldPath: "customerProperties.platform.managedResourceGroup",
				},
			},
		},
		{
			name: "Valid managed resource group name with periods and parentheses",
			tweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Platform: api.CustomerPlatformProfile{
						ManagedResourceGroup: "valid.name(test)",
					},
				},
			},
			expectErrors: []utils.ExpectedError{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resource := tt.resource
			if resource == nil {
				resource = api.ClusterTestCase(t, tt.tweaks)
			}

			op := operation.Operation{Type: operation.Create, Options: tt.opOptions}
			actualErrors := ValidateCluster(context.TODO(), op, resource, nil, nil)
			utils.VerifyErrorsMatch(t, tt.expectErrors, actualErrors)
		})
	}
}
