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
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Customer", func() {
	timeBombDeadline := framework.V20270201DeploymentDeadline

	It("should create a cluster using v20270201 API and verify cluster health",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.CreateCluster,
		labels.MIContainers(1),
		func(ctx context.Context) {
			const (
				customerClusterName  = "v20270201"
				customerNodePoolName = "np-1"
			)

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "v20270201", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for v20270201 test")

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams20270201()
			clusterParams.ClusterName = customerClusterName
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)

			By("creating customer resources (infrastructure and managed identities)")
			clusterParams, err = tc.CreateClusterCustomerResources20270201(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for v20270201 cluster")

			By("creating the HCP cluster via v20270201")
			err = tc.CreateHCPClusterFromParam20270201(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil,
				framework.ClusterCreationTimeout,
			)
			if framework.IsAPINotDeployedError(err) {
				if time.Now().Before(timeBombDeadline) {
					Skip(fmt.Sprintf("v20270201 API not yet deployed; skipping until %s", timeBombDeadline.Format(time.RFC3339)))
				}
				Fail(fmt.Sprintf("v20270201 API still not deployed as of %s deadline", timeBombDeadline.Format(time.RFC3339)))
			}
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %q via v20270201", customerClusterName)

			By("creating the node pool")
			nodePoolParams := framework.NewDefaultNodePoolParams20270201()
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.Replicas = int32(2)

			err = tc.CreateNodePoolFromParam20270201(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams.ManagedResourceGroupName,
				customerClusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create node pool %q for v20270201 cluster %q",
				customerNodePoolName, customerClusterName)

			By("getting admin credentials for the cluster")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20270201(
				ctx,
				tc.Get20270201ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for v20270201 cluster %q", customerClusterName)

			By("verifying the cluster is viable")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to verify cluster health for v20270201 cluster %q", customerClusterName)

			By("verifying a simple web app can run")
			err = verifiers.VerifySimpleWebApp().Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to verify simple web app runs on v20270201 cluster %q", customerClusterName)
		},
	)
})
