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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
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

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "autoscaling-cluster", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for autoscaling cluster test")

			clusterParams := framework.NewDefaultClusterParams20240610()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName
			clusterParams.Autoscaling = &hcpsdk20240610preview.ClusterAutoscalingProfile{
				MaxNodeProvisionTimeSeconds: to.Ptr(autoscalingMaxNodeProvisionTimeSeconds),
				MaxPodGracePeriodSeconds:    to.Ptr(autoscalingMaxPodGracePeriodSeconds),
				PodPriorityThreshold:        to.Ptr(autoscalingPodPriorityThreshold),
			}

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20240610(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{
					"customerNsgName":        customerNetworkSecurityGroupName,
					"customerVnetName":       customerVnetName,
					"customerVnetSubnetName": customerVnetSubnetName,
				},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for autoscaling cluster")

			By("creating the cluster")
			err = tc.CreateHCPClusterFromParam20240610(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %q with custom autoscaling", customerClusterName)

			By("ensuring the custom autoscaling was honored")
			got, err := framework.GetHCPCluster20240610(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get cluster %q to verify autoscaling settings", customerClusterName)
			Expect(got.Properties.Autoscaling).ToNot(BeNil(), "cluster Properties.Autoscaling was nil")
			Expect(got.Properties.Autoscaling.MaxNodeProvisionTimeSeconds).To(Equal(to.Ptr(autoscalingMaxNodeProvisionTimeSeconds)), "cluster autoscaling MaxNodeProvisionTimeSeconds should be %d", autoscalingMaxNodeProvisionTimeSeconds)
			Expect(got.Properties.Autoscaling.MaxPodGracePeriodSeconds).To(Equal(to.Ptr(autoscalingMaxPodGracePeriodSeconds)), "cluster autoscaling MaxPodGracePeriodSeconds should be %d", autoscalingMaxPodGracePeriodSeconds)
			Expect(got.Properties.Autoscaling.PodPriorityThreshold).To(Equal(to.Ptr(autoscalingPodPriorityThreshold)), "cluster autoscaling PodPriorityThreshold should be %d", autoscalingPodPriorityThreshold)

			By("creating the node pool")
			nodePoolParams := framework.NewDefaultNodePoolParams20240610()
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.Replicas = int32(2)

			err = tc.CreateNodePoolFromParam20240610(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				managedResourceGroupName,
				customerClusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create nodepool %q for autoscaling cluster", customerNodePoolName)

			By("patching the cluster to set maxNodesTotal")
			_, err = framework.UpdateHCPCluster20240610(
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
				framework.UpdateHCPClusterTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to patch cluster %q to set maxNodesTotal", customerClusterName)

		})

})
