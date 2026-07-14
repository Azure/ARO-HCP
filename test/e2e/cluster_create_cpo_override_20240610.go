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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = FDescribe("Create HCPOpenShiftCluster with CPO override (v20240610preview)", func() {
	It("should create a 5.0 cluster with CPO image override using v20240610preview API",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.CreateCluster,
		func(ctx context.Context) {
			const customerClusterName = "cpo-override-2406"

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "cpo-override-2406", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for CPO override test")

			const channelGroup = "candidate"

			By("resolving 5.0 install version")
			cpVersion, err := framework.GetLatestInstallVersion(ctx, channelGroup, "5.0")
			Expect(err).NotTo(HaveOccurred(), "failed to resolve 5.0 install version")

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams20240610()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName
			clusterParams.OpenshiftVersionId = cpVersion
			clusterParams.ChannelGroup = channelGroup
			clusterParams.Tags[api.TagClusterCPOImageOverride] = to.Ptr(cpoOverrideImage)

			By("creating customer resources (infrastructure and managed identities)")
			clusterParams, err = tc.CreateClusterCustomerResources20240610(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for CPO override cluster")

			By("creating the HCP cluster")
			err = tc.CreateHCPClusterFromParam20240610(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %q with CPO override", customerClusterName)

			By("creating the node pool")
			nodePoolParams := framework.NewDefaultNodePoolParams20240610()
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = "np-1"
			nodePoolParams.Replicas = int32(2)
			nodePoolParams.OpenshiftVersionId = cpVersion
			nodePoolParams.ChannelGroup = channelGroup

			err = tc.CreateNodePoolFromParam20240610(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				managedResourceGroupName,
				customerClusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create node pool for CPO override cluster %q", customerClusterName)

			By("getting admin credentials for the cluster")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20240610(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for CPO override cluster %q", customerClusterName)

			By("verifying the cluster is viable")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to verify CPO override cluster %q is viable", customerClusterName)
		})
})
