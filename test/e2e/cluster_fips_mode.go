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

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("FIPS Mode Support", func() {
	Context("with ExperimentalReleaseFeatures AFEC registered", func() {
		It("should create an HCP cluster with FIPS mode enabled via experimental tag",
			labels.RequireNothing,
			labels.Medium,
			labels.Positive,
			labels.AroRpApiCompatible,
			labels.CreateCluster,
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

				By("creating cluster parameters with FIPS enabled tag")
				clusterParams := framework.NewDefaultClusterParams20251223()
				clusterParams.ClusterName = customerClusterName
				managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
				clusterParams.ManagedResourceGroupName = managedResourceGroupName
				clusterParams.Tags[api.TagClusterFIPSEnabled] = to.Ptr("true")

				By("creating customer resources (infrastructure and managed identities)")
				clusterParams, err = tc.CreateClusterCustomerResources20251223(ctx,
					resourceGroup,
					clusterParams,
					map[string]interface{}{},
					TestArtifactsFS,
					framework.RBACScopeResourceGroup,
				)
				Expect(err).NotTo(HaveOccurred(), "failed to create cluster customer resources")

				By("creating the ARO-HCP cluster with FIPS enabled")
				clusterResource, err := framework.BuildHCPClusterFromParams20251223(clusterParams, tc.Location(), nil)
				Expect(err).NotTo(HaveOccurred(), "failed to build HCP cluster resource from params")

				_, err = framework.CreateHCPClusterAndWait20251223(
					ctx,
					GinkgoLogr,
					tc.Get20251223ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
					*resourceGroup.Name,
					customerClusterName,
					clusterResource,
					framework.ClusterCreationTimeout,
				)
				Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %q with FIPS enabled", customerClusterName)

				By("creating the node pool with FIPS enabled machines")
				nodePoolParams := framework.NewDefaultNodePoolParams20251223()
				nodePoolParams.ClusterName = customerClusterName
				nodePoolParams.NodePoolName = "np-1"
				nodePoolParams.Replicas = int32(2)

				err = tc.CreateNodePoolFromParam20251223(ctx,
					GinkgoLogr,
					*resourceGroup.Name,
					managedResourceGroupName,
					customerClusterName,
					nodePoolParams,
					framework.NodePoolCreationTimeout,
				)
				Expect(err).NotTo(HaveOccurred(), "failed to create node pool %q for fips-enabled cluster %q", nodePoolParams.NodePoolName, customerClusterName)

				By("verifying the cluster was created with the FIPS tag")
				actualHCPCluster, err := tc.Get20251223ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient().Get(ctx, *resourceGroup.Name, customerClusterName, nil)
				Expect(err).NotTo(HaveOccurred(), "failed to get HCP cluster %s", customerClusterName)
				Expect(actualHCPCluster.Tags).NotTo(BeNil(), "cluster tags should not be nil")
				fipsTag, exists := actualHCPCluster.Tags[api.TagClusterFIPSEnabled]
				Expect(exists).To(BeTrue(), "FIPS tag should exist")
				Expect(fipsTag).NotTo(BeNil(), "FIPS tag value should not be nil")
				Expect(*fipsTag).To(Equal("true"), "FIPS tag should be set to 'true'")

				By("getting credentials")
				adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20240610(
					ctx,
					tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
					*resourceGroup.Name,
					customerClusterName,
					framework.GetAdminRESTConfigTimeout,
				)
				Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for HCP cluster %s", customerClusterName)

				By("verifying FIPS mode is enabled on the cluster")
				err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig, verifiers.VerifyFIPSEnabled())
				Expect(err).NotTo(HaveOccurred(), "failed to verify FIPS is enabled on cluster %s", customerClusterName)

				By("attempting to change FIPS tag from true to false - should be rejected")
				actualHCPCluster.Tags[api.TagClusterFIPSEnabled] = to.Ptr("false")
				poller, err := tc.Get20251223ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient().BeginCreateOrUpdate(
					ctx,
					*resourceGroup.Name,
					customerClusterName,
					actualHCPCluster.HcpOpenShiftCluster,
					nil,
				)
				if err == nil {
					_, err = poller.PollUntilDone(ctx, nil)
				}
				Expect(err).To(HaveOccurred(), "expected FIPS tag modification to be rejected")
				Expect(strings.ToLower(err.Error())).To(ContainSubstring("immutable"), "error should indicate FIPS tag is immutable")

				By("verifying FIPS tag remains unchanged at true")
				verifyCluster, err := tc.Get20251223ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient().Get(ctx, *resourceGroup.Name, customerClusterName, nil)
				Expect(err).NotTo(HaveOccurred(), "failed to get HCP cluster after PATCH attempt")
				Expect(verifyCluster.Tags).NotTo(BeNil(), "cluster tags should not be nil")
				fipsTagAfterPatch, exists := verifyCluster.Tags[api.TagClusterFIPSEnabled]
				Expect(exists).To(BeTrue(), "FIPS tag should still exist")
				Expect(fipsTagAfterPatch).NotTo(BeNil(), "FIPS tag value should not be nil")
				Expect(*fipsTagAfterPatch).To(Equal("true"), "FIPS tag should remain 'true' after rejected PATCH")
			})

		It("should reject an HCP cluster creation with invalid FIPS tag value",
			labels.RequireNothing,
			labels.Medium,
			labels.Negative,
			labels.AroRpApiCompatible,
			labels.CreateCluster,
			func(ctx context.Context) {
				const (
					customerClusterName = "fips-invalid-cluster"
				)
				tc := framework.NewTestContext()

				if tc.UsePooledIdentities() {
					err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
					Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
				}

				By("creating a resource group")
				resourceGroup, err := tc.NewResourceGroup(ctx, "fips-invalid", tc.Location())
				Expect(err).NotTo(HaveOccurred(), "failed to create resource group for fips-invalid test")

				By("creating cluster parameters with invalid FIPS tag value")
				clusterParams := framework.NewDefaultClusterParams20251223()
				clusterParams.ClusterName = customerClusterName
				managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
				clusterParams.ManagedResourceGroupName = managedResourceGroupName
				clusterParams.Tags[api.TagClusterFIPSEnabled] = to.Ptr("yes")

				By("creating customer resources (infrastructure and managed identities)")
				clusterParams, err = tc.CreateClusterCustomerResources20251223(ctx,
					resourceGroup,
					clusterParams,
					map[string]interface{}{},
					TestArtifactsFS,
					framework.RBACScopeResourceGroup,
				)
				Expect(err).NotTo(HaveOccurred(), "failed to create cluster customer resources")

				By("attempting to create the hcp cluster with invalid FIPS value")
				clusterResource, err := framework.BuildHCPClusterFromParams20251223(clusterParams, tc.Location(), nil)
				Expect(err).NotTo(HaveOccurred(), "failed to build HCP cluster resource from params")
				_, err = framework.CreateHCPClusterAndWait20251223(
					ctx,
					GinkgoLogr,
					tc.Get20251223ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
					*resourceGroup.Name,
					customerClusterName,
					clusterResource,
					framework.ClusterCreationTimeout,
				)

				Expect(err).To(HaveOccurred(), "expected cluster creation to fail with invalid FIPS tag value")
				errMessage := "must be true or false"
				Expect(strings.ToLower(err.Error())).To(ContainSubstring(strings.ToLower(errMessage)), "error should indicate FIPS value must be true or false")
			})
	})
})
