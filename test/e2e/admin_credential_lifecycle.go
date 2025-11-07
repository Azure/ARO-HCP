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
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/openshift-eng/openshift-tests-extension/pkg/util/sets"

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

	terminalProvisioningStates := sets.New[hcpsdk20240610preview.ProvisioningState](hcpsdk20240610preview.ProvisioningStateSucceeded, hcpsdk20240610preview.ProvisioningStateFailed, hcpsdk20240610preview.ProvisioningStateCanceled)

	It("should be able to test admin credentials before cluster ready, then full admin credential lifecycle",
		labels.RequireNothing, labels.High, labels.Positive,
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
			deploymentCtx, deploymentCancel := context.WithTimeout(ctx, 45*time.Minute)
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
			var previousState hcpsdk20240610preview.ProvisioningState
			GinkgoLogr.Info("creating cluster, waiting for it to reach a terminal state")
			Eventually(func() bool {
				cluster, err := clusterClient.Get(ctx, *resourceGroup.Name, clusterName, nil)
				if err != nil {
					var respErr *azcore.ResponseError
					if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
						GinkgoLogr.Info("Cluster not found yet, continuing to wait...")
						return false
					}
					Fail("Cluster GET returned error: " + err.Error())
				}

				// only log state changes
				if previousState != *cluster.Properties.ProvisioningState {
					GinkgoLogr.Info(fmt.Sprintf("Cluster provisioning state changed: '%v' -> '%v'", previousState, *cluster.Properties.ProvisioningState))
					previousState = *cluster.Properties.ProvisioningState
				}

				// If cluster is still deploying and we haven't tested yet, test admin credentials
				if !testedWhileDeploying && !terminalProvisioningStates.Has(*cluster.Properties.ProvisioningState) {
					By("testing admin credentials while cluster is in deploying state")
					testedWhileDeploying = true
					_, err := clusterClient.BeginRequestAdminCredential(
						ctx,
						*resourceGroup.Name,
						clusterName,
						nil,
					)
					var respErr *azcore.ResponseError
					if err != nil && errors.As(err, &respErr) && http.StatusConflict == respErr.StatusCode {
						By("verifying admin credentials request fails with HTTP 409 CONFLICT on deploying cluster")
						GinkgoLogr.Info("Admin credentials request correctly returned 409 conflict error while cluster is deploying")
					} else {
						Fail("Admin credentials did not return 409 conflict error while cluster is deploying")
					}
				}

				// If cluster is ready, we're done
				if *cluster.Properties.ProvisioningState == hcpsdk20240610preview.ProvisioningStateSucceeded {
					if !testedWhileDeploying {
						Fail("Cluster provisioned too quickly to test 409 behavior - unable to validate admin credentials fail during deployment")
					}
					return true // Success - cluster is ready
				}

				// If cluster failed, that's an error
				if *cluster.Properties.ProvisioningState == hcpsdk20240610preview.ProvisioningStateFailed {
					Fail("Cluster provisioning failed")
				}

				// Continue waiting
				return false
			}, 45*time.Minute, 30*time.Second).Should(BeTrue(), "Cluster should become ready within 45 minutes")

			// Store all admin credentials for later validation
			var credentials []*rest.Config
			credentialCount := 3

			By(fmt.Sprintf("creating %d admin credentials for the ready cluster", credentialCount))
			for i := range credentialCount {
				By(fmt.Sprintf("requesting admin credential %d", i+1))
				adminRESTConfig, err := framework.GetAdminRESTConfigForHCPCluster(
					ctx,
					clusterClient,
					*resourceGroup.Name,
					clusterName,
					10*time.Minute,
				)
				Expect(err).NotTo(HaveOccurred())
				Expect(adminRESTConfig).NotTo(BeNil())
				credentials = append(credentials, adminRESTConfig)

				By(fmt.Sprintf("validating admin credential %d works", i+1))
				Expect(verifiers.VerifyHCPCluster(ctx, adminRESTConfig)).To(Succeed())
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
			newAdminRESTConfig, err := framework.GetAdminRESTConfigForHCPCluster(
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
