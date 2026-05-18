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

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Create HCPOpenShiftCluster with Private KeyVault", func() {
	// Set deadline to a reasonable date after which we expect the private keyvault
	// feature to be fully ready. Adjust as needed based on rollout schedule.
	timeBombDeadline := mustParseDate("2026-04-01")

	BeforeEach(func() {
		// do nothing. per test initialization usually ages better than shared.
	})

	It("should create a cluster with private keyvault using v20251223preview API",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.CreateCluster,
		func(ctx context.Context) {
			const customerClusterName = "private-kv-cluster"

			tc := framework.NewTestContext()
			tc.SetHCPAPIVersionForTest(framework.HCPAPIVersion20251223)

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
			err = tc.CreateHCPClusterFromParam(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
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
			visibility, err := tc.GetClusterKeyVaultVisibility(ctx, *resourceGroup.Name, customerClusterName)
			Expect(err).ToNot(HaveOccurred())

			visibilityNotPresent := visibility == nil
			if visibilityNotPresent {
				if time.Now().Before(timeBombDeadline) {
					Skip("v20251223preview deployed but Visibility field not present in cluster response; skipping until rollout completes")
				}
				Fail(fmt.Sprintf("Visibility field still not present in v20251223preview cluster response as of %s deadline", timeBombDeadline.Format(time.RFC3339)))
			}
			Expect(*visibility).To(Equal("Private"))

			GinkgoLogr.Info("Cluster created successfully with private keyvault",
				"clusterName", customerClusterName,
				"keyVaultName", clusterParams.KeyVaultName,
				"keyVaultVisibility", *visibility)

			By("creating the node pool")
			nodePoolParams := framework.NewDefaultNodePoolParams()
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = "np-1"
			nodePoolParams.Replicas = int32(2)

			err = tc.CreateNodePoolFromParam(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				managedResourceGroupName,
				customerClusterName,
				nodePoolParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			GinkgoLogr.Info("Nodepool created successfully for private keyvault cluster",
				"clusterName", customerClusterName,
				"nodePoolName", nodePoolParams.NodePoolName)

			By("getting admin credentials for the cluster")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster(
				ctx,
				*resourceGroup.Name,
				customerClusterName,
				10*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the cluster is viable and pod logs can be fetched")
			logVerifier := verifiers.VerifyGetDeploymentLogs("openshift-ingress", "router-default", "router")
			var previousError string
			Eventually(func() error {
				err := logVerifier.Verify(ctx, adminRESTConfig)
				if err != nil {
					currentError := err.Error()
					if currentError != previousError {
						GinkgoLogr.Info("Verifier check", "name", logVerifier.Name(), "status", "failed", "error", currentError)
						previousError = currentError
					}
				}
				return err
			}, 10*time.Minute, 30*time.Second).Should(Succeed(), "router-default deployment logs should be fetchable")

		})
})
