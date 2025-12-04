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
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/util/rand"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Customer", func() {

	It("should not be able to deploy nodepool into a hosted cluster with failed provisioning state",
		labels.RequireNothing,
		labels.Negative,
		labels.Medium,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			tc := framework.NewTestContext()

			By("creating resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "rg-nodepool-into-failed-cluster", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			clusterName := "failed-cluster" + rand.String(6)

			By("creating cluster parameters and customer resources")
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = clusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			clusterParams, err = tc.CreateClusterCustomerResources(ctx,
				resourceGroup,
				clusterParams,
				map[string]any{
					"persistTagValue": false,
				},
				TestArtifactsFS,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating cluster with invalid subnet ID that will fail after ARM resource creation")
			subscriptionID := ""
			if resourceGroup.ID != nil {
				// Resource group ID format: /subscriptions/{subscription-id}/resourceGroups/{rg-name}
				parts := strings.Split(*resourceGroup.ID, "/")
				if len(parts) >= 3 {
					subscriptionID = parts[2]
				}
			}
			clusterParams.SubnetResourceID = fmt.Sprintf("/subscriptions/%s/resourceGroups/nonexistent-rg/providers/Microsoft.Network/virtualNetworks/nonexistent-vnet/subnets/nonexistent-subnet", subscriptionID)

			_, err = framework.BeginCreateHCPCluster(ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				clusterName,
				clusterParams,
				tc.Location(),
			)
			By("verifying error does not occur before ARM resource is created")
			Expect(err).NotTo(HaveOccurred())

			By("verifying the cluster resource exists")
			Eventually(func() bool {
				_, err := framework.GetHCPCluster(ctx, tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(), *resourceGroup.Name, clusterName)
				return err == nil
			}, 2*time.Minute, 5*time.Second).Should(BeTrue(), "Cluster ARM resource should be created")

			By("waiting for cluster to reach failed provisioning state")
			Eventually(func() hcpsdk20240610preview.ProvisioningState {
				cluster, err := framework.GetHCPCluster(ctx, tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(), *resourceGroup.Name, clusterName)
				if err != nil {
					GinkgoLogr.Error(err, "Error getting cluster")
					return ""
				}
				if cluster.Properties != nil && cluster.Properties.ProvisioningState != nil {
					currentState := *cluster.Properties.ProvisioningState
					GinkgoLogr.Info("Current cluster provisioning state", "state", currentState)
					return currentState
				}
				return ""
			}, 30*time.Minute, 10*time.Second).Should(Equal(hcpsdk20240610preview.ProvisioningStateFailed))

			By("attempting to deploy nodepool into cluster with failed provisioning state")
			nodePoolParams := framework.NewDefaultNodePoolParams()
			nodePoolParams.ClusterName = clusterName
			nodePoolParams.NodePoolName = "nodepool1"
			nodePoolParams.VMSize = "Standard_D2s_v3"
			nodePoolParams.Replicas = int32(1)

			err = tc.CreateNodePoolFromParam(ctx,
				*resourceGroup.Name,
				clusterName,
				nodePoolParams,
				5*time.Minute,
			)

			By("verifying nodepool deployment failed with appropriate error code")
			Expect(err).To(HaveOccurred())
			GinkgoLogr.Error(err, "nodepool deployment error")
			Expect(err.Error()).To(ContainSubstring("Node pools can only be created on clusters in 'ready' state, cluster requested is in 'error' state."))

		})

})
