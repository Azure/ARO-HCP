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
)

var _ = Describe("NodePool Autoscaling", func() {
	BeforeEach(func() {
		// do nothing. per test initialization usually ages better than shared.
	})

	It("should be able to create a cluster with default autoscaling and a nodepool with autoscaling enabled",
		labels.RequireNothing,
		labels.Medium,
		labels.Positive,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			const (
				customerClusterName  = "np-autoscale-cluster"
				customerNodePoolName = "autoscale-np"

				// Autoscaling configuration
				autoscalingMin int32 = 1
				autoscalingMax int32 = 5
			)
			tc := framework.NewTestContext()

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "np-autoscaling", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = customerClusterName
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)

			By("creating customer resources")
			clusterParams, err = framework.CreateClusterCustomerResources(ctx,
				tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				resourceGroup,
				clusterParams,
				map[string]interface{}{
					"persistTagValue": false,
				},
				TestArtifactsFS,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating the HCP cluster")
			err = framework.CreateHCPClusterFromParam(ctx,
				tc,
				*resourceGroup.Name,
				clusterParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating nodepool parameters with autoscaling enabled")
			nodePoolParams := framework.NewDefaultNodePoolParams()
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = customerNodePoolName
			// Enable autoscaling instead of fixed replicas
			nodePoolParams.AutoScaling = &framework.NodePoolAutoScalingParams{
				Min: autoscalingMin,
				Max: autoscalingMax,
			}

			By("creating the autoscaling nodepool")
			err = framework.CreateNodePoolFromParam(ctx,
				tc,
				*resourceGroup.Name,
				customerClusterName,
				nodePoolParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the nodepool has autoscaling configured")
			nodePoolClient := tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient()
			nodePoolResp, err := nodePoolClient.Get(ctx, *resourceGroup.Name, customerClusterName, customerNodePoolName, nil)
			Expect(err).NotTo(HaveOccurred())

			Expect(nodePoolResp.Properties).NotTo(BeNil())
			Expect(nodePoolResp.Properties.AutoScaling).NotTo(BeNil(), "Expected autoscaling to be configured on the nodepool")
			Expect(nodePoolResp.Properties.AutoScaling.Min).To(Equal(to.Ptr(autoscalingMin)), "Expected autoscaling min to be %d", autoscalingMin)
			Expect(nodePoolResp.Properties.AutoScaling.Max).To(Equal(to.Ptr(autoscalingMax)), "Expected autoscaling max to be %d", autoscalingMax)
			// Replicas should be nil when autoscaling is enabled
			Expect(nodePoolResp.Properties.Replicas).To(BeNil(), "Expected replicas to be nil when autoscaling is enabled")
		})

	It("should be able to update a nodepool from fixed replicas to autoscaling",
		labels.RequireNothing,
		labels.Medium,
		labels.Positive,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			const (
				customerClusterName  = "np-scale-update"
				customerNodePoolName = "update-np"

				initialReplicas    int32 = 2
				updatedMinReplicas int32 = 1
				updatedMaxReplicas int32 = 4
			)
			tc := framework.NewTestContext()

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "np-scale-update", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = customerClusterName
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)

			By("creating customer resources")
			clusterParams, err = framework.CreateClusterCustomerResources(ctx,
				tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				resourceGroup,
				clusterParams,
				map[string]interface{}{
					"persistTagValue": false,
				},
				TestArtifactsFS,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating the HCP cluster")
			err = framework.CreateHCPClusterFromParam(ctx,
				tc,
				*resourceGroup.Name,
				clusterParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating nodepool with fixed replicas")
			nodePoolParams := framework.NewDefaultNodePoolParams()
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.Replicas = initialReplicas

			err = framework.CreateNodePoolFromParam(ctx,
				tc,
				*resourceGroup.Name,
				customerClusterName,
				nodePoolParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the nodepool has fixed replicas")
			nodePoolClient := tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient()
			nodePoolResp, err := nodePoolClient.Get(ctx, *resourceGroup.Name, customerClusterName, customerNodePoolName, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(nodePoolResp.Properties.Replicas).To(Equal(to.Ptr(initialReplicas)))
			Expect(nodePoolResp.Properties.AutoScaling).To(BeNil())

			By("updating the nodepool to use autoscaling")
			updateResp, err := nodePoolClient.BeginCreateOrUpdate(
				ctx,
				*resourceGroup.Name,
				customerClusterName,
				customerNodePoolName,
				hcpsdk20240610preview.NodePool{
					Location: to.Ptr(tc.Location()),
					Properties: &hcpsdk20240610preview.NodePoolProperties{
						Version: nodePoolResp.Properties.Version,
						Platform: &hcpsdk20240610preview.NodePoolPlatformProfile{
							VMSize: nodePoolResp.Properties.Platform.VMSize,
							OSDisk: nodePoolResp.Properties.Platform.OSDisk,
						},
						AutoScaling: &hcpsdk20240610preview.NodePoolAutoScaling{
							Min: to.Ptr(updatedMinReplicas),
							Max: to.Ptr(updatedMaxReplicas),
						},
					},
				},
				nil,
			)
			Expect(err).NotTo(HaveOccurred())

			_, err = updateResp.PollUntilDone(ctx, nil)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the nodepool now has autoscaling enabled")
			updatedNodePool, err := nodePoolClient.Get(ctx, *resourceGroup.Name, customerClusterName, customerNodePoolName, nil)
			Expect(err).NotTo(HaveOccurred())

			Expect(updatedNodePool.Properties.AutoScaling).NotTo(BeNil(), "Expected autoscaling to be configured after update")
			Expect(updatedNodePool.Properties.AutoScaling.Min).To(Equal(to.Ptr(updatedMinReplicas)))
			Expect(updatedNodePool.Properties.AutoScaling.Max).To(Equal(to.Ptr(updatedMaxReplicas)))
		})
})
