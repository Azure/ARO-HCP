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

	hcpsdk "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Customer", func() {
	It("should be able to create a HCP cluster without CNI",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		func(ctx context.Context) {
			const (
				customerClusterName  = "no-cni-cl"
				customerNodePoolName = "no-cni-np"
			)
			tc := framework.NewTestContext()

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "e2e-no-cni", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("deploying no-cni bicep file to create no-cni cluster without a node pool")
			_, err = framework.CreateBicepTemplateAndWait(ctx,
				tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				*resourceGroup.Name,
				"aro-hcp-no-cni",
				framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/no-cni.json")),
				map[string]interface{}{
					"clusterName": customerClusterName,
				},
				30*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("getting credentials and verifying cluster is available")
			adminRESTConfig, err := framework.GetAdminRESTConfigForHCPCluster(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				10*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(verifiers.VerifyHCPCluster(ctx, adminRESTConfig)).To(Succeed())

			By("deploying bicep file to create a node pool")
			_, err = framework.CreateBicepTemplateAndWait(ctx,
				tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				*resourceGroup.Name,
				"aro-hcp-no-cni-np",
				framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/modules/nodepool.json")),
				map[string]interface{}{
					"clusterName":  customerClusterName,
					"nodePoolName": customerNodePoolName,
				},
				15*time.Minute,
			)
			// ARO-20829 workaround: instead of a finished and succesfull
			// deployment, we expect that the provisioning is still going on
			Expect(err).To(HaveOccurred())
			By("expecting the node pool to be still deploying because of ARO-20829")
			nodePool, err := framework.GetNodePool(ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient(),
				*resourceGroup.Name,
				customerClusterName,
				customerNodePoolName,
				5*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(nodePool.Properties).ToNot(BeNil())
			Expect(nodePool.Properties.ProvisioningState).ToNot(BeNil())
			Expect(*nodePool.Properties.ProvisioningState).To(Equal(hcpsdk.ProvisioningStateProvisioning))

			By("expecting that on a cluster without CNI plugin, nodes are in NotReady state")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig, verifiers.VerifyNodesReady())
			Expect(err).To(HaveOccurred())

			// TODO: add cilium setup here, and then rerun VerifyHCPCluster
			// with VerifyNodesReady() expecting it to pass
		},
	)
})
