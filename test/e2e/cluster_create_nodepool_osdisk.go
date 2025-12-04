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
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Customer", func() {
	BeforeEach(func() {
		// do nothing.  per test initialization usually ages better than shared.
	})

	It("should be able to create an HCP cluster and custom node pool osDisk size using bicep template",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		FlakeAttempts(3),
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
			_, err = tc.CreateBicepTemplateAndWait(ctx,
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
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				10*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("ensuring the cluster is viable")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the node pool is created and has the correct osDisk size")
			created, err := framework.GetNodePool(ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient(),
				*resourceGroup.Name,
				customerClusterName,
				customerNodePoolName,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(created.Properties).ToNot(BeNil())
			Expect(created.Properties.ProvisioningState).ToNot(BeNil())
			Expect(*created.Properties.ProvisioningState).To(Equal(hcpsdk20240610preview.ProvisioningStateSucceeded))
			Expect(created.Properties.Platform).ToNot(BeNil())
			Expect(created.Properties.Platform.OSDisk).ToNot(BeNil())
			Expect(*created.Properties.Platform.OSDisk.SizeGiB).To(Equal(customerNodeOsDiskSizeGiB))
		})
})
