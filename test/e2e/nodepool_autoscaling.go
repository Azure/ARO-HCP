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
				customerClusterName  = "np-autoscale-cluster"
				customerNodePoolName = "autoscale-np"

				// Autoscaling configuration
				autoscalingMin int32 = 1
				autoscalingMax int32 = 500
			)
			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

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
			err = tc.CreateNodePoolFromParam(ctx,
				*resourceGroup.Name,
				customerClusterName,
				nodePoolParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the cluster has default autoscaling parameters")
			clusterResp, err := framework.GetHCPCluster(ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName)
			Expect(err).NotTo(HaveOccurred())

			// Verify cluster autoscaling defaults are applied
			Expect(clusterResp.Properties).NotTo(BeNil())
			Expect(clusterResp.Properties.Autoscaling).NotTo(BeNil(), "Expected cluster to have default autoscaling configuration")
			Expect(clusterResp.Properties.Autoscaling.MaxNodeProvisionTimeSeconds).To(Equal(to.Ptr(int32(900))), "Expected default MaxNodeProvisionTimeSeconds to be 900 seconds")
			Expect(clusterResp.Properties.Autoscaling.MaxPodGracePeriodSeconds).To(Equal(to.Ptr(int32(600))), "Expected default MaxPodGracePeriodSeconds to be 600 seconds")
			Expect(clusterResp.Properties.Autoscaling.PodPriorityThreshold).To(Equal(to.Ptr(int32(-10))), "Expected default PodPriorityThreshold to be -10")
			// MaxNodesTotal should be nil (no maximum limit) when not explicitly set
			Expect(clusterResp.Properties.Autoscaling.MaxNodesTotal).To(BeNil(), "Expected MaxNodesTotal to be nil when not explicitly set")

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
