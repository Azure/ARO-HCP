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
		func(ctx context.Context) {
			const (
				customerClusterName             = "hcp-cluster-np-32gib"
				customerNodePoolName            = "nodepool-32GiB"
				customerNodeOsDiskSizeGiB int32 = 32 // Min is 1 for 2024-06-10-preview, 64 for newer API versions
				customerNodeReplicas      int32 = 2
			)
			tc := framework.NewTestContext()
			openshiftControlPlaneVersionId := framework.DefaultOpenshiftControlPlaneVersionId()
			openshiftNodeVersionId := framework.DefaultOpenshiftNodePoolVersionId()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "clusternp32gib", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating the infrastructure, cluster and node pool from a single bicep template")

			identities, usePooled, err := tc.ResolveIdentitiesForTemplate(*resourceGroup.Name)
			Expect(err).NotTo(HaveOccurred())

			_, err = tc.CreateBicepTemplateAndWait(ctx,
				framework.WithTemplateFromFS(TestArtifactsFS, "test-artifacts/generated-test-artifacts/cluster-nodepool-osdisk.json"),
				framework.WithDeploymentName("cluster-deployment"),
				framework.WithScope(framework.BicepDeploymentScopeResourceGroup),
				framework.WithClusterResourceGroup(*resourceGroup.Name),
				framework.WithParameters(map[string]interface{}{
					"openshiftControlPlaneVersionId": openshiftControlPlaneVersionId,
					"openshiftNodePoolVersionId":     openshiftNodeVersionId,
					"persistTagValue":                false,
					"clusterName":                    customerClusterName,
					"nodePoolName":                   customerNodePoolName,
					"nodePoolOsDiskSizeGiB":          customerNodeOsDiskSizeGiB,
					"nodeReplicas":                   customerNodeReplicas,
					"identities":                     identities,
					"usePooledIdentities":            usePooled,
				}),
				framework.WithTimeout(45*time.Minute),
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

			By("verifying the node pool is created with 32 GiB osDisk (64 GiB minimum for newer API versions)")
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
