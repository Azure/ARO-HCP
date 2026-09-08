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

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apitesting/coreapitesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

var (
	managedIdentity1 = coreapitesting.NewTestUserAssignedIdentity("myManagedIdentity1")
	managedIdentity2 = coreapitesting.NewTestUserAssignedIdentity("myManagedIdentity2")
	managedIdentity3 = coreapitesting.NewTestUserAssignedIdentity("myManagedIdentity3")
)

func TestClusterRequired(t *testing.T) {
	tests := []struct {
		name         string
		resource     *coreapi.HCPOpenShiftCluster
		tweaks       *coreapi.HCPOpenShiftCluster
		opOptions    []string
		expectErrors []utils.ExpectedError
	}{
		{
			name:     "Empty cluster",
			resource: &coreapi.HCPOpenShiftCluster{},
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
					FieldPath: "customerProperties.ingress.type",
				},
				{
					Message:   "Unsupported value",
					FieldPath: "customerProperties.ingress.type",
				},
				{
					Message:   "Unsupported value",
					FieldPath: "customerProperties.cryptoRestrictions",
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
			resource: coreapi.NewDefaultHCPOpenShiftCluster(
				metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster")),
				coreapitesting.TestLocation,
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
					Message:   "Unsupported value",
					FieldPath: "customerProperties.etcd.dataEncryption.keyManagementMode",
				},
				{
					Message:   "Required value",
					FieldPath: "customerProperties.etcd.dataEncryption.keyManagementMode",
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
			resource:     coreapitesting.MinimumValidClusterTestCase(),
			expectErrors: []utils.ExpectedError{},
		},
		{
			name: "Cluster with identity",
			tweaks: &coreapi.HCPOpenShiftCluster{
				CustomerProperties: coreapi.HCPOpenShiftClusterCustomerProperties{
					Platform: coreapi.CustomerPlatformProfile{
						OperatorsAuthentication: coreapi.OperatorsAuthenticationProfile{
							UserAssignedIdentities: coreapi.UserAssignedIdentitiesProfile{
								ControlPlaneOperators: map[string]*azcorearm.ResourceID{
									"operatorX": coreapitesting.NewTestUserAssignedIdentity("MyManagedIdentity"),
								},
							},
						},
					},
				},
				Identity: &coreapi.ManagedServiceIdentity{
					Type: coreapi.ManagedServiceIdentityTypeUserAssigned,
					UserAssignedIdentities: map[string]*coreapi.UserAssignedIdentity{
						coreapitesting.NewTestUserAssignedIdentity("MyManagedIdentity").String(): {},
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
				resource = coreapitesting.ClusterTestCase(t, tt.tweaks)
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
		resource     *coreapi.HCPOpenShiftCluster
		tweaks       *coreapi.HCPOpenShiftCluster
		opOptions    []string
		expectErrors []utils.ExpectedError
	}{
		{
			name:         "Minimum valid cluster",
			tweaks:       &coreapi.HCPOpenShiftCluster{},
			expectErrors: []utils.ExpectedError{},
		},
		{
			name: "Bad cidrv4",
			tweaks: &coreapi.HCPOpenShiftCluster{
				CustomerProperties: coreapi.HCPOpenShiftClusterCustomerProperties{
					Network: coreapi.NetworkProfile{
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
			tweaks: &coreapi.HCPOpenShiftCluster{
				CustomerProperties: coreapi.HCPOpenShiftClusterCustomerProperties{
					DNS: coreapi.CustomerDNSProfile{
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
			tweaks: &coreapi.HCPOpenShiftCluster{
				CustomerProperties: coreapi.HCPOpenShiftClusterCustomerProperties{
					Platform: coreapi.CustomerPlatformProfile{
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
			resource: func() *coreapi.HCPOpenShiftCluster {
				r := coreapitesting.MinimumValidClusterTestCase()
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
			resource: func() *coreapi.HCPOpenShiftCluster {
				r := coreapitesting.MinimumValidClusterTestCase()
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
			resource: func() *coreapi.HCPOpenShiftCluster {
				r := coreapitesting.MinimumValidClusterTestCase()
				r.CustomerProperties.Version.ID = "4.20.8"
				return r
			}(),
			opOptions:    testFeatureOptions(metadataapi.FeatureExperimentalReleaseFeatures),
			expectErrors: []utils.ExpectedError{},
		},
		{
			name: "ChannelGroup candidate is rejected without experimental flag",
			resource: func() *coreapi.HCPOpenShiftCluster {
				r := coreapitesting.MinimumValidClusterTestCase()
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
			resource: func() *coreapi.HCPOpenShiftCluster {
				r := coreapitesting.MinimumValidClusterTestCase()
				r.CustomerProperties.Version.ChannelGroup = "candidate"
				return r
			}(),
			opOptions:    testFeatureOptions(metadataapi.FeatureExperimentalReleaseFeatures),
			expectErrors: []utils.ExpectedError{},
		},
		{
			name: "Version ID with prerelease is rejected without experimental flag",
			resource: func() *coreapi.HCPOpenShiftCluster {
				r := coreapitesting.MinimumValidClusterTestCase()
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
			resource: func() *coreapi.HCPOpenShiftCluster {
				r := coreapitesting.MinimumValidClusterTestCase()
				r.CustomerProperties.Version.ID = "4.21.0-rc.1"
				return r
			}(),
			opOptions:    testFeatureOptions(metadataapi.FeatureExperimentalReleaseFeatures),
			expectErrors: []utils.ExpectedError{},
		},
		{
			name: "Version ID with nightly format is allowed with experimental flag",
			resource: func() *coreapi.HCPOpenShiftCluster {
				r := coreapitesting.MinimumValidClusterTestCase()
				r.CustomerProperties.Version.ChannelGroup = "nightly"
				r.CustomerProperties.Version.ID = "4.21.0-0.nightly-2024-01-15-123456"
				return r
			}(),
			opOptions:    testFeatureOptions(metadataapi.FeatureExperimentalReleaseFeatures),
			expectErrors: []utils.ExpectedError{},
		},
		{
			name: "ChannelGroup fast is allowed without experimental flag",
			resource: func() *coreapi.HCPOpenShiftCluster {
				r := coreapitesting.MinimumValidClusterTestCase()
				r.CustomerProperties.Version.ChannelGroup = "fast"
				return r
			}(),
			expectErrors: []utils.ExpectedError{},
		},
		{
			name: "Version must be at least 4.20 without experimental flag",
			resource: func() *coreapi.HCPOpenShiftCluster {
				r := coreapitesting.MinimumValidClusterTestCase()
				r.CustomerProperties.Version.ID = "4.20"
				return r
			}(),
			expectErrors: []utils.ExpectedError{},
		},
		{
			name: "Version must be at least 4.20 with experimental flag",
			resource: func() *coreapi.HCPOpenShiftCluster {
				r := coreapitesting.MinimumValidClusterTestCase()
				r.CustomerProperties.Version.ID = "4.19"
				return r
			}(),
			opOptions: testFeatureOptions(metadataapi.FeatureExperimentalReleaseFeatures),
			expectErrors: []utils.ExpectedError{
				{Message: "must be at least 4.20", FieldPath: "customerProperties.version.id"},
			},
		},
		{
			name: "ChannelGroup nightly is rejected without experimental flag",
			resource: func() *coreapi.HCPOpenShiftCluster {
				r := coreapitesting.MinimumValidClusterTestCase()
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
			resource: func() *coreapi.HCPOpenShiftCluster {
				r := coreapitesting.MinimumValidClusterTestCase()
				r.CustomerProperties.Version.ChannelGroup = "nightly"
				return r
			}(),
			opOptions:    testFeatureOptions(metadataapi.FeatureExperimentalReleaseFeatures),
			expectErrors: []utils.ExpectedError{},
		},
		{
			name: "ChannelGroup blah is rejected even with experimental flag",
			resource: func() *coreapi.HCPOpenShiftCluster {
				r := coreapitesting.MinimumValidClusterTestCase()
				r.CustomerProperties.Version.ChannelGroup = "blah"
				return r
			}(),
			opOptions: testFeatureOptions(metadataapi.FeatureExperimentalReleaseFeatures),
			expectErrors: []utils.ExpectedError{
				{
					Message:   "supported values: \"candidate\", \"fast\", \"nightly\", \"stable\"",
					FieldPath: "customerProperties.version.channelGroup",
				},
			},
		},
		{
			name: "Bad enum_visibility",
			tweaks: &coreapi.HCPOpenShiftCluster{
				CustomerProperties: coreapi.HCPOpenShiftClusterCustomerProperties{
					API: coreapi.CustomerAPIProfile{
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
			tweaks: &coreapi.HCPOpenShiftCluster{
				Identity: &coreapi.ManagedServiceIdentity{
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
			tweaks: &coreapi.HCPOpenShiftCluster{
				CustomerProperties: coreapi.HCPOpenShiftClusterCustomerProperties{
					ClusterImageRegistry: coreapi.ClusterImageRegistryProfile{
						State: metadataapi.ClusterImageRegistryState("not enabled"),
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
			tweaks: &coreapi.HCPOpenShiftCluster{
				CustomerProperties: coreapi.HCPOpenShiftClusterCustomerProperties{
					DNS: coreapi.CustomerDNSProfile{
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
			tweaks: &coreapi.HCPOpenShiftCluster{
				CustomerProperties: coreapi.HCPOpenShiftClusterCustomerProperties{
					Network: coreapi.NetworkProfile{
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
			tweaks: &coreapi.HCPOpenShiftCluster{
				CustomerProperties: coreapi.HCPOpenShiftClusterCustomerProperties{
					Network: coreapi.NetworkProfile{
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
			tweaks: &coreapi.HCPOpenShiftCluster{
				CustomerProperties: coreapi.HCPOpenShiftClusterCustomerProperties{
					Platform: coreapi.CustomerPlatformProfile{
						OperatorsAuthentication: coreapi.OperatorsAuthenticationProfile{
							UserAssignedIdentities: coreapi.UserAssignedIdentitiesProfile{
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
			tweaks: &coreapi.HCPOpenShiftCluster{
				CustomerProperties: coreapi.HCPOpenShiftClusterCustomerProperties{
					Platform: coreapi.CustomerPlatformProfile{
						OperatorsAuthentication: coreapi.OperatorsAuthenticationProfile{
							UserAssignedIdentities: coreapi.UserAssignedIdentitiesProfile{
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
			resource: func() *coreapi.HCPOpenShiftCluster {
				r := coreapitesting.MinimumValidClusterTestCase()
				r.CustomerProperties.Etcd.DataEncryption.CustomerManaged = nil
				return r
			}(),
			expectErrors: []utils.ExpectedError{
				{
					Message:   "must be specified when `keyManagementMode` is \"CustomerManaged\"",
					FieldPath: "customerProperties.etcd.dataEncryption.customerManaged",
				},
			},
		},
		{
			name: "Customer managed Key Management Service (KMS) requires Kms fields",
			resource: func() *coreapi.HCPOpenShiftCluster {
				r := coreapitesting.MinimumValidClusterTestCase()
				r.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms = nil
				return r
			}(),
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
			resource: func() *coreapi.HCPOpenShiftCluster {
				r := coreapitesting.MinimumValidClusterTestCase()
				r.CustomerProperties.Etcd.DataEncryption.CustomerManaged.EncryptionType = "Alternate"
				r.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms = &coreapi.KmsEncryptionProfile{}
				return r
			}(),
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
					FieldPath: "customerProperties.etcd.dataEncryption.customerManaged.kms.keyEncryptionKeyUrl",
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
			tweaks: &coreapi.HCPOpenShiftCluster{
				CustomerProperties: coreapi.HCPOpenShiftClusterCustomerProperties{
					Network: coreapi.NetworkProfile{
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
			tweaks: &coreapi.HCPOpenShiftCluster{
				CustomerProperties: coreapi.HCPOpenShiftClusterCustomerProperties{
					Network: coreapi.NetworkProfile{
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
			tweaks: &coreapi.HCPOpenShiftCluster{
				CustomerProperties: coreapi.HCPOpenShiftClusterCustomerProperties{
					Network: coreapi.NetworkProfile{
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
			tweaks: &coreapi.HCPOpenShiftCluster{
				CustomerProperties: coreapi.HCPOpenShiftClusterCustomerProperties{
					Platform: coreapi.CustomerPlatformProfile{
						ManagedResourceGroup: coreapitesting.TestResourceGroupName,
						// Use a different resource group name to avoid a subnet ID error.
						SubnetID:                metadataapi.Must(azcorearm.ParseResourceID(path.Join("/subscriptions", coreapitesting.TestSubscriptionID, "resourceGroups", "anotherResourceGroup", "providers", "Microsoft.Network", "virtualNetworks", coreapitesting.TestVirtualNetworkName, "subnets", coreapitesting.TestSubnetName))),
						VnetIntegrationSubnetID: metadataapi.Must(azcorearm.ParseResourceID(path.Join("/subscriptions", coreapitesting.TestSubscriptionID, "resourceGroups", "anotherResourceGroup", "providers", "Microsoft.Network", "virtualNetworks", coreapitesting.TestVirtualNetworkName, "subnets", coreapitesting.TestVnetIntegrationSubnetName))),
						NetworkSecurityGroupID:  metadataapi.Must(azcorearm.ParseResourceID(path.Join("/subscriptions", coreapitesting.TestSubscriptionID, "resourceGroups", "anotherResourceGroup", "providers", "Microsoft.Network", "networkSecurityGroups", coreapitesting.TestNetworkSecurityGroupName))),
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
			resource: func() *coreapi.HCPOpenShiftCluster {
				c := coreapitesting.MinimumValidClusterTestCase()
				c.CustomerProperties.Platform.ManagedResourceGroup = "MRG"
				c.CustomerProperties.Platform.SubnetID = metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/MRG/providers/Microsoft.Network/virtualNetworks/testVirtualNetwork/subnets/testSubnet"))
				c.CustomerProperties.Platform.VnetIntegrationSubnetID = metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/anotherResourceGroup/providers/Microsoft.Network/virtualNetworks/testVirtualNetwork/subnets/testVnetIntegrationSubnet"))
				return c
			}(),
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
					Message:   "must belong to the same VNet as subnetId",
					FieldPath: "customerProperties.platform.vnetIntegrationSubnetId",
				},
				{
					Message:   "must be in the same Azure subscription: \"11111111-1111-1111-1111-111111111111\"",
					FieldPath: "customerProperties.platform.vnetIntegrationSubnetId",
				},
			},
		},
		{
			name: "Cluster with differently-cased identities",
			tweaks: &coreapi.HCPOpenShiftCluster{
				CustomerProperties: coreapi.HCPOpenShiftClusterCustomerProperties{
					Platform: coreapi.CustomerPlatformProfile{
						OperatorsAuthentication: coreapi.OperatorsAuthenticationProfile{
							UserAssignedIdentities: coreapi.UserAssignedIdentitiesProfile{
								ControlPlaneOperators: map[string]*azcorearm.ResourceID{
									"operatorX": metadataapi.Must(azcorearm.ParseResourceID(strings.ToLower(managedIdentity1.String()))),
								},
								ServiceManagedIdentity: metadataapi.Must(azcorearm.ParseResourceID(strings.ToLower(managedIdentity2.String()))),
							},
						},
					},
				},
				Identity: &coreapi.ManagedServiceIdentity{
					Type: coreapi.ManagedServiceIdentityTypeUserAssigned,
					UserAssignedIdentities: map[string]*coreapi.UserAssignedIdentity{
						strings.ToUpper(managedIdentity1.String()): {},
						strings.ToUpper(managedIdentity2.String()): {},
					},
				},
			},
		},
		{
			name: "Cluster with broken identities",
			tweaks: &coreapi.HCPOpenShiftCluster{
				CustomerProperties: coreapi.HCPOpenShiftClusterCustomerProperties{
					Platform: coreapi.CustomerPlatformProfile{
						OperatorsAuthentication: coreapi.OperatorsAuthenticationProfile{
							UserAssignedIdentities: coreapi.UserAssignedIdentitiesProfile{
								ControlPlaneOperators: map[string]*azcorearm.ResourceID{
									"operatorX": managedIdentity1,
								},
								ServiceManagedIdentity: managedIdentity2,
							},
						},
					},
				},
				Identity: &coreapi.ManagedServiceIdentity{
					Type: coreapi.ManagedServiceIdentityTypeUserAssigned,
					UserAssignedIdentities: map[string]*coreapi.UserAssignedIdentity{
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
			tweaks: &coreapi.HCPOpenShiftCluster{
				CustomerProperties: coreapi.HCPOpenShiftClusterCustomerProperties{
					Platform: coreapi.CustomerPlatformProfile{
						OperatorsAuthentication: coreapi.OperatorsAuthenticationProfile{
							UserAssignedIdentities: coreapi.UserAssignedIdentitiesProfile{
								ControlPlaneOperators: map[string]*azcorearm.ResourceID{
									"operatorX": managedIdentity1,
									"operatorY": managedIdentity1,
								},
								ServiceManagedIdentity: managedIdentity1,
							},
						},
					},
				},
				Identity: &coreapi.ManagedServiceIdentity{
					Type: coreapi.ManagedServiceIdentityTypeUserAssigned,
					UserAssignedIdentities: map[string]*coreapi.UserAssignedIdentity{
						managedIdentity1.String(): {},
					},
				},
			},
			expectErrors: []utils.ExpectedError{
				{
					Message:   "must be unique within the cluster",
					FieldPath: "customerProperties.platform.operatorsAuthentication.userAssignedIdentities.controlPlaneOperators",
				},
				{
					Message:   "must be unique within the cluster",
					FieldPath: "customerProperties.platform.operatorsAuthentication.userAssignedIdentities.serviceManagedIdentity",
				},
				{
					Message:   "identity is used multiple times",
					FieldPath: "identity.userAssignedIdentities",
				},
			},
		},
		{
			name: "Cluster with invalid data plane operator identities",
			tweaks: &coreapi.HCPOpenShiftCluster{
				CustomerProperties: coreapi.HCPOpenShiftClusterCustomerProperties{
					Platform: coreapi.CustomerPlatformProfile{
						OperatorsAuthentication: coreapi.OperatorsAuthenticationProfile{
							UserAssignedIdentities: coreapi.UserAssignedIdentitiesProfile{
								DataPlaneOperators: map[string]*azcorearm.ResourceID{
									"operatorX": managedIdentity1,
								},
							},
						},
					},
				},
				Identity: &coreapi.ManagedServiceIdentity{
					Type: coreapi.ManagedServiceIdentityTypeUserAssigned,
					UserAssignedIdentities: map[string]*coreapi.UserAssignedIdentity{
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
			resource: func() *coreapi.HCPOpenShiftCluster {
				r := coreapitesting.MinimumValidClusterTestCase()
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
			tweaks: &coreapi.HCPOpenShiftCluster{
				CustomerProperties: coreapi.HCPOpenShiftClusterCustomerProperties{
					Platform: coreapi.CustomerPlatformProfile{
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
			tweaks: &coreapi.HCPOpenShiftCluster{
				CustomerProperties: coreapi.HCPOpenShiftClusterCustomerProperties{
					Platform: coreapi.CustomerPlatformProfile{
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
			tweaks: &coreapi.HCPOpenShiftCluster{
				CustomerProperties: coreapi.HCPOpenShiftClusterCustomerProperties{
					Platform: coreapi.CustomerPlatformProfile{
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
			tweaks: &coreapi.HCPOpenShiftCluster{
				CustomerProperties: coreapi.HCPOpenShiftClusterCustomerProperties{
					Platform: coreapi.CustomerPlatformProfile{
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
				resource = coreapitesting.ClusterTestCase(t, tt.tweaks)
			}

			op := operation.Operation{Type: operation.Create, Options: tt.opOptions}
			actualErrors := ValidateCluster(context.TODO(), op, resource, nil, nil)
			utils.VerifyErrorsMatch(t, tt.expectErrors, actualErrors)
		})
	}
}
