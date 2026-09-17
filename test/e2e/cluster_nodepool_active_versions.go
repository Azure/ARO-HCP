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

	"github.com/blang/semver/v4"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Customer", func() {
	timeBombDeadline := framework.Must(time.Parse(time.RFC3339, "2026-11-01T00:00:00Z"))

	It("should be able to retrieve cluster and nodepool status active versions",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.CreateCluster,
		labels.MIContainers(1),
		func(ctx context.Context) {
			const (
				customerClusterName  = "v20261001"
				customerNodePoolName = "np-1"
			)

			tc := framework.NewTestContext()

			By("checking API version availability")
			apiAvailable, err := tc.IsHCPAPIVersionAvailable(ctx, "2026-10-01-preview")
			Expect(err).NotTo(HaveOccurred(), "failed to check API version availability")
			if !apiAvailable {
				if time.Now().After(timeBombDeadline) {
					Fail(fmt.Sprintf("API version 2026-10-01-preview should be fully available by %s", timeBombDeadline.Format(time.RFC3339)))
				}
				Skip("API version 2026-10-01-preview is not fully available in this environment")
			}

			if tc.UsePooledIdentities() {
				err = tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "v20261001preview", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for v20261001preview test")

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams20261001()
			clusterParams.ClusterName = customerClusterName
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)

			By("creating customer resources (infrastructure and managed identities)")
			clusterParams, err = tc.CreateClusterCustomerResources20261001(ctx,
				resourceGroup,
				clusterParams,
				map[string]any{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for v20261001preview cluster")

			By("creating the HCP cluster via v20261001preview")
			err = tc.CreateHCPClusterFromParam20261001(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil,
				framework.ClusterCreationTimeout,
			)
			if isAPINotDeployedError(err) {
				if time.Now().Before(timeBombDeadline) {
					Skip(fmt.Sprintf("v20261001preview API not yet deployed; skipping until %s", timeBombDeadline.Format(time.RFC3339)))
				}
				Fail(fmt.Sprintf("v20261001preview API still not deployed as of %s deadline", timeBombDeadline.Format(time.RFC3339)))
			}
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %q via v20261001preview", customerClusterName)

			By("creating the node pool")
			nodePoolParams := framework.NewDefaultNodePoolParams20261001()
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.Replicas = int32(2)

			err = tc.CreateNodePoolFromParam20261001(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams.ManagedResourceGroupName,
				customerClusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create node pool %q for v20261001preview cluster %q",
				customerNodePoolName, customerClusterName)

			By("verifying active versions are returned for the cluster")
			clusterResp, err := tc.Get20261001ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient().Get(ctx, *resourceGroup.Name, customerClusterName, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to GET cluster %q", customerClusterName)
			Expect(clusterResp.Properties).NotTo(BeNil(), "cluster %q response Properties was nil", customerClusterName)
			Expect(clusterResp.Properties.Status).NotTo(BeNil(), "cluster %q response Properties.Status was nil", customerClusterName)
			Expect(clusterResp.Properties.Status.ActiveVersions).NotTo(BeEmpty(), "cluster %q should have at least one active version", customerClusterName)
			var clusterActiveVersions []string
			for _, v := range clusterResp.Properties.Status.ActiveVersions {
				Expect(v.Version).NotTo(BeNil(), "cluster %q active version entry should have a non-nil Version", customerClusterName)
				Expect(*v.Version).NotTo(BeEmpty(), "cluster %q active version should not be empty", customerClusterName)
				clusterActiveVersions = append(clusterActiveVersions, *v.Version)
			}
			parsed, err := semver.ParseTolerant(clusterParams.OpenshiftVersionId)
			Expect(err).NotTo(HaveOccurred(), "failed to parse cluster version %q as semver", clusterParams.OpenshiftVersionId)
			expectedClusterVersion := fmt.Sprintf("%d.%d", parsed.Major, parsed.Minor)
			Expect(clusterActiveVersions).To(ContainElement(expectedClusterVersion),
				"cluster %q active versions %v should contain requested version %q (major.minor of %q)",
				customerClusterName, clusterActiveVersions, expectedClusterVersion, clusterParams.OpenshiftVersionId)

			By("verifying active versions are returned for the node pool")
			nodePool, err := framework.GetNodePool20261001(ctx,
				tc.Get20261001ClientFactoryOrDie(ctx).NewNodePoolsClient(),
				*resourceGroup.Name, customerClusterName, customerNodePoolName,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to GET node pool %q", customerNodePoolName)
			Expect(nodePool.Properties).NotTo(BeNil(), "node pool %q response Properties was nil", customerNodePoolName)
			Expect(nodePool.Properties.Status).NotTo(BeNil(), "node pool %q response Properties.Status was nil", customerNodePoolName)
			Expect(nodePool.Properties.Status.ActiveVersions).To(HaveLen(1),
				"node pool %q should have exactly one active version after initial creation", customerNodePoolName)
			Expect(nodePool.Properties.Status.ActiveVersions[0].Version).NotTo(BeNil(),
				"node pool %q active version entry should have a non-nil Version", customerNodePoolName)
			Expect(*nodePool.Properties.Status.ActiveVersions[0].Version).To(Equal(nodePoolParams.OpenshiftVersionId),
				"node pool %q active version should match requested version", customerNodePoolName)

			By("getting admin credentials for the cluster")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20261001(
				ctx,
				tc.Get20261001ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for v20261001preview cluster %q", customerClusterName)

			By("verifying the cluster is viable")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to verify cluster health for v20261001preview cluster %q", customerClusterName)

			By("verifying a simple web app can run")
			err = verifiers.VerifySimpleWebApp().Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to verify simple web app runs on v20261001preview cluster %q", customerClusterName)
		},
	)
})
