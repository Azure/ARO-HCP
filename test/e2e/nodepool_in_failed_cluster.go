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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/util/rand"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Customer", func() {

	It("should not be able to deploy nodepool into a hosted cluster with failed provisioning state",
		labels.RequireNothing,
		labels.Negative,
		labels.Medium,
		func(ctx context.Context) {
			tc := framework.NewTestContext()

			By("creating resource group ")
			resourceGroup, err := tc.NewResourceGroup(ctx, "rg-nodepool-into-failed-cluster", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			clusterName := "failed-cluster" + rand.String(6)

			By("creating cluster using bicep template with invalid network configuration to force failure")
			_, err = framework.CreateBicepTemplateAndWait(ctx,
				tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				*resourceGroup.Name,
				"invalid-network-config-cluster",
				framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/invalid-network-config-cluster.json")),
				map[string]any{
					"clusterName": clusterName,
				},
				45*time.Minute,
			)

			By("verifying cluster provisioning failed as expected due to invalid network configuration")
			Expect(err).To(HaveOccurred())

			clusterClient := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()

			Eventually(func() bool {
				By("verifying the cluster resource exists even if deployment failed")
				_, err := clusterClient.Get(ctx, *resourceGroup.Name, clusterName, nil)
				return err == nil
			}, 5*time.Minute, 10*time.Second).Should(BeTrue(), "Cluster resource should exist even if deployment failed")

			Eventually(func() hcpsdk20240610preview.ProvisioningState {
				By("verifying the cluster is in a failed provisioning state")
				cluster, err := clusterClient.Get(ctx, *resourceGroup.Name, clusterName, nil)
				if err != nil {
					return ""
				}
				if cluster.Properties != nil && cluster.Properties.ProvisioningState != nil {
					return *cluster.Properties.ProvisioningState
				}
				return ""

			}, 15*time.Minute, 30*time.Second).Should(Equal(hcpsdk20240610preview.ProvisioningStateFailed))

			By("attempting to deploy nodepool via direct API call into cluster with failed provisioning state")
			nodePoolClient := tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient()
			nodePoolName := "nodepool1"
			nodePool := hcpsdk20240610preview.NodePool{
				Location: to.Ptr(tc.Location()),
				Properties: &hcpsdk20240610preview.NodePoolProperties{
					Platform: &hcpsdk20240610preview.NodePoolPlatformProfile{
						VMSize: to.Ptr("Standard_D2s_v3"),
					},
					Replicas: to.Ptr(int32(1)),
				},
			}
			nodePoolCtx, nodePoolCancel := context.WithTimeout(ctx, 5*time.Minute)
			defer nodePoolCancel()

			_, err = nodePoolClient.BeginCreateOrUpdate(nodePoolCtx, *resourceGroup.Name, clusterName, nodePoolName, nodePool, nil)

			By("verifying nodepool failed to deploy")
			Expect(err).To(HaveOccurred())

			By("verifying the error message matches the expected")
			By(fmt.Sprintf("nodepool deployment error: %q", err.Error()))
			Expect(err.Error()).To(ContainSubstring("Node pools can only be created on clusters in 'ready' state, cluster requested is in 'error' state."))

		})

})
