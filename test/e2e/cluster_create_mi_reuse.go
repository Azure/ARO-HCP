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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Customer", func() {
	It("should be able to create a cluster with reused managed identities",
		labels.RequireNothing,
		labels.Medium,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.CreateCluster,
		func(ctx context.Context) {
			const clusterName = "cluster-mi-reuse"

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "rg-mi-reuse", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group")

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams20240610()
			clusterParams.ClusterName = clusterName
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)

			By("creating customer resources (infrastructure and managed identities)")
			clusterParams, err = tc.CreateClusterCustomerResources20240610(ctx,
				resourceGroup,
				clusterParams,
				map[string]any{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources")

			By("reusing the same managed identity for two control plane operators")
			cpOps := clusterParams.UserAssignedIdentitiesProfile.ControlPlaneOperators
			cpOps["ingress"] = cpOps["cluster-api-azure"]

			By("creating a cluster with reused managed identity")
			err = tc.CreateHCPClusterFromParam20240610(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create cluster with reused managed identity")
		})
})
