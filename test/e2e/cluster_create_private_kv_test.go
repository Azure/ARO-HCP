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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hcpsdk20251223preview "github.com/Azure/ARO-HCP/test/sdk/v20251223preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Create HCPOpenShiftCluster with Private KeyVault", func() {
	// Set deadline to a reasonable date after which we expect the private keyvault
	// feature to be fully ready. Adjust as needed based on rollout schedule.
	timeBombDeadline := mustParseDate("2026-05-01")

	BeforeEach(func() {
		// do nothing. per test initialization usually ages better than shared.
	})

	It("should create a cluster with private keyvault using v20251223preview API",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		labels.CreateCluster,
		func(ctx context.Context) {
			const customerClusterName = "private-kv-cluster"

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "private-keyvault", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName
			clusterParams.KeyVaultVisibility = "Private"

			By("creating customer resources (infrastructure and managed identities)")
			clusterParams, err = tc.CreateClusterCustomerResources(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{
					"privateKeyVault": true,
				},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating the HCP cluster")
			clusterResource, err := framework.BuildHCPCluster20251223FromParams(clusterParams, tc.Location(), nil)
			Expect(err).NotTo(HaveOccurred())

			// Set KeyVault visibility
			if clusterResource.Properties != nil && clusterResource.Properties.Etcd != nil &&
				clusterResource.Properties.Etcd.DataEncryption != nil &&
				clusterResource.Properties.Etcd.DataEncryption.CustomerManaged != nil &&
				clusterResource.Properties.Etcd.DataEncryption.CustomerManaged.Kms != nil {
				clusterResource.Properties.Etcd.DataEncryption.CustomerManaged.Kms.Visibility = to.Ptr(hcpsdk20251223preview.KeyVaultVisibilityPrivate)
			}

			_, err = framework.CreateHCPCluster20251223AndWait(
				ctx,
				GinkgoLogr,
				tc.Get20251223ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				clusterResource,
				45*time.Minute,
			)
			if isAPINotDeployedError(err) {
				if time.Now().Before(timeBombDeadline) {
					Skip(fmt.Sprintf("v20251223preview API not yet deployed; skipping until %s", timeBombDeadline.Format(time.RFC3339)))
				}
				Fail(fmt.Sprintf("v20251223preview API still not deployed as of %s deadline", timeBombDeadline.Format(time.RFC3339)))
			}
			Expect(err).NotTo(HaveOccurred())

			By("verifying cluster was created with private keyvault visibility")
			clientFactory := tc.Get20251223ClientFactoryOrDie(ctx)
			cluster, err := clientFactory.NewHcpOpenShiftClustersClient().Get(
				ctx,
				*resourceGroup.Name,
				customerClusterName,
				nil,
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(cluster.Properties).ToNot(BeNil())
			Expect(cluster.Properties.Etcd).ToNot(BeNil())
			Expect(cluster.Properties.Etcd.DataEncryption).ToNot(BeNil())
			Expect(cluster.Properties.Etcd.DataEncryption.CustomerManaged).ToNot(BeNil())
			Expect(cluster.Properties.Etcd.DataEncryption.CustomerManaged.Kms).ToNot(BeNil())

			visibilityNotPresent := cluster.Properties.Etcd.DataEncryption.CustomerManaged.Kms.Visibility == nil
			if visibilityNotPresent {
				if time.Now().Before(timeBombDeadline) {
					Skip("v20251223preview deployed but Visibility field not present in cluster response; skipping until rollout completes")
				}
				Fail(fmt.Sprintf("Visibility field still not present in v20251223preview cluster response as of %s deadline", timeBombDeadline.Format(time.RFC3339)))
			}
			Expect(*cluster.Properties.Etcd.DataEncryption.CustomerManaged.Kms.Visibility).To(Equal(hcpsdk20251223preview.KeyVaultVisibilityPrivate))

			GinkgoLogr.Info("Cluster created successfully with private keyvault",
				"clusterName", customerClusterName,
				"keyVaultName", clusterParams.KeyVaultName,
				"keyVaultVisibility", *cluster.Properties.Etcd.DataEncryption.CustomerManaged.Kms.Visibility)

			By("deleting cluster")
			deletePoller, err := clientFactory.NewHcpOpenShiftClustersClient().BeginDelete(
				ctx,
				*resourceGroup.Name,
				customerClusterName,
				nil,
			)
			Expect(err).ToNot(HaveOccurred())

			By("waiting for cluster deletion to complete")
			_, err = deletePoller.PollUntilDone(ctx, nil)
			Expect(err).ToNot(HaveOccurred())
		})
})
