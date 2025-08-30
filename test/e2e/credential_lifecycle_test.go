//go:build E2Etests

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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/rest"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Create and Revoke Credentials", func() {
	Context("Comprehensive Credential Lifecycle", func() {
		It("tests credentials before cluster ready, then full credential lifecycle",
			labels.RequireNothing, labels.High, labels.Positive,
			func(ctx context.Context) {
				clusterName := "cred-lifecycle-cluster-" + rand.String(6)
				tc := framework.NewTestContext()

				By("creating resource group for credential lifecycle testing")
				resourceGroup, err := tc.NewResourceGroup(ctx, "credential-lifecycle-test", "uksouth")
				Expect(err).NotTo(HaveOccurred())

				By("starting cluster deployment (but not waiting for completion)")
				deploymentsClient := tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient()

				// Prepare deployment parameters
				bicepParameters := map[string]interface{}{}
				parameters := map[string]interface{}{
					"clusterName": clusterName,
				}
				for k, v := range parameters {
					bicepParameters[k] = map[string]interface{}{
						"value": v,
					}
				}

				// Parse the ARM template
				templateBytes := framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/demo.json"))
				bicepTemplateMap := map[string]interface{}{}
				err = json.Unmarshal(templateBytes, &bicepTemplateMap)
				Expect(err).NotTo(HaveOccurred())

				// Start deployment but don't wait yet
				deploymentProperties := armresources.Deployment{
					Properties: &armresources.DeploymentProperties{
						Template:   bicepTemplateMap,
						Parameters: bicepParameters,
						Mode:       to.Ptr(armresources.DeploymentModeIncremental),
					},
				}

				deploymentPoller, err := deploymentsClient.BeginCreateOrUpdate(
					ctx,
					*resourceGroup.Name,
					"cred-lifecycle-cluster",
					deploymentProperties,
					nil,
				)
				Expect(err).NotTo(HaveOccurred())

				By("CONSULTANT HYPOTHESIS TEST: verifying cluster shows up but is still deploying")
				clusterClient := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()

				// Give deployment a moment to start
				time.Sleep(30 * time.Second)

				cluster, err := clusterClient.Get(ctx, *resourceGroup.Name, clusterName, nil)
				Expect(err).NotTo(HaveOccurred())
				Expect(cluster.HcpOpenShiftCluster.ID).NotTo(BeNil())

				// Should be in a provisioning state, not succeeded
				if cluster.Properties.ProvisioningState != nil {
					Expect(*cluster.Properties.ProvisioningState).NotTo(Equal("Succeeded"),
						"Cluster should still be deploying")
				}

				By("CONSULTANT HYPOTHESIS TEST: attempting credentials on deploying cluster")
				credentialPoller, err := clusterClient.BeginRequestAdminCredential(
					ctx,
					*resourceGroup.Name,
					clusterName,
					nil,
				)

				By("testing credentials on deploying cluster - should fail")
				if err != nil {
					GinkgoWriter.Printf("Got immediate error: %v\n", err)
					Expect(err).To(HaveOccurred())
				} else {
					GinkgoWriter.Printf("No immediate error - checking polling...\n")
					ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
					defer cancel()

					_, pollErr := credentialPoller.PollUntilDone(ctx, nil)
					if pollErr != nil {
						GinkgoWriter.Printf("Got polling error: %v\n", pollErr)
					}
					Expect(pollErr).To(HaveOccurred(), "Should fail on deploying cluster")
				}

				By("now waiting for cluster deployment to complete for credential lifecycle testing")
				ctx, cancel := context.WithTimeout(ctx, 45*time.Minute)
				defer cancel()

				_, err = deploymentPoller.PollUntilDone(ctx, nil)
				Expect(err).NotTo(HaveOccurred())

				By("verifying cluster is now ready for credential operations")
				cluster, err = clusterClient.Get(ctx, *resourceGroup.Name, clusterName, nil)
				Expect(err).NotTo(HaveOccurred())
				Expect(cluster.HcpOpenShiftCluster.ID).NotTo(BeNil())

				// Store all credentials for later validation
				var credentials []*rest.Config
				credentialCount := 3

				By(fmt.Sprintf("creating %d admin credentials for the ready cluster", credentialCount))
				for i := 0; i < credentialCount; i++ {
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
				}

				By("validating all credentials work before revocation")
				for i, cred := range credentials {
					By(fmt.Sprintf("verifying credential %d works", i+1))
					Expect(framework.VerifyHCPCluster(ctx, cred)).To(Succeed())
				}

				By("revoking all cluster credentials via ARM API")
				poller, err := clusterClient.BeginRevokeCredentials(ctx, *resourceGroup.Name, clusterName, nil)
				Expect(err).NotTo(HaveOccurred())

				By("waiting for revocation operation to complete")
				_, err = poller.PollUntilDone(ctx, nil)
				Expect(err).NotTo(HaveOccurred())

				By("validating all credentials now fail after revocation")
				for i, cred := range credentials {
					By(fmt.Sprintf("verifying credential %d now fails", i+1))
					err := framework.VerifyHCPCluster(ctx, cred)
					Expect(err).To(HaveOccurred(), "Revoked credential should fail immediately")
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

				By("verifying new credentials work after revocation")
				Expect(framework.VerifyHCPCluster(ctx, newAdminRESTConfig)).To(Succeed())
			})
	})
})
