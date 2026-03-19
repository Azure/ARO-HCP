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
	BeforeEach(func() {
		// do nothing. per test initialization usually ages better than shared.
	})

	It("should be able to create a cluster with default autoscaling and a nodepool with autoscaling enabled up to 500 replicas",
		labels.RequireNothing,
		labels.Medium,
		labels.Positive,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			const (
				customerClusterName = "np-autoscale-cluster"

				azNodePoolName         = "autoscale-az"
				azAutoscalingMin int32 = 1
				azAutoscalingMax int32 = 500
				availabilityZone       = "1"

				noAZNodePoolName         = "autoscale-noaz"
				noAZAutoscalingMin int32 = 1
				noAZAutoscalingMax int32 = 200
			)
			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			By("checking if the region supports availability zones")
			hasAZ, err := tc.LocationHasAvailabilityZones(ctx, "Standard_D8s_v3")
			Expect(err).NotTo(HaveOccurred())

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "np-autoscaling", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = customerClusterName
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources(ctx,
				resourceGroup,
				clusterParams,
				map[string]any{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
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

			By("verifying the cluster has default autoscaling parameters")
			clusterResp, err := framework.GetHCPCluster(ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName)
			Expect(err).NotTo(HaveOccurred())

			Expect(clusterResp.Properties).NotTo(BeNil(), "cluster response Properties was nil")
			Expect(clusterResp.Properties.Autoscaling).NotTo(BeNil(), "Expected cluster to have default autoscaling configuration")
			Expect(clusterResp.Properties.Autoscaling.MaxNodeProvisionTimeSeconds).To(Equal(to.Ptr(int32(900))), "Expected default MaxNodeProvisionTimeSeconds to be 900 seconds")
			Expect(clusterResp.Properties.Autoscaling.MaxPodGracePeriodSeconds).To(Equal(to.Ptr(int32(600))), "Expected default MaxPodGracePeriodSeconds to be 600 seconds")
			Expect(clusterResp.Properties.Autoscaling.PodPriorityThreshold).To(Equal(to.Ptr(int32(-10))), "Expected default PodPriorityThreshold to be -10")
			Expect(clusterResp.Properties.Autoscaling.MaxNodesTotal).To(BeNil(), "Expected MaxNodesTotal to be nil when not explicitly set")

			By("getting admin credentials for the cluster")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				10*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			nodePoolsClient := tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient()
			expectedNodeCount := 0

			if !hasAZ {
				By("skipping AZ nodepool creation: region does not support availability zones")
			} else {
				By("creating the AZ nodepool with 500 max replicas")
				azNodePoolParams := framework.NewDefaultNodePoolParams()
				azNodePoolParams.ClusterName = customerClusterName
				azNodePoolParams.NodePoolName = azNodePoolName
				azNodePoolParams.AutoScaling = &framework.NodePoolAutoScalingParams{
					Min: azAutoscalingMin,
					Max: azAutoscalingMax,
				}
				azNodePoolParams.AvailabilityZone = availabilityZone

				err = tc.CreateNodePoolFromParam(ctx,
					*resourceGroup.Name,
					customerClusterName,
					azNodePoolParams,
					45*time.Minute,
				)
				Expect(err).NotTo(HaveOccurred())

				By("verifying the AZ nodepool has the correct autoscaling configuration")
				azNodePoolResp, err := framework.GetNodePool(ctx,
					nodePoolsClient,
					*resourceGroup.Name,
					customerClusterName,
					azNodePoolName)
				Expect(err).NotTo(HaveOccurred())
				Expect(azNodePoolResp.Properties).NotTo(BeNil(), "nodepool response Properties was nil")
				Expect(azNodePoolResp.Properties.AutoScaling).NotTo(BeNil(), "Expected nodepool to have autoscaling configuration")
				Expect(azNodePoolResp.Properties.AutoScaling.Min).To(Equal(to.Ptr(azAutoscalingMin)))
				Expect(azNodePoolResp.Properties.AutoScaling.Max).To(Equal(to.Ptr(azAutoscalingMax)))

				expectedNodeCount += int(azAutoscalingMin)
				By("verifying node count after AZ nodepool creation")
				Expect(verifiers.VerifyNodeCount(expectedNodeCount).Verify(ctx, adminRESTConfig)).To(Succeed())

				By("updating the AZ nodepool max replicas from 500 to 2 before creating the next nodepool")
				_, err = framework.UpdateNodePoolAndWait(ctx,
					nodePoolsClient,
					*resourceGroup.Name,
					customerClusterName,
					azNodePoolName,
					hcpsdk20240610preview.NodePoolUpdate{
						Properties: &hcpsdk20240610preview.NodePoolPropertiesUpdate{
							AutoScaling: &hcpsdk20240610preview.NodePoolAutoScaling{
								Min: to.Ptr(azAutoscalingMin),
								Max: to.Ptr(int32(2)),
							},
						},
					},
					25*time.Minute,
				)
				Expect(err).NotTo(HaveOccurred())

				By("verifying the AZ nodepool max replicas was updated to 2")
				azNodePoolUpdatedResp, err := framework.GetNodePool(ctx,
					nodePoolsClient,
					*resourceGroup.Name,
					customerClusterName,
					azNodePoolName)
				Expect(err).NotTo(HaveOccurred())
				Expect(azNodePoolUpdatedResp.Properties).NotTo(BeNil(), "nodepool response Properties was nil")
				Expect(azNodePoolUpdatedResp.Properties.AutoScaling).NotTo(BeNil(), "Expected nodepool to have autoscaling configuration")
				Expect(azNodePoolUpdatedResp.Properties.AutoScaling.Max).To(Equal(to.Ptr(int32(2))), "Expected AZ nodepool max replicas to be updated to 2")
			}

			By("creating the no-AZ nodepool with 200 max replicas")
			noAZNodePoolParams := framework.NewDefaultNodePoolParams()
			noAZNodePoolParams.ClusterName = customerClusterName
			noAZNodePoolParams.NodePoolName = noAZNodePoolName
			noAZNodePoolParams.AutoScaling = &framework.NodePoolAutoScalingParams{
				Min: noAZAutoscalingMin,
				Max: noAZAutoscalingMax,
			}

			err = tc.CreateNodePoolFromParam(ctx,
				*resourceGroup.Name,
				customerClusterName,
				noAZNodePoolParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the no-AZ nodepool has the correct autoscaling configuration")
			noAZNodePoolResp, err := framework.GetNodePool(ctx,
				nodePoolsClient,
				*resourceGroup.Name,
				customerClusterName,
				noAZNodePoolName)
			Expect(err).NotTo(HaveOccurred())
			Expect(noAZNodePoolResp.Properties).NotTo(BeNil(), "nodepool response Properties was nil")
			Expect(noAZNodePoolResp.Properties.AutoScaling).NotTo(BeNil(), "Expected nodepool to have autoscaling configuration")
			Expect(noAZNodePoolResp.Properties.AutoScaling.Min).To(Equal(to.Ptr(noAZAutoscalingMin)))
			Expect(noAZNodePoolResp.Properties.AutoScaling.Max).To(Equal(to.Ptr(noAZAutoscalingMax)))

			expectedNodeCount += int(noAZAutoscalingMin)
			By("verifying node count after no-AZ nodepool creation")
			Expect(verifiers.VerifyNodeCount(expectedNodeCount).Verify(ctx, adminRESTConfig)).To(Succeed())

		})

	It("should respect cluster-wide node limits with nodepool autoscaling",
		labels.RequireNothing,
		labels.Medium,
		labels.Negative,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			const (
				customerClusterName  = "node-limit-cluster"
				customerNodePoolName = "exceeding-nodepool"
			)
			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "node-limit-test", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating cluster with MaxNodesTotal limit")
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = customerClusterName
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.Autoscaling = &hcpsdk20240610preview.ClusterAutoscalingProfile{
				MaxNodesTotal: to.Ptr(int32(3)), // Set low limit
			}

			clusterParams, err = tc.CreateClusterCustomerResources(ctx,
				resourceGroup,
				clusterParams,
				map[string]any{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred())

			err = tc.CreateHCPClusterFromParam(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("attempting to create nodepool with Min > MaxNodesTotal")
			nodePoolParams := framework.NewDefaultNodePoolParams()
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.AutoScaling = &framework.NodePoolAutoScalingParams{
				Min: 5, // Greater than cluster MaxNodesTotal (3)
				Max: 10,
			}

			// Should fail quickly during validation
			err = tc.CreateNodePoolFromParam(ctx,
				*resourceGroup.Name,
				customerClusterName,
				nodePoolParams,
				5*time.Minute,
			)
			Expect(err).To(HaveOccurred(), "Expected nodepool creation to fail when Min exceeds cluster MaxNodesTotal")
		})
})
