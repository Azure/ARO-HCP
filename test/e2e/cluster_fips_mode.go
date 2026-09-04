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

	v20260901preview "github.com/Azure/ARO-HCP/test/sdk/v20260901preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("FIPS Mode Support", func() {
	Context("with v2026-09-01 API", func() {
		It("should create an HCP cluster with FIPS mode enabled via cryptoRestrictions property",
			labels.RequireNothing,
			labels.Medium,
			labels.Positive,
			labels.AroRpApiCompatible,
			labels.CreateCluster,
			labels.MIContainers(1),
			func(ctx context.Context) {
				const (
					customerClusterName = "fips-enabled-cluster"
					apiVersion          = "2026-09-01-preview"
				)

				tc := framework.NewTestContext()

				if tc.UsePooledIdentities() {
					err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
					Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
				}

				By("creating a resource group")
				resourceGroup, err := tc.NewResourceGroup(ctx, "fips-enabled", tc.Location())
				Expect(err).NotTo(HaveOccurred(), "failed to create resource group for fips-enabled test")

				By("creating cluster parameters with cryptoRestrictions set to FIPS")
				clusterParams := framework.NewDefaultClusterParams20260901()
				clusterParams.ClusterName = customerClusterName
				managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
				clusterParams.ManagedResourceGroupName = managedResourceGroupName
				clusterParams.CryptoRestrictions = to.Ptr(v20260901preview.CryptoRestrictionsFIPS)

				By("creating customer resources (infrastructure and managed identities)")
				clusterParams, err = tc.CreateClusterCustomerResources20260901(ctx,
					resourceGroup,
					clusterParams,
					map[string]interface{}{},
					TestArtifactsFS,
					framework.RBACScopeResourceGroup,
				)
				Expect(err).NotTo(HaveOccurred(), "failed to create cluster customer resources")

				By("creating the ARO-HCP cluster with cryptoRestrictions set to FIPS")
				clusterResource, err := framework.BuildHCPClusterFromParams20260901(clusterParams, tc.Location(), nil)
				Expect(err).NotTo(HaveOccurred(), "failed to build HCP cluster resource from params")

				_, err = framework.CreateHCPClusterAndWait20260901(
					ctx,
					GinkgoLogr,
					tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
					*resourceGroup.Name,
					customerClusterName,
					clusterResource,
					framework.ClusterCreationTimeout,
				)
				Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %q with cryptoRestrictions set to FIPS", customerClusterName)

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
				actualHCPCluster, err := tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient().Get(ctx, *resourceGroup.Name, customerClusterName, nil)
				Expect(err).NotTo(HaveOccurred(), "failed to get HCP cluster %s", customerClusterName)
				Expect(actualHCPCluster.Properties.CryptoRestrictions).NotTo(BeNil(), "cryptoRestrictions should not be nil")
				Expect(*actualHCPCluster.Properties.CryptoRestrictions).To(Equal(v20260901preview.CryptoRestrictionsFIPS), "cryptoRestrictions should be set to 'FIPS'")

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
				actualHCPCluster.Properties.CryptoRestrictions = to.Ptr(v20260901preview.CryptoRestrictionsNone)
				// The Get above returns a fully populated UserAssignedIdentities map
				// (ClientID/PrincipalID set by ARM). Re-sending those populated values on
				// this PUT is rejected by ARM with InvalidIdentityValues; existing
				// identities must be echoed back as empty objects.
				framework.ClearUserAssignedIdentityValues20260901(actualHCPCluster.Identity)
				poller, err := tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient().BeginCreateOrUpdate(
					ctx,
					*resourceGroup.Name,
					customerClusterName,
					actualHCPCluster.HcpOpenShiftCluster,
					nil,
				)
				if err == nil {
					_, err = poller.PollUntilDone(ctx, nil)
				}
				Expect(err).To(HaveOccurred(), "expected cryptoRestrictions modification to be rejected")
				Expect(strings.ToLower(err.Error())).To(ContainSubstring("immutable"), "error should indicate cryptoRestrictions is immutable")

				By("verifying cryptoRestrictions remains unchanged at FIPS")
				verifyCluster, err := tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient().Get(ctx, *resourceGroup.Name, customerClusterName, nil)
				Expect(err).NotTo(HaveOccurred(), "failed to get HCP cluster after update attempt")
				Expect(verifyCluster.Properties.CryptoRestrictions).NotTo(BeNil(), "cryptoRestrictions should not be nil")
				Expect(*verifyCluster.Properties.CryptoRestrictions).To(Equal(v20260901preview.CryptoRestrictionsFIPS), "cryptoRestrictions should remain 'FIPS' after rejected update")
			})
	})
})
