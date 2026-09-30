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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	hcpsdk20260901preview "github.com/Azure/ARO-HCP/test/sdk/v20260901preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

// Helper to convert ManagedServiceIdentity to AzureResourceManagerCommonTypesManagedServiceIdentityUpdate
func toIdentityUpdate(identity *hcpsdk20260901preview.ManagedServiceIdentity) *hcpsdk20260901preview.AzureResourceManagerCommonTypesManagedServiceIdentityUpdate {
	if identity == nil {
		return nil
	}
	return &hcpsdk20260901preview.AzureResourceManagerCommonTypesManagedServiceIdentityUpdate{
		Type:                   identity.Type,
		UserAssignedIdentities: identity.UserAssignedIdentities,
	}
}

var _ = Describe("Update HCPOpenShiftCluster", func() {
	Context("Negative", func() {
		It("creates a cluster and fails to update its name with a PATCH request",
			labels.RequireNothing, labels.Medium, labels.Negative,
			labels.MIContainers(1),
			func(ctx context.Context) {
				const clusterName = "patch-name-cluster"

				tc := framework.NewTestContext()

				if tc.UsePooledIdentities() {
					err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
					Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
				}

				By("creating a resource group")
				resourceGroup, err := tc.NewResourceGroup(ctx, "patch-name", tc.Location())
				Expect(err).NotTo(HaveOccurred(), "failed to create resource group for patch-name test")

				By("creating cluster parameters")
				clusterParams := framework.NewDefaultClusterParams20260901()
				clusterParams.ClusterName = clusterName
				managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
				clusterParams.ManagedResourceGroupName = managedResourceGroupName

				By("creating customer resources")
				clusterParams, err = tc.CreateClusterCustomerResources20260901(ctx,
					resourceGroup,
					clusterParams,
					map[string]interface{}{},
					TestArtifactsFS,
					framework.RBACScopeResourceGroup,
				)
				Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for patch-name cluster")

				By("creating the HCP cluster")
				err = tc.CreateHCPClusterFromParam20260901(
					ctx,
					GinkgoLogr,
					*resourceGroup.Name,
					clusterParams,
					nil,
					framework.ClusterCreationTimeout,
				)
				Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster for patch-name test")

				By("getting credentials")
				adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20260901(
					ctx,
					tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
					*resourceGroup.Name,
					clusterName,
					framework.GetAdminRESTConfigTimeout,
				)
				Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for patch-name cluster")

				By("ensuring the cluster is viable")
				err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
				Expect(err).NotTo(HaveOccurred(), "failed to verify HCP cluster viability for patch-name test")

				By("sending a PATCH request attempting to change the resource name")
				newName := clusterName + "-renamed"
				update := hcpsdk20260901preview.HcpOpenShiftClusterUpdate{
					Name: &newName,
				}
				_, err = framework.UpdateHCPCluster20260901(
					ctx,
					tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
					*resourceGroup.Name,
					clusterName,
					update,
					framework.UpdateHCPClusterTimeout,
				)
				Expect(err).To(HaveOccurred(), "expected error when attempting to rename cluster via PATCH")
				Expect(strings.ToLower(err.Error())).To(ContainSubstring("mismatchingresourcename"), "error should indicate mismatching resource name")
			},
		)
	})

	Context("Positive", func() {
		It("creates a cluster and updates tags with a PATCH request",
			labels.RequireNothing, labels.Medium, labels.Positive, labels.AroRpApiCompatible,
			labels.MIContainers(1),
			func(ctx context.Context) {
				const clusterName = "patch-tags-cluster"

				tc := framework.NewTestContext()

				if tc.UsePooledIdentities() {
					err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
					Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
				}

				By("creating a resource group")
				resourceGroup, err := tc.NewResourceGroup(ctx, "patch-tags", tc.Location())
				Expect(err).NotTo(HaveOccurred(), "failed to create resource group for patch-tags test")
				useOldAPI, err := tc.APIVersionAvailable(ctx, *resourceGroup.Name, metadataapi.APIVersionV20240610Preview)
				Expect(err).NotTo(HaveOccurred(), "failed to check whether v20240610preview is available")

				managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
				var patchTags func(string) (map[string]*string, error)
				var getTags func() (map[string]*string, error)
				if useOldAPI {
					By("creating cluster parameters using 20240610preview API")
					clusterParams := framework.NewDefaultClusterParams20240610()
					clusterParams.ClusterName = clusterName
					clusterParams.ManagedResourceGroupName = managedResourceGroupName
					By("creating customer resources with v20240610preview")
					clusterParams, err = tc.CreateClusterCustomerResources20240610(ctx, resourceGroup, clusterParams, map[string]interface{}{}, TestArtifactsFS, framework.RBACScopeResourceGroup)
					Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for patch-tags cluster")
					By("creating the HCP cluster with v20240610preview")
					err = tc.CreateHCPClusterFromParam20240610(ctx, GinkgoLogr, *resourceGroup.Name, clusterParams, framework.ClusterCreationTimeout)
					Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster for patch-tags test")
					client := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
					patchTags = func(val string) (map[string]*string, error) {
						update := hcpsdk20240610preview.HcpOpenShiftClusterUpdate{
							Identity: &hcpsdk20240610preview.AzureResourceManagerCommonTypesManagedServiceIdentityUpdate{
								Type: clusterParams.Identity.Type, UserAssignedIdentities: clusterParams.Identity.UserAssignedIdentities,
							},
							Tags: map[string]*string{"test": &val},
						}
						resp, err := framework.UpdateHCPCluster20240610(ctx, client, *resourceGroup.Name, clusterName, update, framework.UpdateHCPClusterTimeout)
						if err != nil {
							return nil, err
						}
						return resp.Tags, nil
					}
					getTags = func() (map[string]*string, error) {
						resp, err := client.Get(ctx, *resourceGroup.Name, clusterName, nil)
						if err != nil {
							return nil, err
						}
						return resp.Tags, nil
					}
				} else {
					By("creating cluster parameters using 20260901preview API")
					clusterParams := framework.NewDefaultClusterParams20260901()
					clusterParams.ClusterName = clusterName
					clusterParams.ManagedResourceGroupName = managedResourceGroupName
					By("creating customer resources with v20260901preview")
					clusterParams, err = tc.CreateClusterCustomerResources20260901(ctx, resourceGroup, clusterParams, map[string]interface{}{}, TestArtifactsFS, framework.RBACScopeResourceGroup)
					Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for patch-tags cluster")
					By("creating the HCP cluster with v20260901preview")
					err = tc.CreateHCPClusterFromParam20260901(ctx, GinkgoLogr, *resourceGroup.Name, clusterParams, nil, framework.ClusterCreationTimeout)
					Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster for patch-tags test")
					client := tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
					patchTags = func(val string) (map[string]*string, error) {
						update := hcpsdk20260901preview.HcpOpenShiftClusterUpdate{
							Identity: toIdentityUpdate(clusterParams.Identity),
							Tags:     map[string]*string{"test": &val},
						}
						resp, err := framework.UpdateHCPCluster20260901(ctx, client, *resourceGroup.Name, clusterName, update, framework.UpdateHCPClusterTimeout)
						if err != nil {
							return nil, err
						}
						return resp.Tags, nil
					}
					getTags = func() (map[string]*string, error) {
						resp, err := client.Get(ctx, *resourceGroup.Name, clusterName, nil)
						if err != nil {
							return nil, err
						}
						return resp.Tags, nil
					}
				}

				By("getting credentials")
				adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20260901(
					ctx,
					tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
					*resourceGroup.Name,
					clusterName,
					framework.GetAdminRESTConfigTimeout,
				)
				Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for patch-tags cluster")

				By("ensuring the cluster is viable")
				err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
				Expect(err).NotTo(HaveOccurred(), "failed to verify HCP cluster viability for patch-tags test")

				By("sending a PATCH request to set a tag")
				val := "should succeed"
				responseTags, err := patchTags(val)
				Expect(err).NotTo(HaveOccurred(), "failed to update HCP cluster tags via PATCH")

				By("verifying the tag is present in the update response body")
				Expect(responseTags).ToNot(BeNil(), "update response Tags was nil")
				Expect(responseTags["test"]).ToNot(BeNil(), "update response Tags[\"test\"] was nil")
				Expect(*responseTags["test"]).To(Equal(val), "update response Tags[\"test\"] should equal %q", val)

				By("verifying the tag is present on the cluster")
				storedTags, err := getTags()
				Expect(err).NotTo(HaveOccurred(), "failed to GET HCP cluster after tag update")
				Expect(storedTags).ToNot(BeNil(), "GET response Tags was nil")
				Expect(storedTags["test"]).ToNot(BeNil(), "GET response Tags[\"test\"] was nil")
				Expect(*storedTags["test"]).To(Equal(val), "GET response Tags[\"test\"] should equal %q after update", val)
			},
		)
	})
})
