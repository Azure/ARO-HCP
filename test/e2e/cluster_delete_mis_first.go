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

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Customer", func() {
	It("should be able to delete a cluster when managed identities were deleted first",
		labels.RequireNothing,
		labels.Medium,
		labels.Positive,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			const (
				clusterName = "cluster-del-mi"
			)

			tc := framework.NewTestContext()

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "rg-del-mi", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating customer infrastructure and managed identities")
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = clusterName
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)

			clusterParams, err = tc.CreateClusterCustomerResources(
				ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{
					"persistTagValue": false,
				},
				TestArtifactsFS,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating the HCP cluster")
			err = tc.CreateHCPClusterFromParam(ctx, *resourceGroup.Name, clusterParams, 45*time.Minute)
			Expect(err).NotTo(HaveOccurred())

			By("getting the cluster and extracting managed identity resource IDs")
			hcpClient := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
			cluster, err := hcpClient.Get(ctx, *resourceGroup.Name, clusterName, nil)
			Expect(err).NotTo(HaveOccurred())

			Expect(cluster.Identity).NotTo(BeNil())
			Expect(cluster.Identity.UserAssignedIdentities).NotTo(BeEmpty())
			Expect(cluster.Properties).NotTo(BeNil())
			Expect(cluster.Properties.Platform).NotTo(BeNil())
			Expect(cluster.Properties.Platform.OperatorsAuthentication).NotTo(BeNil())
			Expect(cluster.Properties.Platform.OperatorsAuthentication.UserAssignedIdentities).NotTo(BeNil())

			// Collect all managed identity resource IDs
			managedIdentityResourceIDs := make(map[string]struct{})

			// Control plane user-assigned identities
			for resourceID := range cluster.Identity.UserAssignedIdentities {
				managedIdentityResourceIDs[resourceID] = struct{}{}
			}

			// Operator identities (service, control plane, and data plane)
			operatorIdentities := cluster.Properties.Platform.OperatorsAuthentication.UserAssignedIdentities
			Expect(operatorIdentities.ServiceManagedIdentity).NotTo(BeNil())
			Expect(operatorIdentities.ControlPlaneOperators).NotTo(BeEmpty())
			Expect(operatorIdentities.DataPlaneOperators).NotTo(BeEmpty())

			// Service managed identity
			Expect(*operatorIdentities.ServiceManagedIdentity).NotTo(BeEmpty())
			managedIdentityResourceIDs[*operatorIdentities.ServiceManagedIdentity] = struct{}{}

			// Control plane operator identities
			for _, resourceID := range operatorIdentities.ControlPlaneOperators {
				Expect(resourceID).NotTo(BeNil())
				Expect(*resourceID).NotTo(BeEmpty())
				managedIdentityResourceIDs[*resourceID] = struct{}{}
			}

			// Data plane operator identities
			for _, resourceID := range operatorIdentities.DataPlaneOperators {
				Expect(resourceID).NotTo(BeNil())
				Expect(*resourceID).NotTo(BeEmpty())
				managedIdentityResourceIDs[*resourceID] = struct{}{}
			}

			Expect(managedIdentityResourceIDs).NotTo(BeEmpty())

			By("deleting all managed identities")
			resourcesClient := tc.GetARMResourcesClientFactoryOrDie(ctx).NewClient()
			for resourceID := range managedIdentityResourceIDs {
				err := framework.DeleteResourceByID(ctx, resourcesClient, resourceID, 5*time.Minute)
				Expect(err).NotTo(HaveOccurred())
			}

			By("attempting to delete the cluster (should succeed despite missing MIs)")
			err = framework.DeleteHCPCluster(
				ctx,
				hcpClient,
				*resourceGroup.Name,
				clusterName,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the cluster was deleted")
			_, err = hcpClient.Get(ctx, *resourceGroup.Name, clusterName, nil)
			Expect(err).To(HaveOccurred())
		})
})
