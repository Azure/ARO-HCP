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
	"encoding/json"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/rest"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Customer", func() {
	BeforeEach(func() {
		// do nothing.  per test initialization usually ages better than shared.
	})

	It("should be able to test admin credentials before cluster ready, then full admin credential lifecycle",
		labels.RequireNothing, labels.High, labels.Positive, FlakeAttempts(3),
		func(ctx context.Context) {
			clusterName := "admin-cred-lifecycle-" + rand.String(6)
			tc := framework.NewTestContext()

			By("creating resource group for admin credential lifecycle testing")
			resourceGroup, err := tc.NewResourceGroup(ctx, "admin-credential-lifecycle-test", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("starting cluster-only template deployment asynchronously")
			deploymentsClient := tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient()

			// Prepare the template and parameters
			templateBytes := framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/cluster-only.json"))
			bicepTemplateMap := map[string]interface{}{}
			err = json.Unmarshal(templateBytes, &bicepTemplateMap)
			Expect(err).NotTo(HaveOccurred())

			bicepParameters := map[string]interface{}{
				"clusterName": map[string]interface{}{
					"value": clusterName,
				},
			}

			// Start deployment without waiting
			// Apply 45-minute timeout for the entire cluster deployment process (matches all other tests)
			// This timeout covers both the initial deployment call and the subsequent polling
			timeout := 45 * time.Minute
			deploymentCtx, deploymentCancel := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during admin credential lifecycle test", timeout.Minutes()))
			defer deploymentCancel()

			_, err = deploymentsClient.BeginCreateOrUpdate(
				deploymentCtx,
				*resourceGroup.Name,
				"aro-hcp-cluster-only",
				armresources.Deployment{
					Properties: &armresources.DeploymentProperties{
						Template:   bicepTemplateMap,
						Parameters: bicepParameters,
						Mode:       to.Ptr(armresources.DeploymentModeIncremental),
					},
				},
				nil,
			)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for cluster to appear and testing admin credentials while in deploying state")
			clusterClient := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()

			// Poll the cluster state and test admin credentials when we find it deploying
			var testedWhileDeploying bool
			Eventually(func() error {
				cluster, getErr := clusterClient.Get(ctx, *resourceGroup.Name, clusterName, nil)
				if getErr != nil {
					GinkgoWriter.Printf("Cluster not yet available: %v\n", getErr)
					return getErr // Keep waiting for cluster to appear
				}

				// Cluster exists! Check its provisioning state
				if cluster.Properties.ProvisioningState != nil {
					state := *cluster.Properties.ProvisioningState
					GinkgoWriter.Printf("Cluster found with provisioning state: %v\n", state)

					// If cluster is still deploying and we haven't tested yet, test admin credentials
					if !testedWhileDeploying && (state == hcpsdk20240610preview.ProvisioningStateAccepted || state == hcpsdk20240610preview.ProvisioningStateProvisioning) {
						By("testing admin credentials while cluster is in deploying state")
						_, adminCredErr := clusterClient.BeginRequestAdminCredential(
							ctx,
							*resourceGroup.Name,
							clusterName,
							nil,
						)

						if adminCredErr != nil {
							By("verifying admin credentials request fails with HTTP 409 CONFLICT on deploying cluster")
							GinkgoWriter.Printf("Got expected deployment error: %v\n", adminCredErr)
							testedWhileDeploying = true
						} else {
							Fail("Admin credentials unexpectedly succeeded while deploying - this should return HTTP 409 CONFLICT")
						}

						// Continue polling until cluster is ready, don't return success yet
						return fmt.Errorf("cluster still deploying (state: %v), continuing to wait", state)
					}

					// If cluster is ready, we're done
					if state == hcpsdk20240610preview.ProvisioningStateSucceeded {
						if !testedWhileDeploying {
							Fail("Cluster provisioned too quickly to test 409 behavior - unable to validate admin credentials fail during deployment")
						}
						return nil // Success - cluster is ready
					}

					// If cluster failed, that's an error
					if state == hcpsdk20240610preview.ProvisioningStateFailed {
						return fmt.Errorf("cluster deployment failed")
					}
				}

				// Continue waiting
				return fmt.Errorf("cluster provisioning state unknown, continuing to wait")
			}, 45*time.Minute, 30*time.Second).Should(Succeed(), "Cluster should become ready within 45 minutes")

			By("verifying cluster is now ready for admin credential operations")
			cluster, err := clusterClient.Get(ctx, *resourceGroup.Name, clusterName, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(cluster.HcpOpenShiftCluster.ID).NotTo(BeNil())

			By("getting initial credentials to verify cluster is viable")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster(
				ctx,
				clusterClient,
				*resourceGroup.Name,
				clusterName,
				10*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("ensuring the cluster is viable")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred())

			// Store all admin credentials for later validation
			var credentials []*rest.Config
			credentialCount := 3

			By(fmt.Sprintf("creating %d admin credentials for the ready cluster", credentialCount))
			for i := 0; i < credentialCount; i++ {
				By(fmt.Sprintf("requesting admin credential %d", i+1))
				adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster(
					ctx,
					clusterClient,
					*resourceGroup.Name,
					clusterName,
					10*time.Minute,
				)
				Expect(err).NotTo(HaveOccurred())
				Expect(adminRESTConfig).NotTo(BeNil())
				credentials = append(credentials, adminRESTConfig)
			}

			By("validating all admin credentials work before revocation")
			for i, cred := range credentials {
				By(fmt.Sprintf("verifying admin credential %d works", i+1))
				Expect(verifiers.VerifyHCPCluster(ctx, cred)).To(Succeed())
			}

			By("revoking all cluster admin credentials via ARM API")
			poller, err := clusterClient.BeginRevokeCredentials(ctx, *resourceGroup.Name, clusterName, nil)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for revocation operation to complete")
			_, err = poller.PollUntilDone(ctx, nil)
			Expect(err).NotTo(HaveOccurred())

			By("validating all admin credentials now fail after revocation")
			for i, cred := range credentials {
				By(fmt.Sprintf("verifying admin credential %d now fails", i+1))
				// TODO(bvesel) remove once OCPBUGS-62177 is implemented
				Eventually(verifiers.VerifyHCPCluster(ctx, cred), 10*time.Minute, 15*time.Second).ToNot(Succeed(), "Revoked admin credential %d should no longer work", i+1)
			}

			By("verifying new admin credentials can still be requested after revocation")
			// After revocation, new admin credential requests should still work
			// This validates the revocation endpoint doesn't break the cluster
			newAdminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster(
				ctx,
				clusterClient,
				*resourceGroup.Name,
				clusterName,
				10*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(newAdminRESTConfig).NotTo(BeNil())

			By("verifying new admin credentials work after revocation")
			Expect(verifiers.VerifyHCPCluster(ctx, newAdminRESTConfig)).To(Succeed(), "New admin credentials should work after revocation")
		})
})
