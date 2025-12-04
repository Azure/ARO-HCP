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

package e2e

import (
	"context"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	hcpsdk "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Authorized CIDRs", func() {
	Context("Cluster Creation", func() {
		It("should be able to create a HCP cluster with valid authorized CIDRs",
			labels.RequireNothing,
			labels.Medium,
			labels.Positive,
			func(ctx context.Context) {
				const (
					customerNetworkSecurityGroupName = "customer-nsg-name"
					customerVnetName                 = "customer-vnet-name"
					customerVnetSubnetName           = "customer-vnet-subnet1"
					customerClusterName              = "with-authorized-cidrs-cl"
					openshiftControlPlaneVersionId   = "4.19"
				)
				tc := framework.NewTestContext()

				By("creating a resource group")
				resourceGroup, err := tc.NewResourceGroup(ctx, "e2e-with-authorized-cidrs", tc.Location())
				Expect(err).NotTo(HaveOccurred())

				By("creating a customer-infra")
				customerInfraDeploymentResult, err := framework.CreateBicepTemplateAndWait(ctx,
					tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
					*resourceGroup.Name,
					"customer-infra",
					framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/modules/customer-infra.json")),
					map[string]interface{}{
						"persistTagValue":        false,
						"customerNsgName":        customerNetworkSecurityGroupName,
						"customerVnetName":       customerVnetName,
						"customerVnetSubnetName": customerVnetSubnetName,
					},
					45*time.Minute,
				)
				Expect(err).NotTo(HaveOccurred())

				By("creating a managed identities")
				keyVaultName, err := framework.GetOutputValue(customerInfraDeploymentResult, "keyVaultName")
				Expect(err).NotTo(HaveOccurred())
				keyVaultNameStr, ok := keyVaultName.(string)
				Expect(ok).To(BeTrue())
				etcdEncryptionKeyVersion, err := framework.GetOutputValueString(customerInfraDeploymentResult, "etcdEncryptionKeyVersion")
				Expect(err).NotTo(HaveOccurred())
				managedIdentityDeploymentResult, err := framework.CreateBicepTemplateAndWait(ctx,
					tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
					*resourceGroup.Name,
					"managed-identities",
					framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/modules/managed-identities.json")),
					map[string]interface{}{
						"clusterName":  customerClusterName,
						"nsgName":      customerNetworkSecurityGroupName,
						"vnetName":     customerVnetName,
						"subnetName":   customerVnetSubnetName,
						"keyVaultName": keyVaultName,
					},
					45*time.Minute,
				)
				Expect(err).NotTo(HaveOccurred())

				By("creating the cluster with authorized CIDRs")
				userAssignedIdentities, err := framework.GetOutputValue(managedIdentityDeploymentResult, "userAssignedIdentitiesValue")
				Expect(err).NotTo(HaveOccurred())
				identity, err := framework.GetOutputValue(managedIdentityDeploymentResult, "identityValue")
				Expect(err).NotTo(HaveOccurred())
				etcdEncryptionKeyName, err := framework.GetOutputValueString(customerInfraDeploymentResult, "etcdEncryptionKeyName")
				Expect(err).NotTo(HaveOccurred())
				nsgResourceID, err := framework.GetOutputValueString(customerInfraDeploymentResult, "nsgID")
				Expect(err).NotTo(HaveOccurred())
				vnetSubnetResourceID, err := framework.GetOutputValueString(customerInfraDeploymentResult, "vnetSubnetID")
				Expect(err).NotTo(HaveOccurred())
				managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
				userAssignedIdentitiesProfile, err := framework.ConvertToUserAssignedIdentitiesProfile(userAssignedIdentities)
				Expect(err).NotTo(HaveOccurred())
				identityProfile, err := framework.ConvertToManagedServiceIdentity(identity)
				Expect(err).NotTo(HaveOccurred())

				clusterParams := framework.NewDefaultClusterParams()
				clusterParams.ClusterName = customerClusterName
				clusterParams.OpenshiftVersionId = openshiftControlPlaneVersionId
				clusterParams.ManagedResourceGroupName = managedResourceGroupName
				clusterParams.NsgResourceID = nsgResourceID
				clusterParams.SubnetResourceID = vnetSubnetResourceID
				clusterParams.VnetName = customerVnetName
				clusterParams.UserAssignedIdentitiesProfile = userAssignedIdentitiesProfile
				clusterParams.Identity = identityProfile
				clusterParams.KeyVaultName = keyVaultNameStr
				clusterParams.EtcdEncryptionKeyName = etcdEncryptionKeyName
				clusterParams.EtcdEncryptionKeyVersion = etcdEncryptionKeyVersion
				clusterParams.AuthorizedCIDRs = []*string{
					to.Ptr("10.0.0.0/16"),
					to.Ptr("192.168.1.0/24"),
				}

				err = framework.CreateHCPClusterFromParam(ctx,
					tc,
					*resourceGroup.Name,
					clusterParams,
					45*time.Minute,
				)
				Expect(err).NotTo(HaveOccurred())

				By("verifying cluster has the expected authorized CIDRs")
				cluster, err := framework.GetHCPCluster(
					ctx,
					tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
					*resourceGroup.Name,
					customerClusterName,
				)
				Expect(err).NotTo(HaveOccurred())
				Expect(cluster.Properties).ToNot(BeNil())
				Expect(cluster.Properties.API).ToNot(BeNil())
				Expect(cluster.Properties.API.AuthorizedCIDRs).ToNot(BeNil())
				Expect(cluster.Properties.API.AuthorizedCIDRs).To(HaveLen(2))

				// Dereference and check the CIDR values
				cidrs := make([]string, len(cluster.Properties.API.AuthorizedCIDRs))
				for i, cidr := range cluster.Properties.API.AuthorizedCIDRs {
					if cidr != nil {
						cidrs[i] = *cidr
					}
				}
				Expect(cidrs).To(ConsistOf("10.0.0.0/16", "192.168.1.0/24"))
			},
		)

		It("should be able to create a HCP cluster with empty authorized CIDRs",
			labels.RequireNothing,
			labels.Medium,
			labels.Positive,
			func(ctx context.Context) {
				const (
					customerNetworkSecurityGroupName = "customer-nsg-name"
					customerVnetName                 = "customer-vnet-name"
					customerVnetSubnetName           = "customer-vnet-subnet1"
					customerClusterName              = "empty-authorized-cidrs-cl"
					openshiftControlPlaneVersionId   = "4.19"
				)
				tc := framework.NewTestContext()

				By("creating a resource group")
				resourceGroup, err := tc.NewResourceGroup(ctx, "e2e-empty-authorized-cidrs", tc.Location())
				Expect(err).NotTo(HaveOccurred())

				By("creating a customer-infra")
				customerInfraDeploymentResult, err := framework.CreateBicepTemplateAndWait(ctx,
					tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
					*resourceGroup.Name,
					"customer-infra",
					framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/modules/customer-infra.json")),
					map[string]interface{}{
						"persistTagValue":        false,
						"customerNsgName":        customerNetworkSecurityGroupName,
						"customerVnetName":       customerVnetName,
						"customerVnetSubnetName": customerVnetSubnetName,
					},
					45*time.Minute,
				)
				Expect(err).NotTo(HaveOccurred())

				By("creating a managed identities")
				keyVaultName, err := framework.GetOutputValue(customerInfraDeploymentResult, "keyVaultName")
				Expect(err).NotTo(HaveOccurred())
				keyVaultNameStr, ok := keyVaultName.(string)
				Expect(ok).To(BeTrue())
				etcdEncryptionKeyVersion, err := framework.GetOutputValueString(customerInfraDeploymentResult, "etcdEncryptionKeyVersion")
				Expect(err).NotTo(HaveOccurred())
				managedIdentityDeploymentResult, err := framework.CreateBicepTemplateAndWait(ctx,
					tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
					*resourceGroup.Name,
					"managed-identities",
					framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/modules/managed-identities.json")),
					map[string]interface{}{
						"clusterName":  customerClusterName,
						"nsgName":      customerNetworkSecurityGroupName,
						"vnetName":     customerVnetName,
						"subnetName":   customerVnetSubnetName,
						"keyVaultName": keyVaultName,
					},
					45*time.Minute,
				)
				Expect(err).NotTo(HaveOccurred())

				By("creating the cluster with empty authorized CIDRs")
				userAssignedIdentities, err := framework.GetOutputValue(managedIdentityDeploymentResult, "userAssignedIdentitiesValue")
				Expect(err).NotTo(HaveOccurred())
				identity, err := framework.GetOutputValue(managedIdentityDeploymentResult, "identityValue")
				Expect(err).NotTo(HaveOccurred())
				etcdEncryptionKeyName, err := framework.GetOutputValueString(customerInfraDeploymentResult, "etcdEncryptionKeyName")
				Expect(err).NotTo(HaveOccurred())
				nsgResourceID, err := framework.GetOutputValueString(customerInfraDeploymentResult, "nsgID")
				Expect(err).NotTo(HaveOccurred())
				vnetSubnetResourceID, err := framework.GetOutputValueString(customerInfraDeploymentResult, "vnetSubnetID")
				Expect(err).NotTo(HaveOccurred())
				managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
				userAssignedIdentitiesProfile, err := framework.ConvertToUserAssignedIdentitiesProfile(userAssignedIdentities)
				Expect(err).NotTo(HaveOccurred())
				identityProfile, err := framework.ConvertToManagedServiceIdentity(identity)
				Expect(err).NotTo(HaveOccurred())

				clusterParams := framework.NewDefaultClusterParams()
				clusterParams.ClusterName = customerClusterName
				clusterParams.OpenshiftVersionId = openshiftControlPlaneVersionId
				clusterParams.ManagedResourceGroupName = managedResourceGroupName
				clusterParams.NsgResourceID = nsgResourceID
				clusterParams.SubnetResourceID = vnetSubnetResourceID
				clusterParams.VnetName = customerVnetName
				clusterParams.UserAssignedIdentitiesProfile = userAssignedIdentitiesProfile
				clusterParams.Identity = identityProfile
				clusterParams.KeyVaultName = keyVaultNameStr
				clusterParams.EtcdEncryptionKeyName = etcdEncryptionKeyName
				clusterParams.EtcdEncryptionKeyVersion = etcdEncryptionKeyVersion
				clusterParams.AuthorizedCIDRs = []*string{}

				err = framework.CreateHCPClusterFromParam(ctx,
					tc,
					*resourceGroup.Name,
					clusterParams,
					45*time.Minute,
				)
				Expect(err).NotTo(HaveOccurred())

				By("verifying cluster has empty authorized CIDRs")
				cluster, err := framework.GetHCPCluster(
					ctx,
					tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
					*resourceGroup.Name,
					customerClusterName,
				)
				Expect(err).NotTo(HaveOccurred())
				Expect(cluster.Properties).ToNot(BeNil())
				Expect(cluster.Properties.API).ToNot(BeNil())
				Expect(cluster.Properties.API.AuthorizedCIDRs).To(BeNil())
			},
		)

		It("should reject cluster creation with invalid CIDR format",
			labels.RequireNothing,
			labels.Medium,
			labels.Negative,
			func(ctx context.Context) {
				const clusterName = "invalid-cidr-cluster"

				tc := framework.NewTestContext()

				By("creating a resource group")
				resourceGroup, err := tc.NewResourceGroup(ctx, "e2e-invalid-cidr", tc.Location())
				Expect(err).NotTo(HaveOccurred())

				By("attempting to create cluster with invalid CIDR")
				location := tc.Location()
				cluster := hcpsdk.HcpOpenShiftCluster{
					Location: &location,
					Properties: &hcpsdk.HcpOpenShiftClusterProperties{
						Version: &hcpsdk.VersionProfile{
							ID:           to.Ptr("4.19"),
							ChannelGroup: to.Ptr("stable"),
						},
						API: &hcpsdk.APIProfile{
							Visibility: to.Ptr(hcpsdk.VisibilityPublic),
							AuthorizedCIDRs: []*string{
								to.Ptr("invalid-cidr"),
							},
						},
					},
				}

				_, err = tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient().BeginCreateOrUpdate(
					ctx,
					*resourceGroup.Name,
					clusterName,
					cluster,
					nil,
				)
				Expect(err).To(HaveOccurred())
				Expect(strings.ToLower(err.Error())).To(ContainSubstring("invalid cidr"))
			},
		)

		It("should reject cluster creation with IPv6 CIDR",
			labels.RequireNothing,
			labels.Medium,
			labels.Negative,
			func(ctx context.Context) {
				const clusterName = "ipv6-cidr-cluster"

				tc := framework.NewTestContext()

				By("creating a resource group")
				resourceGroup, err := tc.NewResourceGroup(ctx, "e2e-ipv6-cidr", tc.Location())
				Expect(err).NotTo(HaveOccurred())

				By("attempting to create cluster with IPv6 CIDR")
				location := tc.Location()
				cluster := hcpsdk.HcpOpenShiftCluster{
					Location: &location,
					Properties: &hcpsdk.HcpOpenShiftClusterProperties{
						Version: &hcpsdk.VersionProfile{
							ID:           to.Ptr("4.19"),
							ChannelGroup: to.Ptr("stable"),
						},
						API: &hcpsdk.APIProfile{
							Visibility: to.Ptr(hcpsdk.VisibilityPublic),
							AuthorizedCIDRs: []*string{
								to.Ptr("2001:db8::/32"),
							},
						},
					},
				}

				_, err = tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient().BeginCreateOrUpdate(
					ctx,
					*resourceGroup.Name,
					clusterName,
					cluster,
					nil,
				)
				Expect(err).To(HaveOccurred())
				Expect(strings.ToLower(err.Error())).To(Or(
					ContainSubstring("ipv4"),
					ContainSubstring("not an ip"),
				))
			},
		)

		It("should reject cluster creation with too many CIDRs",
			labels.RequireNothing,
			labels.Medium,
			labels.Negative,
			func(ctx context.Context) {
				const clusterName = "too-many-cidrs-cluster"

				tc := framework.NewTestContext()

				By("creating a resource group")
				resourceGroup, err := tc.NewResourceGroup(ctx, "e2e-too-many-cidrs", tc.Location())
				Expect(err).NotTo(HaveOccurred())

				By("attempting to create cluster with more than 500 CIDRs")
				cidrs := make([]*string, 501)
				for i := 0; i < 501; i++ {
					cidrs[i] = to.Ptr("10.0.0.1")
				}

				location := tc.Location()
				cluster := hcpsdk.HcpOpenShiftCluster{
					Location: &location,
					Properties: &hcpsdk.HcpOpenShiftClusterProperties{
						Version: &hcpsdk.VersionProfile{
							ID:           to.Ptr("4.19"),
							ChannelGroup: to.Ptr("stable"),
						},
						API: &hcpsdk.APIProfile{
							Visibility:      to.Ptr(hcpsdk.VisibilityPublic),
							AuthorizedCIDRs: cidrs,
						},
					},
				}

				_, err = tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient().BeginCreateOrUpdate(
					ctx,
					*resourceGroup.Name,
					clusterName,
					cluster,
					nil,
				)
				Expect(err).To(HaveOccurred())
				Expect(strings.ToLower(err.Error())).To(ContainSubstring("too many"))
			},
		)
	})
})
