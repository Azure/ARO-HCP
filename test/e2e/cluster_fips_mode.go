// Copyright 2026 Microsoft Corporation
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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	v20260630preview "github.com/Azure/ARO-HCP/test/sdk/v20260630preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	v20260901preview "github.com/Azure/ARO-HCP/test/sdk/v20260901preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("FIPS Mode Support", func() {
	Context("with an available API version", func() {
		It("should create an HCP cluster with FIPS mode enabled via cryptoRestrictions property",
			labels.RequireNothing,
			labels.Medium,
			labels.Positive,
			labels.AroRpApiCompatible,
			labels.CreateCluster,
			labels.MIContainers(1),
			func(ctx context.Context) {
				const customerClusterName = "fips-enabled-cluster"

				tc := framework.NewTestContext()

				if tc.UsePooledIdentities() {
					err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
					Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
				}

				By("creating a resource group")
				resourceGroup, err := tc.NewResourceGroup(ctx, "fips-enabled", tc.Location())
				Expect(err).NotTo(HaveOccurred(), "failed to create resource group for fips-enabled test")
				useOldAPI, err := tc.APIVersionAvailable(ctx, *resourceGroup.Name, metadataapi.APIVersionV20260630Preview)
				Expect(err).NotTo(HaveOccurred(), "failed to check whether v20260630preview is available")

				managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
				var getCryptoRestrictions func() (string, error)
				var tryChangingCryptoRestrictions func() error
				if useOldAPI {
					By("creating cluster parameters with cryptoRestrictions set to FIPS using 20260630preview API")
					clusterParams := framework.NewDefaultClusterParams20260630()
					clusterParams.ClusterName = customerClusterName
					clusterParams.ManagedResourceGroupName = managedResourceGroupName
					clusterParams.CryptoRestrictions = to.Ptr(v20260630preview.CryptoRestrictionsFIPS)
					By("creating customer resources with v20260630preview")
					clusterParams, err = tc.CreateClusterCustomerResources20260630(ctx, resourceGroup, clusterParams, map[string]interface{}{}, TestArtifactsFS, framework.RBACScopeResourceGroup)
					Expect(err).NotTo(HaveOccurred(), "failed to create cluster customer resources")
					clusterResource, buildErr := framework.BuildHCPClusterFromParams20260630(clusterParams, tc.Location(), nil)
					Expect(buildErr).NotTo(HaveOccurred(), "failed to build v20260630preview cluster resource")
					By("creating the FIPS cluster with v20260630preview")
					_, err = framework.CreateHCPClusterAndWait20260630(ctx, GinkgoLogr, tc.Get20260630ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(), *resourceGroup.Name, customerClusterName, clusterResource, framework.ClusterCreationTimeout)
					Expect(err).NotTo(HaveOccurred(), "failed to create FIPS cluster %q", customerClusterName)
					client := tc.Get20260630ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
					getCryptoRestrictions = func() (string, error) {
						cluster, err := client.Get(ctx, *resourceGroup.Name, customerClusterName, nil)
						if err != nil {
							return "", err
						}
						Expect(cluster.Properties).NotTo(BeNil(), "v20260630preview cluster Properties was nil")
						Expect(cluster.Properties.CryptoRestrictions).NotTo(BeNil(), "v20260630preview cryptoRestrictions was nil")
						return string(*cluster.Properties.CryptoRestrictions), nil
					}
					tryChangingCryptoRestrictions = func() error {
						cluster, err := client.Get(ctx, *resourceGroup.Name, customerClusterName, nil)
						if err != nil {
							return err
						}
						cluster.Properties.CryptoRestrictions = to.Ptr(v20260630preview.CryptoRestrictionsNone)
						framework.ClearUserAssignedIdentityValues20260630(cluster.Identity)
						poller, err := client.BeginCreateOrUpdate(ctx, *resourceGroup.Name, customerClusterName, cluster.HcpOpenShiftCluster, nil)
						if err != nil {
							return err
						}
						_, err = poller.PollUntilDone(ctx, nil)
						return err
					}
				} else {
					By("creating cluster parameters with cryptoRestrictions set to FIPS using 20260901preview API")
					clusterParams := framework.NewDefaultClusterParams20260901()
					clusterParams.ClusterName = customerClusterName
					clusterParams.ManagedResourceGroupName = managedResourceGroupName
					clusterParams.CryptoRestrictions = to.Ptr(v20260901preview.CryptoRestrictionsFIPS)
					By("creating customer resources with v20260901preview")
					clusterParams, err = tc.CreateClusterCustomerResources20260901(ctx, resourceGroup, clusterParams, map[string]interface{}{}, TestArtifactsFS, framework.RBACScopeResourceGroup)
					Expect(err).NotTo(HaveOccurred(), "failed to create cluster customer resources")
					clusterResource, buildErr := framework.BuildHCPClusterFromParams20260901(clusterParams, tc.Location(), nil)
					Expect(buildErr).NotTo(HaveOccurred(), "failed to build v20260901preview cluster resource")
					By("creating the FIPS cluster with v20260901preview")
					_, err = framework.CreateHCPClusterAndWait20260901(ctx, GinkgoLogr, tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(), *resourceGroup.Name, customerClusterName, clusterResource, framework.ClusterCreationTimeout)
					Expect(err).NotTo(HaveOccurred(), "failed to create FIPS cluster %q", customerClusterName)
					client := tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
					getCryptoRestrictions = func() (string, error) {
						cluster, err := client.Get(ctx, *resourceGroup.Name, customerClusterName, nil)
						if err != nil {
							return "", err
						}
						Expect(cluster.Properties).NotTo(BeNil(), "v20260901preview cluster Properties was nil")
						Expect(cluster.Properties.CryptoRestrictions).NotTo(BeNil(), "v20260901preview cryptoRestrictions was nil")
						return string(*cluster.Properties.CryptoRestrictions), nil
					}
					tryChangingCryptoRestrictions = func() error {
						cluster, err := client.Get(ctx, *resourceGroup.Name, customerClusterName, nil)
						if err != nil {
							return err
						}
						cluster.Properties.CryptoRestrictions = to.Ptr(v20260901preview.CryptoRestrictionsNone)
						framework.ClearUserAssignedIdentityValues20260901(cluster.Identity)
						poller, err := client.BeginCreateOrUpdate(ctx, *resourceGroup.Name, customerClusterName, cluster.HcpOpenShiftCluster, nil)
						if err != nil {
							return err
						}
						_, err = poller.PollUntilDone(ctx, nil)
						return err
					}
				}

				By("creating the node pool with FIPS enabled machines")
				nodePoolParams := framework.NewDefaultNodePoolParams20260901()
				nodePoolParams.ClusterName = customerClusterName
				nodePoolParams.NodePoolName = "np-1"
				nodePoolParams.Replicas = int32(2)

				err = tc.CreateNodePoolFromParam20260901(ctx,
					GinkgoLogr,
					*resourceGroup.Name,
					managedResourceGroupName,
					customerClusterName,
					nodePoolParams,
					framework.NodePoolCreationTimeout,
				)
				Expect(err).NotTo(HaveOccurred(), "failed to create node pool %q for fips-enabled cluster %q", nodePoolParams.NodePoolName, customerClusterName)

				By("verifying the cluster was created with cryptoRestrictions=FIPS")
				cryptoRestrictions, err := getCryptoRestrictions()
				Expect(err).NotTo(HaveOccurred(), "failed to get HCP cluster %s", customerClusterName)
				Expect(cryptoRestrictions).To(Equal("FIPS"), "cryptoRestrictions should be set to 'FIPS'")

				By("getting credentials")
				adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20260901(
					ctx,
					tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
					*resourceGroup.Name,
					customerClusterName,
					framework.GetAdminRESTConfigTimeout,
				)
				Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for HCP cluster %s", customerClusterName)

				By("verifying FIPS mode is enabled on the cluster")
				err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig, verifiers.VerifyFIPSEnabled())
				Expect(err).NotTo(HaveOccurred(), "failed to verify FIPS is enabled on cluster %s", customerClusterName)

				By("attempting to change cryptoRestrictions from FIPS to None - should be rejected")
				err = tryChangingCryptoRestrictions()
				Expect(err).To(HaveOccurred(), "expected cryptoRestrictions modification to be rejected")
				Expect(strings.ToLower(err.Error())).To(ContainSubstring("immutable"), "error should indicate cryptoRestrictions is immutable")

				By("verifying cryptoRestrictions remains unchanged at FIPS")
				cryptoRestrictions, err = getCryptoRestrictions()
				Expect(err).NotTo(HaveOccurred(), "failed to get HCP cluster after update attempt")
				Expect(cryptoRestrictions).To(Equal("FIPS"), "cryptoRestrictions should remain 'FIPS' after rejected update")
			})
	})
})
