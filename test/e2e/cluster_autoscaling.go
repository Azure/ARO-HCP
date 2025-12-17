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
		// do nothing.  per test initialization usually ages better than shared.
	})

	It("should be able to create an HCP cluster with custom autoscaling",
		labels.RequireNothing,
		labels.Medium,
		labels.Positive,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			const (
				customerNetworkSecurityGroupName = "customer-nsg-name"
				customerVnetName                 = "customer-vnet-name"
				customerVnetSubnetName           = "customer-vnet-subnet1"
				customerClusterName              = "autoscaling-hcp-cluster"
				customerNodePoolName             = "np-1"

				// These must differ from RP defaults.
				// See the SDK's models.go for defaults.
				autoscalingMaxPodGracePeriodSeconds    int32 = 700  // default is 600
				autoscalingMaxNodesTotal               int32 = 3    // default is to omit
				autoscalingMaxNodeProvisionTimeSeconds int32 = 1000 // default is 900
				autoscalingPodPriorityThreshold        int32 = -11  // default is -10
			)
			tc := framework.NewTestContext()

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "autoscaling-cluster", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = customerClusterName
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.Autoscaling = &hcpsdk20240610preview.ClusterAutoscalingProfile{
				MaxNodeProvisionTimeSeconds: to.Ptr(autoscalingMaxNodeProvisionTimeSeconds),
				MaxPodGracePeriodSeconds:    to.Ptr(autoscalingMaxPodGracePeriodSeconds),
				PodPriorityThreshold:        to.Ptr(autoscalingPodPriorityThreshold),
			}

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{
					"persistTagValue":        false,
					"customerNsgName":        customerNetworkSecurityGroupName,
					"customerVnetName":       customerVnetName,
					"customerVnetSubnetName": customerVnetSubnetName,
				},
				TestArtifactsFS)
			Expect(err).NotTo(HaveOccurred())

			By("creating the cluster")
			err = tc.CreateHCPClusterFromParam(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("ensuring the custom autoscaling was honored")
			got, err := framework.GetHCPCluster(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Properties.Autoscaling).ToNot(BeNil())
			Expect(got.Properties.Autoscaling.MaxNodeProvisionTimeSeconds).To(Equal(to.Ptr(autoscalingMaxNodeProvisionTimeSeconds)))
			Expect(got.Properties.Autoscaling.MaxPodGracePeriodSeconds).To(Equal(to.Ptr(autoscalingMaxPodGracePeriodSeconds)))
			Expect(got.Properties.Autoscaling.PodPriorityThreshold).To(Equal(to.Ptr(autoscalingPodPriorityThreshold)))

			By("creating the node pool")
			nodePoolParams := framework.NewDefaultNodePoolParams()
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.Replicas = int32(2)

			err = tc.CreateNodePoolFromParam(ctx,
				*resourceGroup.Name,
				customerClusterName,
				nodePoolParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("patching the cluster to set maxNodesTotal")
			update, err := framework.UpdateHCPCluster(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				hcpsdk20240610preview.HcpOpenShiftClusterUpdate{
					Properties: &hcpsdk20240610preview.HcpOpenShiftClusterPropertiesUpdate{
						Autoscaling: &hcpsdk20240610preview.ClusterAutoscalingProfile{
							MaxNodesTotal: to.Ptr(autoscalingMaxNodesTotal),
						},
					},
				},
				5*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Properties.Autoscaling).ToNot(BeNil())
			Expect(update.Properties.Autoscaling.MaxNodesTotal).To(Equal(to.Ptr(autoscalingMaxNodesTotal)))
		})

	It("should reject cluster creation with invalid autoscaling parameters",
		labels.RequireNothing,
		labels.High,
		labels.Negative,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			testCases := []struct {
				name        string
				autoscaling *hcpsdk20240610preview.ClusterAutoscalingProfile
				description string
			}{
				{
					name: "negativeMaxNodeProvisionTimeSeconds",
					autoscaling: &hcpsdk20240610preview.ClusterAutoscalingProfile{
						MaxNodeProvisionTimeSeconds: to.Ptr(int32(-1)),
					},
					description: "should reject negative MaxNodeProvisionTimeSeconds",
				},
				{
					name: "negativeMaxPodGracePeriodSeconds",
					autoscaling: &hcpsdk20240610preview.ClusterAutoscalingProfile{
						MaxPodGracePeriodSeconds: to.Ptr(int32(-1)),
					},
					description: "should reject zero MaxPodGracePeriodSeconds",
				},
				{
					name: "highMaxNodesTotal",
					autoscaling: &hcpsdk20240610preview.ClusterAutoscalingProfile{
						MaxNodesTotal: to.Ptr(int32(100000)),
					},
					description: "should reject unreasonably high MaxNodesTotal",
				},
			}

			for _, testCase := range testCases {
				By(testCase.description)
				tc := framework.NewTestContext()

				resourceGroup, err := tc.NewResourceGroup(ctx, "autoscaling-"+testCase.name, tc.Location())
				Expect(err).NotTo(HaveOccurred())

				clusterParams := framework.NewDefaultClusterParams()
				clusterParams.ClusterName = "invalid-cluster-" + testCase.name
				clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)
				clusterParams.Autoscaling = testCase.autoscaling

				clusterParams, err = tc.CreateClusterCustomerResources(ctx,
					resourceGroup,
					clusterParams,
					map[string]interface{}{
						"persistTagValue": false,
					},
					TestArtifactsFS)
				Expect(err).NotTo(HaveOccurred())

				// Creating cluster with invalid autoscaling should fail
				err = tc.CreateHCPClusterFromParam(ctx,
					GinkgoLogr,
					*resourceGroup.Name,
					clusterParams,
					45*time.Minute,
				)
				Expect(err).To(HaveOccurred(), "Expected cluster creation to fail with invalid autoscaling parameter: %s", testCase.name)
			}
		})

	It("should reject cluster updates with invalid autoscaling values",
		labels.RequireNothing,
		labels.High,
		labels.Negative,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			const (
				customerClusterName = "update-invalid-cluster"
			)
			tc := framework.NewTestContext()

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "update-invalid-autoscaling", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating a valid cluster first")
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = customerClusterName
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)

			clusterParams, err = tc.CreateClusterCustomerResources(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{
					"persistTagValue": false,
				},
				TestArtifactsFS)
			Expect(err).NotTo(HaveOccurred())

			err = tc.CreateHCPClusterFromParam(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating a nodepool to establish current node count")
			nodePoolParams := framework.NewDefaultNodePoolParams()
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = "test-nodepool"
			nodePoolParams.Replicas = int32(3)

			err = tc.CreateNodePoolFromParam(ctx,
				*resourceGroup.Name,
				customerClusterName,
				nodePoolParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("attempting invalid cluster autoscaling updates")
			client := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()

			// Try to set MaxNodesTotal below current node count
			_, err = framework.UpdateHCPCluster(
				ctx,
				client,
				*resourceGroup.Name,
				customerClusterName,
				hcpsdk20240610preview.HcpOpenShiftClusterUpdate{
					Properties: &hcpsdk20240610preview.HcpOpenShiftClusterPropertiesUpdate{
						Autoscaling: &hcpsdk20240610preview.ClusterAutoscalingProfile{
							MaxNodesTotal: to.Ptr(int32(1)), // Lower than existing nodes (3)
						},
					},
				},
				5*time.Minute,
			)
			Expect(err).To(HaveOccurred(), "Expected update to fail when MaxNodesTotal is below current node count")
		})
})
