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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hcpsdk20251223preview "github.com/Azure/ARO-HCP/test/sdk/v20251223preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

// This test creates a default (Public visibility) cluster and verifies both
// KAS access paths that define the PublicAndPrivate topology:
//   - Public path: KAS is reachable from outside the VNet via shared ingress
//   - Private/Swift path: worker nodes reach KAS via Swift networking, proven
//     by nodes reporting Ready and the API server successfully fetching pod logs
var _ = Describe("Customer", func() {
	It("should create a default cluster and verify KAS is accessible via both public shared ingress and private Swift networking from worker nodes",
		labels.RequireNothing,
		labels.High,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.CreateCluster,
		labels.MIContainers(1),
		func(ctx context.Context) {
			const (
				customerClusterName  = "pub-priv-kas"
				customerNodePoolName = "np-1"
			)

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "pub-priv-kas", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for PublicAndPrivate KAS test")

			By("creating cluster parameters with default (Public) API visibility")
			clusterParams := framework.NewDefaultClusterParams20251223()
			clusterParams.ClusterName = customerClusterName
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.OpenshiftVersionId = "4.22"

			By("creating customer resources (infrastructure and managed identities)")
			clusterParams, err = tc.CreateClusterCustomerResources20251223(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for PublicAndPrivate KAS cluster")

			By("creating the HCP cluster with default (Public) visibility")
			err = tc.CreateHCPClusterFromParam20251223(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %q", customerClusterName)

			By("verifying cluster API visibility is Public via ARM GET")
			hcpClient := tc.Get20251223ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
			cluster, err := hcpClient.Get(ctx, *resourceGroup.Name, customerClusterName, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to get cluster %q to verify API visibility", customerClusterName)
			Expect(cluster.Properties).ToNot(BeNil(), "cluster %q Properties was nil", customerClusterName)
			Expect(cluster.Properties.API).ToNot(BeNil(), "cluster %q Properties.API was nil", customerClusterName)
			Expect(cluster.Properties.API.Visibility).ToNot(BeNil(), "cluster %q Properties.API.Visibility was nil", customerClusterName)
			Expect(*cluster.Properties.API.Visibility).To(Equal(hcpsdk20251223preview.VisibilityPublic),
				"cluster %q API visibility should be Public", customerClusterName)
			Expect(cluster.Properties.API.URL).ToNot(BeNil(), "cluster %q Properties.API.URL was nil", customerClusterName)
			apiURL := *cluster.Properties.API.URL
			GinkgoLogr.Info("Cluster created with Public visibility", "clusterName", customerClusterName, "apiURL", apiURL)

			By("creating the node pool")
			nodePoolParams := framework.NewDefaultNodePoolParams20251223()
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.Replicas = int32(2)

			err = tc.CreateNodePoolFromParam20251223(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams.ManagedResourceGroupName,
				customerClusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create node pool %q for PublicAndPrivate KAS cluster %q",
				customerNodePoolName, customerClusterName)

			By("getting admin credentials for the cluster")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20240610(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for cluster %q", customerClusterName)

			By("verifying KAS is reachable from outside the VNet via shared ingress (public path)")
			Eventually(func(g Gomega) {
				err := framework.TestHTTPSConnectivity(ctx, apiURL+"/healthz", 10*time.Second, true)
				g.Expect(err).NotTo(HaveOccurred(),
					"KAS should be reachable from outside the VNet via shared ingress, but got error: %v", err)
			}, 5*time.Minute, 15*time.Second).Should(Succeed(),
				"KAS public endpoint should be reachable from outside the VNet via shared ingress")
			GinkgoLogr.Info("Confirmed KAS is reachable from outside the VNet via shared ingress (public path)")

			By("verifying worker nodes are Ready (proves kubelet-to-KAS Swift path is functional)")
			Expect(verifiers.VerifyNodeCount(customerClusterName, 2).Verify(ctx, adminRESTConfig)).To(Succeed(),
				"expected 2 nodes for cluster %q", customerClusterName)
			Expect(verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)).To(Succeed(),
				"all nodes should be Ready, proving kubelet-to-KAS connectivity via Swift networking")
			GinkgoLogr.Info("All worker nodes are Ready, confirming Swift networking path to KAS is functional")

			By("verifying bidirectional Swift connectivity by fetching router-default pod logs")
			logVerifier := verifiers.VerifyGetDeploymentLogs("openshift-ingress", "router-default", "router")
			Eventually(func() error {
				return logVerifier.Verify(ctx, adminRESTConfig)
			}, 10*time.Minute, 30*time.Second).Should(Succeed(),
				"fetching router-default logs should succeed, proving KAS-to-kubelet Swift path")
			GinkgoLogr.Info("Bidirectional Swift connectivity confirmed via pod log retrieval")

			By("verifying public ingress is reachable from outside the VNet (console URL)")
			var consoleURL string
			Eventually(func(g Gomega) {
				resp, err := hcpClient.Get(ctx, *resourceGroup.Name, customerClusterName, nil)
				g.Expect(err).NotTo(HaveOccurred(), "failed to get cluster %q for console URL", customerClusterName)
				g.Expect(resp.Properties).ToNot(BeNil(), "cluster %q Properties was nil", customerClusterName)
				g.Expect(resp.Properties.Console).ToNot(BeNil(), "cluster %q Properties.Console was nil", customerClusterName)
				g.Expect(resp.Properties.Console.URL).ToNot(BeNil(), "cluster %q Properties.Console.URL was nil", customerClusterName)
				consoleURL = *resp.Properties.Console.URL
			}, 15*time.Minute, 30*time.Second).Should(Succeed(), "console URL should become available for cluster %q", customerClusterName)
			GinkgoLogr.Info("Console URL available", "url", consoleURL)

			Eventually(func(g Gomega) {
				err := framework.TestHTTPSConnectivity(ctx, consoleURL, 10*time.Second, true)
				g.Expect(err).NotTo(HaveOccurred(),
					"public ingress (console) should be reachable from outside the VNet, but got error: %v", err)
			}, 10*time.Minute, 15*time.Second).Should(Succeed(),
				"public ingress should be reachable from outside the VNet")
			GinkgoLogr.Info("Public ingress reachable from outside the VNet, confirming shared ingress is operational")
		},
	)
})
