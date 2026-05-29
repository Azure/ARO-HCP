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
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/util/rand"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	hcpsdk20251223preview "github.com/Azure/ARO-HCP/test/sdk/v20251223preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("ARO HCP Service", func() {
	It("should successfully provision cluster when MI role assignments are added after cluster creation",
		labels.RequireNothing,
		labels.High,
		labels.Positive,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			const (
				customerNsgName        = "customer-nsg-name"
				customerVnetName       = "customer-vnet-name"
				customerVnetSubnetName = "customer-vnet-subnet1"
				customerClusterName    = "delayed-rbac-cluster"

				clusterCreationTimeout   = 45 * time.Minute
				consistentlyLoopDuration = 3 * time.Minute
			)
			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred(), "failed to assign identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "delayed-rbac", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group")

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams20251223()
			clusterParams.ClusterName = customerClusterName
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)

			By("deploying customer infrastructure (NSG, VNet, subnet, KeyVault)")
			suffix := rand.String(6)
			customerInfraResult, err := tc.CreateBicepTemplateAndWait(ctx,
				framework.WithTemplateFromFS(TestArtifactsFS, "test-artifacts/generated-test-artifacts/modules/customer-infra.json"),
				framework.WithDeploymentName("customer-infra-"+customerClusterName),
				framework.WithScope(framework.BicepDeploymentScopeResourceGroup),
				framework.WithClusterResourceGroup(*resourceGroup.Name),
				framework.WithParameters(map[string]any{
					"customerNsgName":        customerNsgName,
					"customerVnetName":       customerVnetName,
					"customerVnetSubnetName": customerVnetSubnetName,
					"clusterName":            customerClusterName,
				}),
				framework.WithTimeout(10*time.Minute),
			)
			Expect(err).NotTo(HaveOccurred(), "failed to deploy customer infrastructure")

			clusterParams, err = framework.PopulateClusterParamsFromCustomerInfraDeployment20251223(clusterParams, customerInfraResult)
			Expect(err).NotTo(HaveOccurred(), "failed to populate cluster params from customer infra deployment")

			// Determine MI resource group and identity names. In pooled mode,
			// the pool already created MIs without role assignments. In
			// non-pooled mode, we create MIs explicitly via cluster-identities.
			// Either way, MIs exist but lack role assignments — CS's MI
			// permission validation will fail and retry (ARO-25805).
			By("ensuring managed identities exist WITHOUT role assignments")
			subscriptionID, err := tc.SubscriptionID(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to get subscription ID")

			var msiResourceGroupName string
			var identities framework.Identities
			var leasedPool *framework.LeasedIdentityPool

			if tc.UsePooledIdentities() {
				pool, err := tc.NextLeasedIdentityPool()
				Expect(err).NotTo(HaveOccurred(), "failed to lease identity pool")
				leasedPool = &pool
				msiResourceGroupName = pool.ResourceGroupName
				identities = pool.Identities
			} else {
				msiResourceGroupName = *resourceGroup.Name
				identities = framework.NewDefaultIdentitiesWithSuffix(customerClusterName)
				_, err = tc.CreateBicepTemplateAndWait(ctx,
					framework.WithTemplateFromFS(TestArtifactsFS, "test-artifacts/generated-test-artifacts/modules/cluster-identities.json"),
					framework.WithDeploymentName("mi-identities-only-"+customerClusterName),
					framework.WithScope(framework.BicepDeploymentScopeResourceGroup),
					framework.WithClusterResourceGroup(*resourceGroup.Name),
					framework.WithParameters(map[string]any{
						"identities": identities,
					}),
					framework.WithTimeout(10*time.Minute),
				)
				Expect(err).NotTo(HaveOccurred(), "failed to deploy identity-only bicep template")
			}

			uamis, msi := framework.BuildIdentityParamsFromNames(subscriptionID, msiResourceGroupName, identities)

			By("starting HCP cluster creation (MIs exist but lack role assignments)")
			hcpClient := tc.Get20251223ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
			cluster, err := framework.BuildHCPClusterFromParams20251223(clusterParams, tc.Location(), nil)
			Expect(err).NotTo(HaveOccurred(), "failed to build HCP cluster spec")
			cluster.Identity = msi
			cluster.Properties.Platform.OperatorsAuthentication.UserAssignedIdentities = uamis
			_, err = hcpClient.BeginCreateOrUpdate(ctx, *resourceGroup.Name, customerClusterName, cluster, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to start cluster creation")

			By("waiting for cluster resource to become visible")
			Eventually(func(g Gomega) {
				_, err := hcpClient.Get(ctx, *resourceGroup.Name, customerClusterName, nil)
				g.Expect(err).NotTo(HaveOccurred(), "GET cluster failed — RP may not have registered the resource yet")
			}, 2*time.Minute, 10*time.Second).Should(Succeed(),
				"timed out waiting for cluster resource to become visible after BeginCreateOrUpdate")

			By("verifying cluster does not enter terminal Failed state while role assignments are missing")
			Consistently(func(g Gomega) {
				resp, err := hcpClient.Get(ctx, *resourceGroup.Name, customerClusterName, nil)
				if err != nil {
					GinkgoLogr.Info("GET cluster returned error, skipping poll iteration", "error", err)
					return
				}
				g.Expect(resp.Properties).NotTo(BeNil(), "cluster response has nil Properties")
				g.Expect(resp.Properties.ProvisioningState).NotTo(BeNil(), "cluster response has nil ProvisioningState")
				state := *resp.Properties.ProvisioningState
				GinkgoLogr.Info("cluster provisioning state", "state", state)
				g.Expect(state).NotTo(Equal(hcpsdk20251223preview.ProvisioningStateFailed),
					"cluster entered terminal Failed state — CS inflight validation should retry, not fail terminally (ARO-25805)")
			}, consistentlyLoopDuration, 30*time.Second).Should(Succeed())

			By("deploying role assignments for managed identities")
			deployMIOpts := []framework.BicepDeploymentOption{
				framework.WithTemplateFromFS(TestArtifactsFS, "test-artifacts/generated-test-artifacts/modules/managed-identities.json"),
				framework.WithDeploymentName("mi-assignments-" + customerClusterName + "-" + suffix),
				framework.WithClusterResourceGroup(*resourceGroup.Name),
				framework.WithTimeout(10 * time.Minute),
				framework.WithParameters(map[string]any{
					"nsgName":      clusterParams.NsgName,
					"vnetName":     clusterParams.VnetName,
					"subnetName":   clusterParams.SubnetName,
					"keyVaultName": clusterParams.KeyVaultName,
				}),
			}
			if leasedPool != nil {
				deployMIOpts = append(deployMIOpts, framework.WithIdentityPool(leasedPool))
			}
			_, err = tc.DeployManagedIdentities(ctx,
				customerClusterName,
				framework.RBACScopeResourceGroup,
				deployMIOpts...,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to deploy role assignments for managed identities")

			By("waiting for cluster to reach Succeeded state")
			Eventually(func(g Gomega) {
				resp, err := hcpClient.Get(ctx, *resourceGroup.Name, customerClusterName, nil)
				if err != nil {
					var respErr *azcore.ResponseError
					if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
						g.Expect(err).NotTo(HaveOccurred(), "cluster returned 404 — resource disappeared after role assignment deployment")
						return
					}
					GinkgoLogr.Info("GET cluster returned error, retrying", "error", err)
					g.Expect(err).NotTo(HaveOccurred(), "GET cluster failed — RP returned an unexpected error")
				}
				g.Expect(resp.Properties).NotTo(BeNil(), "cluster response has nil Properties")
				g.Expect(resp.Properties.ProvisioningState).NotTo(BeNil(), "cluster response has nil ProvisioningState")
				state := *resp.Properties.ProvisioningState
				GinkgoLogr.Info("cluster provisioning state", "state", state)
				g.Expect(state).NotTo(Equal(hcpsdk20251223preview.ProvisioningStateFailed),
					"cluster entered terminal Failed state after role assignment deployment")
				g.Expect(state).To(Equal(hcpsdk20251223preview.ProvisioningStateSucceeded),
					"cluster has not yet reached Succeeded state")
			}, clusterCreationTimeout-consistentlyLoopDuration, 30*time.Second).Should(Succeed(),
				"cluster should eventually succeed after role assignments are created")

			By("verifying cluster is viable")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20240610(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				framework.AdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for HCP cluster")

			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to verify HCP cluster viability")
		})
})
