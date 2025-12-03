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

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Customer", func() {
	BeforeEach(func() {
		// do nothing.  per test initialization usually ages better than shared.
	})

	It("should not be able to deploy a node pool in a cluster that is in provisioning state",
		labels.RequireNothing,
		labels.Critical,
		labels.Negative,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			const (
				customerNetworkSecurityGroupName = "np-in-prov-cluster-nsg-name"
				customerVnetName                 = "np-in-prov-cluster-vnet-name"
				customerVnetSubnetName           = "np-in-prov-cluster-vnet-subnet1"
				customerClusterName              = "np-in-prov-cluster"
				customerNodePoolName             = "np-1"
				openshiftControlPlaneVersionId   = "4.19"
				openshiftNodeVersionId           = "4.19.7"
			)
			tc := framework.NewTestContext()

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "np-in-prov-cluster", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName
			clusterParams.OpenshiftVersionId = openshiftControlPlaneVersionId

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
				TestArtifactsFS,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating the cluster")
			err = tc.CreateHCPClusterFromParam(ctx,
				*resourceGroup.Name,
				clusterParams,
				0*time.Second, // Don't wait for the deployment to be finished
			)
			Expect(err).NotTo(HaveOccurred())

			// Give 20 minutes for the cluster to reach Provisioning state
			clusterClient := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
			Eventually(func() hcpsdk20240610preview.ProvisioningState {
				By("waiting for the cluster to be in Provisioning state")
				cluster, err := clusterClient.Get(ctx, *resourceGroup.Name, customerClusterName, nil)
				if err != nil {
					return ""
				}
				if cluster.Properties != nil && cluster.Properties.ProvisioningState != nil {
					return *cluster.Properties.ProvisioningState
				}
				return ""

			}, 20*time.Minute, 30*time.Second).Should(Equal(hcpsdk20240610preview.ProvisioningStateProvisioning))

			By("creating the node pool while the cluster is in Provisioning state")
			nodePoolParams := framework.NewDefaultNodePoolParams()
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.OpenshiftVersionId = openshiftNodeVersionId
			nodePoolParams.Replicas = int32(1)

			err = tc.CreateNodePoolFromParam(ctx,
				*resourceGroup.Name,
				customerClusterName,
				nodePoolParams,
				45*time.Minute,
			)

			By("verifying node pool failed to deploy")
			Expect(err).To(HaveOccurred())

			By("verifying the error message matches the expected")
			Expect(err.Error()).To(ContainSubstring("Cannot create resource while parent resource is provisioning"))
		})
})
