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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Customer", func() {
	It("should be able to update nodepool replicas and autoscaling",
		labels.RequireNothing,
		labels.High,
		labels.Positive,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			const (
				customerClusterName  = "np-update-nodes-hcp-cluster"
				customerNodePoolName = "np-update-nodes"
			)

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "nodepool-update-nodes", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

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

			By("creating the HCP cluster")
			err = tc.CreateHCPClusterFromParam(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("getting admin credentials for the cluster")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				10*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating the node pool")
			nodePoolParams := framework.NewDefaultNodePoolParams()
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.Replicas = int32(2)

			err = tc.CreateNodePoolFromParam(ctx,
				*resourceGroup.Name,
				customerClusterName,
				nodePoolParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("verifying nodes count and status after initial creation")
			Expect(verifiers.VerifyNodeCount(int(nodePoolParams.Replicas)).Verify(ctx, adminRESTConfig)).To(Succeed())
			Expect(verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)).To(Succeed())

			By("scaling up the nodepool replicas from 2 to 3 replicas")
			update := hcpsdk20240610preview.NodePoolUpdate{
				Properties: &hcpsdk20240610preview.NodePoolPropertiesUpdate{
					Replicas: to.Ptr(int32(3)),
				},
			}
			scaleUpResp, err := framework.UpdateNodePoolAndWait(ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient(),
				*resourceGroup.Name,
				customerClusterName,
				customerNodePoolName,
				update,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(scaleUpResp.Properties).NotTo(BeNil())
			Expect(scaleUpResp.Properties.Replicas).NotTo(BeNil())
			Expect(*scaleUpResp.Properties.Replicas).To(Equal(int32(3)))

			By("verifying nodes count and status after scaling up")
			Expect(verifiers.VerifyNodeCount(3).Verify(ctx, adminRESTConfig)).To(Succeed())
			Expect(verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)).To(Succeed())

			By("scaling down the nodepool replicas from 3 to 1 replicas")
			update = hcpsdk20240610preview.NodePoolUpdate{
				Properties: &hcpsdk20240610preview.NodePoolPropertiesUpdate{
					Replicas: to.Ptr(int32(1)),
				},
			}
			scaleDownResp, err := framework.UpdateNodePoolAndWait(ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient(),
				*resourceGroup.Name,
				customerClusterName,
				customerNodePoolName,
				update,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(scaleDownResp.Properties).NotTo(BeNil())
			Expect(scaleDownResp.Properties.Replicas).NotTo(BeNil())
			Expect(*scaleDownResp.Properties.Replicas).To(Equal(int32(1)))

			By("verifying nodes count and status after scaling down")
			Expect(verifiers.VerifyNodeCount(1).Verify(ctx, adminRESTConfig)).To(Succeed())
			Expect(verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)).To(Succeed())

			By("enabling autoscaling with min 2 and max 3 replicas")
			update = hcpsdk20240610preview.NodePoolUpdate{
				Properties: &hcpsdk20240610preview.NodePoolPropertiesUpdate{
					AutoScaling: &hcpsdk20240610preview.NodePoolAutoScaling{
						Min: to.Ptr(int32(2)),
						Max: to.Ptr(int32(3)),
					},
				},
			}
			autoscaleResp, err := framework.UpdateNodePoolAndWait(ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient(),
				*resourceGroup.Name,
				customerClusterName,
				customerNodePoolName,
				update,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(autoscaleResp.Properties).NotTo(BeNil())
			Expect(autoscaleResp.Properties.AutoScaling).NotTo(BeNil())
			Expect(autoscaleResp.Properties.AutoScaling.Min).NotTo(BeNil())
			Expect(autoscaleResp.Properties.AutoScaling.Max).NotTo(BeNil())
			Expect(*autoscaleResp.Properties.AutoScaling.Min).To(Equal(int32(2)))
			Expect(*autoscaleResp.Properties.AutoScaling.Max).To(Equal(int32(3)))

			By("verifying nodes count and status after enabling autoscaling")
			Expect(verifiers.VerifyNodeCount(2).Verify(ctx, adminRESTConfig)).To(Succeed())
			Expect(verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)).To(Succeed())
		})
})
