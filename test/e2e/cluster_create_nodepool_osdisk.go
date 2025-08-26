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

	hcpapi20240610 "github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Customer", func() {
	BeforeEach(func() {
		// do nothing.  per test initialization usually ages better than shared.
	})

	It("should be able to create an HCP cluster and custom node pool osDisk size using bicep template",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		func(ctx context.Context) {
			const (
				customerClusterName             = "hcp-cluster-np-128"
				customerNodePoolName            = "nodepool-128GiB"
				customerNodeOsDiskSizeGiB int32 = 128
				customerNodeReplicas      int32 = 2
			)
			tc := framework.NewTestContext()

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "clusternp128", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating the infrastructure, cluster and node pool from a single bicep template")
			_, err = framework.CreateBicepTemplateAndWait(ctx,
				tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				*resourceGroup.Name,
				"cluster-deployment",
				framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/cluster-nodepool-osdisk.json")),
				map[string]interface{}{
					"persistTagValue":       false,
					"clusterName":           customerClusterName,
					"nodePoolName":          customerNodePoolName,
					"nodePoolOsDiskSizeGiB": customerNodeOsDiskSizeGiB,
					"nodeReplicas":          customerNodeReplicas,
				},
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("getting credentials")
			adminRESTConfig, err := framework.GetAdminRESTConfigForHCPCluster(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				10*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("ensuring the cluster is viable")
			err = framework.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred())

			/* 			By("waiting for the node pool to be ready")
			   			provisioningState, err := framework.WaitForNodePoolReady(ctx,
			   				tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient(),
			   				*resourceGroup.Name,
			   				customerClusterName,
			   				customerNodePoolName,
			   				10*time.Minute,
			   			)
			   			Expect(err).NotTo(HaveOccurred())
			   			Expect(provisioningState).To(Equal(hcpapi20240610.ProvisioningStateSucceeded))


			   			By("verifying nodepool configuration")
			   			err = framework.VerifyNodePool(ctx,
			   				adminRESTConfig,
			   				customerClusterName,
			   				customerNodePoolName,
			   				framework.VerifyNodePoolReplicas(customerNodeReplicas),
			   				framework.VerifyNodePoolOsDiskSize(customerNodeOsDiskSizeGiB),
			   			)
			   			Expect(err).NotTo(HaveOccurred()) */
			// Verify provisioning succeeded and VM size matches what we requested
			created, err := framework.GetNodePool(ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient(),
				*resourceGroup.Name,
				customerClusterName,
				customerNodePoolName,
				5*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(created.Properties).ToNot(BeNil())
			Expect(created.Properties.ProvisioningState).ToNot(BeNil())
			Expect(*created.Properties.ProvisioningState).To(Equal(hcpapi20240610.ProvisioningStateSucceeded))
			Expect(created.Properties.Platform).ToNot(BeNil())
			Expect(created.Properties.Platform.VMSize).ToNot(BeNil())
		})
})
