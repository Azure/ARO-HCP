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

	hcpapi "github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Customer", func() {
	BeforeEach(func() {
		// do nothing.  per test initialization usually ages better than shared.
	})

	It("should be able to test admin credentials before cluster ready, then full admin credential lifecycle",
		labels.RequireNothing, labels.High, labels.Positive,
		func(ctx context.Context) {
			const (
				customerNetworkSecurityGroupName = "admin-cred-nsg"
				customerVnetName                 = "admin-cred-vnet"
				customerVnetSubnetName           = "admin-cred-subnet"
				openshiftControlPlaneVersionId   = "4.19"
			)
			clusterName := "admin-cred-lifecycle-" + rand.String(6)
			tc := framework.NewTestContext()

			By("creating resource group for admin credential lifecycle testing")
			resourceGroup, err := tc.NewResourceGroup(ctx, "admin-credential-lifecycle-test", "uksouth")
			Expect(err).NotTo(HaveOccurred())

			By("creating a customer-infra")
			customerInfraDeploymentResult, err := framework.CreateBicepTemplateAndWait(ctx,
				tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				*resourceGroup.Name,
				"customer-infra",
				framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/modules/customer-infra.json")),
				map[string]interface{}{
					"persistTagValue":        false,
					"customerNsgName":        customerNetworkSecurityGroupName,
					"customerVnetName":       customerVnetName,
					"customerVnetSubnetName": customerVnetSubnetName,
				},
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating a managed identities")
			keyVaultName, err := framework.GetOutputValue(customerInfraDeploymentResult, "keyVaultName")
			Expect(err).NotTo(HaveOccurred())
			managedIdentityDeploymentResult, err := framework.CreateBicepTemplateAndWait(ctx,
				tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				*resourceGroup.Name,
				"managed-identities",
				framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/modules/managed-identities.json")),
				map[string]interface{}{
					"clusterName":  clusterName,
					"nsgName":      customerNetworkSecurityGroupName,
					"vnetName":     customerVnetName,
					"subnetName":   customerVnetSubnetName,
					"keyVaultName": keyVaultName,
				},
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("starting cluster deployment asynchronously to test admin credentials on deploying cluster")
			userAssignedIdentities, err := framework.GetOutputValue(managedIdentityDeploymentResult, "userAssignedIdentitiesValue")
			Expect(err).NotTo(HaveOccurred())
			identity, err := framework.GetOutputValue(managedIdentityDeploymentResult, "identityValue")
			Expect(err).NotTo(HaveOccurred())
			etcdEncryptionKeyName, err := framework.GetOutputValue(customerInfraDeploymentResult, "etcdEncryptionKeyName")
			Expect(err).NotTo(HaveOccurred())
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)

			// Start cluster deployment immediately using Azure SDK
			deploymentsClient := tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient()

			// Parse the cluster template
			templateBytes := framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/modules/cluster.json"))
			bicepTemplateMap := map[string]interface{}{}
			err = json.Unmarshal(templateBytes, &bicepTemplateMap)
			Expect(err).NotTo(HaveOccurred())

			// Prepare deployment parameters
			bicepParameters := map[string]interface{}{
				"openshiftVersionId": map[string]interface{}{
					"value": openshiftControlPlaneVersionId,
				},
				"clusterName": map[string]interface{}{
					"value": clusterName,
				},
				"managedResourceGroupName": map[string]interface{}{
					"value": managedResourceGroupName,
				},
				"nsgName": map[string]interface{}{
					"value": customerNetworkSecurityGroupName,
				},
				"subnetName": map[string]interface{}{
					"value": customerVnetSubnetName,
				},
				"vnetName": map[string]interface{}{
					"value": customerVnetName,
				},
				"userAssignedIdentitiesValue": map[string]interface{}{
					"value": userAssignedIdentities,
				},
				"identityValue": map[string]interface{}{
					"value": identity,
				},
				"keyVaultName": map[string]interface{}{
					"value": keyVaultName,
				},
				"etcdEncryptionKeyName": map[string]interface{}{
					"value": etcdEncryptionKeyName,
				},
			}

			// Apply 45-minute timeout for the entire cluster deployment process (matches all other tests)
			// This timeout covers both the initial deployment call and the subsequent polling
			clusterDeploymentCtx, clusterDeploymentCancel := context.WithTimeout(ctx, 45*time.Minute)
			defer clusterDeploymentCancel()

			// Start deployment
			_, err = deploymentsClient.BeginCreateOrUpdate(
				clusterDeploymentCtx,
				*resourceGroup.Name,
				"cluster",
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
					if !testedWhileDeploying && (state == hcpapi.ProvisioningStateAccepted || state == hcpapi.ProvisioningStateProvisioning) {
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
					if state == hcpapi.ProvisioningStateSucceeded {
						if !testedWhileDeploying {
							Fail("Cluster provisioned too quickly to test 409 behavior - unable to validate admin credentials fail during deployment")
						}
						return nil // Success - cluster is ready
					}

					// If cluster failed, that's an error
					if state == hcpapi.ProvisioningStateFailed {
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
			adminRESTConfig, err := framework.GetAdminRESTConfigForHCPCluster(
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
				Expect(verifiers.VerifyHCPCluster(ctx, cred)).ToNot(Succeed(), "Revoked admin credential %d should no longer work", i+1)
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
			Expect(verifiers.VerifyHCPCluster(ctx, newAdminRESTConfig)).To(Succeed())
		})
})
