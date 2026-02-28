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

	"k8s.io/apimachinery/pkg/util/rand"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Cluster Creation with Reused Managed Identities", func() {
	defer GinkgoRecover()

	var (
		customerEnv = &e2eSetup.CustomerEnv
		clusterEnv  = &e2eSetup.Cluster
	)

	It("Should fail when attempting to create a cluster with already-used managed identities",
		labels.RequireHappyPathInfra,
		labels.High,
		labels.Negative,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			tc := framework.NewTestContext()

			By("Verifying that the first cluster exists and is in Succeeded state")
			hcpClient := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
			existingCluster, err := hcpClient.Get(ctx, customerEnv.CustomerRGName, clusterEnv.Name, nil)
			Expect(err).NotTo(HaveOccurred(), "Failed to get existing cluster")
			Expect(existingCluster.Properties).NotTo(BeNil())
			Expect(existingCluster.Properties.ProvisioningState).NotTo(BeNil())
			GinkgoWriter.Printf("Existing cluster '%s' is in state: %s\n", clusterEnv.Name, *existingCluster.Properties.ProvisioningState)

			By("Extracting managed identities from the existing cluster")
			Expect(existingCluster.Identity).NotTo(BeNil(), "Existing cluster should have identity configuration")
			Expect(existingCluster.Identity.UserAssignedIdentities).NotTo(BeEmpty(), "Existing cluster should have user-assigned identities")
			Expect(existingCluster.Properties.Platform).NotTo(BeNil())
			Expect(existingCluster.Properties.Platform.OperatorsAuthentication).NotTo(BeNil())
			Expect(existingCluster.Properties.Platform.OperatorsAuthentication.UserAssignedIdentities).NotTo(BeNil())

			// Extract the identities that are already in use
			existingUserAssignedIdentities := customerEnv.UAMIs
			existingIdentity := customerEnv.IdentityUAMIs

			GinkgoWriter.Printf("Existing cluster uses %d control plane operator identities\n",
				len(existingUserAssignedIdentities.ControlPlaneOperators))
			GinkgoWriter.Printf("Existing cluster uses %d data plane operator identities\n",
				len(existingUserAssignedIdentities.DataPlaneOperators))
			if existingUserAssignedIdentities.ServiceManagedIdentity != nil {
				GinkgoWriter.Printf("Existing cluster uses service managed identity: %s\n",
					*existingUserAssignedIdentities.ServiceManagedIdentity)
			}

			By("Creating a new resource group for the second cluster")
			secondClusterName := "cluster-reuse-ids-" + rand.String(6)
			resourceGroup, err := tc.NewResourceGroup(ctx, "reuse-identities", tc.Location())
			Expect(err).NotTo(HaveOccurred())
			GinkgoWriter.Printf("Created resource group: %s\n", *resourceGroup.Name)

			By("Creating cluster parameters for the second cluster")
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = secondClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("Creating customer infrastructure (VNet, NSG, KeyVault) for the second cluster")
			// Use unique names to avoid confusion with the first cluster's infrastructure
			randomSuffix := rand.String(6)
			clusterParams, err = tc.CreateClusterCustomerResources(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{
					"persistTagValue":        false,
					"customerNsgName":        "reuse-nsg-" + randomSuffix,
					"customerVnetName":       "reuse-vnet-" + randomSuffix,
					"customerVnetSubnetName": "reuse-subnet",
				},
				TestArtifactsFS,
			)
			Expect(err).NotTo(HaveOccurred())

			By("Overriding cluster parameters to use the SAME managed identities as the first cluster")
			// Replace the newly created identities with the existing cluster's identities
			clusterParams.UserAssignedIdentitiesProfile = &existingUserAssignedIdentities

			// For the Identity field, we need to convert the existing identity to the correct format
			// Azure expects empty objects {} for UserAssignedIdentities in CREATE requests
			emptyUserAssignedIdentities := make(map[string]*hcpsdk20240610preview.UserAssignedIdentity)
			for identityID := range existingIdentity.UserAssignedIdentities {
				emptyUserAssignedIdentities[identityID] = &hcpsdk20240610preview.UserAssignedIdentity{}
			}
			clusterParams.Identity = &hcpsdk20240610preview.ManagedServiceIdentity{
				Type:                   existingIdentity.Type,
				UserAssignedIdentities: emptyUserAssignedIdentities,
			}

			GinkgoWriter.Printf("Attempting to create cluster '%s' with reused identities...\n", secondClusterName)
			GinkgoWriter.Printf("  Reusing %d control plane identities\n", len(existingUserAssignedIdentities.ControlPlaneOperators))
			GinkgoWriter.Printf("  Reusing %d data plane identities\n", len(existingUserAssignedIdentities.DataPlaneOperators))

			By("Attempting to create the second cluster using direct API call")
			// This should fail because the identities are already in use by the first cluster
			err = tc.CreateHCPClusterFromParam(ctx,
				*resourceGroup.Name,
				clusterParams,
				45*time.Minute,
			)

			if err != nil {
				// Good - cluster creation failed as expected
				By("Cluster creation failed as expected")
				GinkgoWriter.Printf("✓ Cluster creation failed with error: %s\n", err.Error())
				Expect(err.Error()).To(Or(
					ContainSubstring("already in use"),
					ContainSubstring("already used"),
					ContainSubstring("already associated"),
					ContainSubstring("reused"),
					ContainSubstring("cannot be reused"),
					ContainSubstring("identity"),
					ContainSubstring("conflict"),
					ContainSubstring("duplicate"),
				), "Error message should indicate that managed identities cannot be reused")
			} else {
				// This is a bug - the cluster was created successfully with reused identities!
				By("UNEXPECTED: Cluster creation succeeded - checking final state")
				GinkgoWriter.Printf("⚠⚠⚠ UNEXPECTED: Cluster creation succeeded!\n")

				secondCluster, getErr := hcpClient.Get(ctx, *resourceGroup.Name, secondClusterName, nil)
				if getErr == nil && secondCluster.Properties != nil && secondCluster.Properties.ProvisioningState != nil {
					GinkgoWriter.Printf("⚠⚠⚠ Second cluster state: %s\n", *secondCluster.Properties.ProvisioningState)
					GinkgoWriter.Printf("⚠⚠⚠ This indicates a BUG: The system should NOT allow reusing managed identities across clusters!\n")
					GinkgoWriter.Printf("⚠⚠⚠ Cluster 1: %s (RG: %s)\n", clusterEnv.Name, customerEnv.CustomerRGName)
					GinkgoWriter.Printf("⚠⚠⚠ Cluster 2: %s (RG: %s)\n", secondClusterName, *resourceGroup.Name)
					GinkgoWriter.Printf("⚠⚠⚠ Both clusters are using the same managed identities, which violates the documented requirement.\n")
					GinkgoWriter.Printf("⚠⚠⚠ \n")
					GinkgoWriter.Printf("⚠⚠⚠ Shared identities:\n")
					for operatorName, identityID := range existingUserAssignedIdentities.ControlPlaneOperators {
						GinkgoWriter.Printf("⚠⚠⚠   - Control Plane Operator '%s': %s\n", operatorName, *identityID)
					}
					for operatorName, identityID := range existingUserAssignedIdentities.DataPlaneOperators {
						GinkgoWriter.Printf("⚠⚠⚠   - Data Plane Operator '%s': %s\n", operatorName, *identityID)
					}
					if existingUserAssignedIdentities.ServiceManagedIdentity != nil {
						GinkgoWriter.Printf("⚠⚠⚠   - Service Managed Identity: %s\n", *existingUserAssignedIdentities.ServiceManagedIdentity)
					}
				}

				Fail("VALIDATION BUG DETECTED: Cluster creation succeeded with reused managed identities. " +
					"According to ARO-HCP documentation (cluster-service/cluster-creation.md), " +
					"'Managed Identities cannot be reused between operators nor between clusters. " +
					"This is, each operator must use a different managed identity, and different clusters " +
					"must use different managed identities, even for the same operators.' " +
					"This test expected the creation to fail, but it succeeded. " +
					"Please implement validation to prevent reusing managed identities across clusters.")
			}
		},
	)
})
