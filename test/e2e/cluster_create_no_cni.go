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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hcpsdk "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Customer", func() {
	It("should be able to create a HCP cluster without CNI",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			const (
				customerClusterName  = "no-cni-cl"
				customerNodePoolName = "no-cni-np"
			)
			tc := framework.NewTestContext()

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "e2e-no-cni", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName
			By("setting no cni network configuration")
			clusterParams.Network.NetworkType = "Other"
			clusterParams.Network.PodCIDR = "10.128.0.0/14"
			clusterParams.Network.ServiceCIDR = "172.30.0.0/16"
			clusterParams.Network.MachineCIDR = "10.0.0.0/16"
			clusterParams.Network.HostPrefix = 23

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{
					"persistTagValue": false,
				},
				TestArtifactsFS,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating no-cni HCP cluster")
			err = tc.CreateHCPClusterFromParam(ctx,
				*resourceGroup.Name,
				clusterParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("getting credentials and verifying cluster is available")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				10*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(verifiers.VerifyHCPCluster(ctx, adminRESTConfig)).To(Succeed())

			By("creating the node pool")
			nodePoolParams := framework.NewDefaultNodePoolParams()
			nodePoolParams.NodePoolName = customerNodePoolName

			err = tc.CreateNodePoolFromParam(ctx,
				*resourceGroup.Name,
				customerClusterName,
				nodePoolParams,
				45*time.Minute,
			)
			// ARO-20829 workaround: instead of a finished and successful
			// deployment, we expect that the provisioning is still going on
			Expect(err).To(HaveOccurred())
			By("expecting the node pool to be still deploying because of ARO-20829")
			nodePool, err := framework.GetNodePool(ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient(),
				*resourceGroup.Name,
				customerClusterName,
				customerNodePoolName,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(nodePool.Properties).ToNot(BeNil())
			Expect(nodePool.Properties.ProvisioningState).ToNot(BeNil())
			Expect(*nodePool.Properties.ProvisioningState).To(Equal(hcpsdk.ProvisioningStateProvisioning))

			By("expecting that on a cluster without CNI plugin, nodes are in NotReady state")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig, verifiers.VerifyNodesReady())
			Expect(err).To(HaveOccurred())

			// TODO: add cilium setup here, and then rerun VerifyHCPCluster
			// with VerifyNodesReady() expecting it to pass
		},
	)
})
