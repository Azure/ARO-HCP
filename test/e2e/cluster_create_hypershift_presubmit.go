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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/util/rand"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("ARO-HCP HyperShift Presubmit", func() {
	It("should create a cluster and nodepool to completion",
		labels.HypershiftPresubmit,
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.MIContainers(1),
		func(ctx context.Context) {
			clusterParams := framework.NewDefaultClusterParams20251223()

			suffix := rand.String(6)
			clusterName := "cluster-hs-pre-" + suffix
			clusterParams.ClusterName = clusterName

			tc := framework.NewTestContext()
			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "rg-hs-pre-", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group")

			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name+"-"+suffix, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20251223(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{
					"customerNsgName":        "customer-nsg-hs-pre-" + suffix,
					"customerVnetName":       "customer-vnet-hs-pre-" + suffix,
					"customerVnetSubnetName": "customer-vnet-subnet-hs-pre-" + suffix,
				},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for cluster %q", clusterName)

			By(fmt.Sprintf("creating the HCP cluster with version '%s' on %s channel", clusterParams.OpenshiftVersionId, clusterParams.ChannelGroup))
			err = tc.CreateHCPClusterFromParam20251223(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "HCP cluster %s/%s should provision", *resourceGroup.Name, clusterName)

			By("verifying the cluster is viable")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20240610(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				clusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for cluster %q", clusterName)
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to verify HCP cluster %q is viable", clusterName)

			nodePoolName := "np-" + suffix
			nodePoolParams := framework.NewDefaultNodePoolParams20251223()
			nodePoolParams.ClusterName = clusterName
			nodePoolParams.NodePoolName = nodePoolName

			By(fmt.Sprintf("creating node pool %q with version '%s' on %s channel", nodePoolName, nodePoolParams.OpenshiftVersionId, nodePoolParams.ChannelGroup))
			err = tc.CreateNodePoolFromParam20251223(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				managedResourceGroupName,
				clusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create node pool %q for cluster %q", nodePoolName, clusterName)

			By("verifying a simple web app can run")
			err = verifiers.VerifySimpleWebApp().Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to verify simple web app runs on cluster %q", clusterName)
		},
	)
})
