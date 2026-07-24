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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Customer", func() {
	BeforeEach(func() {
		// do nothing.  per test initialization usually ages better than shared.
	})

	It("should update cluster autoscaling via PATCH and keep the cluster operational",
		labels.RequireNothing,
		labels.High,
		labels.Positive,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			const (
				customerNetworkSecurityGroupName = "customer-nsg-name"
				customerVnetName                 = "customer-vnet-name"
				customerVnetSubnetName           = "customer-vnet-subnet1"
				customerClusterName              = "autoscaling-hcp-cluster"
				customerNodePoolName             = "np-1"

				// These must differ from RP defaults. See the SDK's models.go for defaults.
				autoscalingMaxPodGracePeriodSeconds    int32 = 700  // default is 600
				autoscalingMaxNodeProvisionTimeSeconds int32 = 1000 // default is 900
				autoscalingPodPriorityThreshold        int32 = -11  // default is -10

				// Updated values applied via PATCH after initial provisioning.
				updatedMaxNodesTotal               int32 = 498
				updatedMaxPodGracePeriodSeconds    int32 = 650
				updatedMaxNodeProvisionTimeSeconds int32 = 950
				updatedPodPriorityThreshold        int32 = -12
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

			hcpClient := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()

			By("creating the cluster with custom autoscaling configuration")
			err = tc.CreateHCPClusterFromParam20240610(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %q with custom autoscaling", customerClusterName)

			By("ensuring the custom autoscaling configuration was honored at create time")
			got, err := framework.GetHCPCluster20240610(
				ctx,
				hcpClient,
				*resourceGroup.Name,
				customerClusterName,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get cluster %q to verify autoscaling settings", customerClusterName)
			Expect(got.Properties).NotTo(BeNil(), "cluster response Properties was nil")
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

			By("getting admin credentials for the cluster")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20240610(
				ctx,
				hcpClient,
				*resourceGroup.Name,
				customerClusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for cluster %q", customerClusterName)

			By("verifying basic cluster health before autoscaling update")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to verify HCP cluster health before autoscaling update")

			By("updating the cluster autoscaling configuration via PATCH")
			updateResp, err := framework.UpdateHCPCluster20240610(
				ctx,
				hcpClient,
				*resourceGroup.Name,
				customerClusterName,
				hcpsdk20240610preview.HcpOpenShiftClusterUpdate{
					Properties: &hcpsdk20240610preview.HcpOpenShiftClusterPropertiesUpdate{
						Autoscaling: &hcpsdk20240610preview.ClusterAutoscalingProfile{
							MaxNodesTotal:               to.Ptr(updatedMaxNodesTotal),
							MaxNodeProvisionTimeSeconds: to.Ptr(updatedMaxNodeProvisionTimeSeconds),
							MaxPodGracePeriodSeconds:    to.Ptr(updatedMaxPodGracePeriodSeconds),
							PodPriorityThreshold:        to.Ptr(updatedPodPriorityThreshold),
						},
					},
				},
				framework.UpdateHCPClusterTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to update cluster %q autoscaling configuration", customerClusterName)

			By("verifying the autoscaling update operation completed successfully")
			Expect(updateResp).NotTo(BeNil(), "autoscaling update response was nil")
			Expect(updateResp.Properties).NotTo(BeNil(), "autoscaling update response Properties was nil")
			Expect(updateResp.Properties.ProvisioningState).NotTo(BeNil(), "autoscaling update response ProvisioningState was nil")
			Expect(*updateResp.Properties.ProvisioningState).To(Equal(hcpsdk20240610preview.ProvisioningStateSucceeded), "cluster provisioning state should be Succeeded after autoscaling update")
			Expect(updateResp.Properties.Autoscaling).NotTo(BeNil(), "autoscaling update response Properties.Autoscaling was nil")
			Expect(updateResp.Properties.Autoscaling.MaxNodesTotal).To(Equal(to.Ptr(updatedMaxNodesTotal)), "update response MaxNodesTotal should be %d", updatedMaxNodesTotal)
			Expect(updateResp.Properties.Autoscaling.MaxNodeProvisionTimeSeconds).To(Equal(to.Ptr(updatedMaxNodeProvisionTimeSeconds)), "update response MaxNodeProvisionTimeSeconds should be %d", updatedMaxNodeProvisionTimeSeconds)
			Expect(updateResp.Properties.Autoscaling.MaxPodGracePeriodSeconds).To(Equal(to.Ptr(updatedMaxPodGracePeriodSeconds)), "update response MaxPodGracePeriodSeconds should be %d", updatedMaxPodGracePeriodSeconds)
			Expect(updateResp.Properties.Autoscaling.PodPriorityThreshold).To(Equal(to.Ptr(updatedPodPriorityThreshold)), "update response PodPriorityThreshold should be %d", updatedPodPriorityThreshold)

			By("verifying the updated autoscaling configuration via GET")
			clusterAfterAutoscalingUpdate, err := framework.GetHCPCluster20240610(
				ctx,
				hcpClient,
				*resourceGroup.Name,
				customerClusterName,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get cluster %q after autoscaling update", customerClusterName)
			Expect(clusterAfterAutoscalingUpdate.Properties).NotTo(BeNil(), "cluster response Properties was nil after autoscaling update")
			Expect(clusterAfterAutoscalingUpdate.Properties.Autoscaling).NotTo(BeNil(), "cluster Properties.Autoscaling was nil after autoscaling update")
			Expect(clusterAfterAutoscalingUpdate.Properties.Autoscaling.MaxNodesTotal).To(Equal(to.Ptr(updatedMaxNodesTotal)), "GET response MaxNodesTotal should be %d after update", updatedMaxNodesTotal)
			Expect(clusterAfterAutoscalingUpdate.Properties.Autoscaling.MaxNodeProvisionTimeSeconds).To(Equal(to.Ptr(updatedMaxNodeProvisionTimeSeconds)), "GET response MaxNodeProvisionTimeSeconds should be %d after update", updatedMaxNodeProvisionTimeSeconds)
			Expect(clusterAfterAutoscalingUpdate.Properties.Autoscaling.MaxPodGracePeriodSeconds).To(Equal(to.Ptr(updatedMaxPodGracePeriodSeconds)), "GET response MaxPodGracePeriodSeconds should be %d after update", updatedMaxPodGracePeriodSeconds)
			Expect(clusterAfterAutoscalingUpdate.Properties.Autoscaling.PodPriorityThreshold).To(Equal(to.Ptr(updatedPodPriorityThreshold)), "GET response PodPriorityThreshold should be %d after update", updatedPodPriorityThreshold)

			By("verifying cluster workloads and pods remain operational after autoscaling update")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig, verifiers.VerifySimpleWebApp())
			Expect(err).NotTo(HaveOccurred(), "failed to verify cluster workloads and pods after autoscaling update")

			By("verifying admin credential auth flow remains operational after autoscaling update")
			kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create Kubernetes client from admin REST config")
			_, err = kubeClient.CoreV1().Namespaces().List(ctx, metav1.ListOptions{Limit: 1})
			Expect(err).NotTo(HaveOccurred(), "failed to list namespaces using admin credentials after autoscaling update")
		})

})
