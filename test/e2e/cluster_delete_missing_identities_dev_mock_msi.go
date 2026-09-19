// Copyright 2026 Microsoft Corporation
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
	"errors"
	"fmt"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/util/rand"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

// DEV prototype for ARO-29287. The STG/PROD spec in
// cluster_delete_missing_identities.go stays StageAndProdOnly: deleting
// customer UAMIs is the real signal there, and it is vacuous in DEV because
// every operator authenticates as the job-leased mock SP.
//
// This spec is selected only by suite development/mock-msi-acl (Parallelism 1)
// so it can strip that mock SP without racing sibling specs. See
// docs/ci/mock-msi-acl-isolation.md.
var _ = Describe("Customer", func() {
	It("should be able to delete an HCP cluster after stripping DEV mock-MSI permissions",
		labels.RequireNothing,
		labels.High,
		labels.Positive,
		labels.MockMSIACL,
		labels.MIContainers(0),
		func(ctx context.Context) {
			const customerClusterName = "missing-mi-hcp-cluster"
			tc := framework.NewTestContext()

			By("resolving the leased mock-MSI principal from env (must match the RP Helm miMockPrincipalId this process was provisioned with)")
			_, err := framework.ResolveLeasedMockMSIPrincipalID()
			Expect(err).NotTo(HaveOccurred(), "failed to resolve leased mock-MSI principal; set %s to the Boskos key this RP was provisioned with, or %s to that key's principalId", framework.LeasedMSIMockSPEnvvar, framework.MSIMockPrincipalIDEnvvar)

			By("creating the customer resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "delete-mi-rg", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resource group")

			clusterParams := framework.NewDefaultClusterParams20240610()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("deploying customer infrastructure (NSG, VNet, subnet, KeyVault)")
			customerInfraResult, err := tc.CreateBicepTemplateAndWait(ctx,
				framework.WithTemplateFromFS(TestArtifactsFS, "test-artifacts/generated-test-artifacts/modules/customer-infra.json"),
				framework.WithDeploymentName("customer-infra-"+customerClusterName),
				framework.WithScope(framework.BicepDeploymentScopeResourceGroup),
				framework.WithClusterResourceGroup(*resourceGroup.Name),
				framework.WithParameters(map[string]interface{}{
					"clusterName": customerClusterName,
				}),
				framework.WithTimeout(45*time.Minute),
			)
			Expect(err).NotTo(HaveOccurred(), "failed to deploy customer infrastructure")
			clusterParams, err = framework.PopulateClusterParamsFromCustomerInfraDeployment20240610(clusterParams, customerInfraResult)
			Expect(err).NotTo(HaveOccurred(), "failed to populate cluster params from customer infra deployment")

			// Dedicated identities (useMsiPool=false) so UAMI delete cannot corrupt
			// the shared identity-container pool. ARO-29288 may later drop the UAMI
			// delete; mock-SP strip is what makes the DEV signal non-vacuous.
			By("deploying dedicated managed identities in the customer resource group")
			identities := framework.NewDefaultIdentitiesWithSuffix(customerClusterName)
			managedIdentitiesResult, err := tc.CreateBicepTemplateAndWait(ctx,
				framework.WithTemplateFromFS(TestArtifactsFS, "test-artifacts/generated-test-artifacts/modules/managed-identities.json"),
				framework.WithDeploymentName(fmt.Sprintf("mi-%s-%s", customerClusterName, rand.String(6))),
				framework.WithScope(framework.BicepDeploymentScopeSubscription),
				framework.WithLocation(tc.Location()),
				framework.WithParameters(map[string]interface{}{
					"useMsiPool":               false,
					"clusterResourceGroupName": *resourceGroup.Name,
					"msiResourceGroupName":     *resourceGroup.Name,
					"identities":               identities,
					"rbacScope":                framework.RBACScopeResourceGroup,
					"nsgName":                  clusterParams.NsgName,
					"vnetName":                 clusterParams.VnetName,
					"subnetName":               clusterParams.SubnetName,
					"keyVaultName":             clusterParams.KeyVaultName,
					"clusterName":              customerClusterName,
				}),
				framework.WithTimeout(45*time.Minute),
			)
			Expect(err).NotTo(HaveOccurred(), "failed to deploy dedicated managed identities")
			clusterParams, err = framework.PopulateClusterParamsFromManagedIdentitiesDeployment20240610(clusterParams, managedIdentitiesResult)
			Expect(err).NotTo(HaveOccurred(), "failed to populate cluster params from managed identities deployment")

			By("creating the HCP cluster")
			err = tc.CreateHCPClusterFromParam20240610(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %q", customerClusterName)

			hcpClient := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()

			By("ensuring the cluster is viable before stripping mock-MSI permissions")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20240610(
				ctx,
				hcpClient,
				*resourceGroup.Name,
				customerClusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for cluster %q", customerClusterName)
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to verify HCP cluster %q is viable", customerClusterName)

			By("listing subscription permissions on the leased DEV mock MSI")
			snapshots, err := tc.ListLeasedMockMSIPermissions(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to list leased mock-MSI permissions")
			Expect(len(snapshots)).To(BeNumerically(">=", 2), "expected at least the custom dev-msi-mock role and Key Vault Crypto User assignments")
			DeferCleanup(func(ctx context.Context) {
				Expect(tc.RestoreMockMSIPermissions(ctx, snapshots)).NotTo(HaveOccurred(), "failed to restore leased mock-MSI permissions")
			})

			By("stripping subscription permissions on the leased DEV mock MSI")
			err = tc.DeleteMockMSIRoleAssignments(ctx, snapshots)
			Expect(err).NotTo(HaveOccurred(), "failed to strip leased mock-MSI permissions")

			By("deleting all of the cluster's managed identities behind the scenes")
			err = tc.DeleteUserAssignedIdentities(ctx, *resourceGroup.Name, identities.ToSlice())
			Expect(err).NotTo(HaveOccurred(), "failed to delete managed identities for cluster %q", customerClusterName)

			By("deleting the HCP cluster")
			err = framework.DeleteHCPCluster20240610(
				ctx,
				hcpClient,
				*resourceGroup.Name,
				customerClusterName,
				framework.HCPClusterDeletionTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to delete HCP cluster %q after stripping mock-MSI permissions", customerClusterName)

			By("verifying the cluster resource is deleted (Not Found)")
			_, err = hcpClient.Get(ctx, *resourceGroup.Name, customerClusterName, nil)
			Expect(err).To(HaveOccurred(), "expected an error when getting deleted cluster %q", customerClusterName)
			var respErr *azcore.ResponseError
			Expect(errors.As(err, &respErr)).To(BeTrue(), "expected azcore.ResponseError when getting deleted cluster %q, got %v", customerClusterName, err)
			Expect(respErr.StatusCode).To(Equal(http.StatusNotFound), "expected HTTP 404 when getting deleted cluster %q", customerClusterName)

			By("verifying the managed resource group is deleted (404)")
			rgClient := tc.GetARMResourcesClientFactoryOrDie(ctx).NewResourceGroupsClient()
			lastObserved := ""
			logObservedDelta := func(observed string) {
				if observed == lastObserved {
					return
				}
				lastObserved = observed
				GinkgoLogr.Info("waiting for managed resource group deletion",
					"resourceGroup", managedResourceGroupName, "expected", "HTTP 404", "observed", observed)
			}
			Eventually(func() error {
				_, err := rgClient.Get(ctx, managedResourceGroupName, nil)
				if err == nil {
					logObservedDelta("resource group still exists")
					return fmt.Errorf("managed resource group %q still exists", managedResourceGroupName)
				}
				var respErr *azcore.ResponseError
				if errors.As(err, &respErr) {
					if respErr.StatusCode == http.StatusNotFound {
						return nil
					}
					logObservedDelta(fmt.Sprintf("HTTP %d (%s)", respErr.StatusCode, respErr.ErrorCode))
				} else {
					logObservedDelta(fmt.Sprintf("non-HTTP error: %v", err))
				}
				return fmt.Errorf("unexpected error getting managed resource group %q: %w", managedResourceGroupName, err)
			}, 15*time.Minute, 30*time.Second).Should(Succeed(), "expected managed resource group %q to be deleted", managedResourceGroupName)
		})
})
