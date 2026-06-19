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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/kubernetes"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/integration"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("E2E setup", func() {
	It("should create a cluster and node pool and export the state to e2e-setup.json",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		labels.CreateCluster,
		func(ctx context.Context) {
			suffix := rand.String(6)
			clusterName := framework.SuffixName("e2e-setup-cluster", suffix, 64)
			nodePoolName := "nodepool-" + suffix

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "e2e-setup", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for e2e setup")

			resourceGroupName := *resourceGroup.Name

			clusterParams := framework.NewDefaultClusterParams20240610()
			clusterParams.ClusterName = clusterName
			managedResourceGroupName := framework.SuffixName(resourceGroupName, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources (infrastructure and managed identities)")
			clusterParams, err = tc.CreateClusterCustomerResources20240610(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for cluster %q", clusterName)

			By("creating the HCP cluster")
			err = tc.CreateHCPClusterFromParam20240610(ctx,
				GinkgoLogr,
				resourceGroupName,
				clusterParams,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %q", clusterName)

			By("creating the node pool")
			nodePoolParams := framework.NewDefaultNodePoolParams20240610()
			nodePoolParams.ClusterName = clusterName
			nodePoolParams.NodePoolName = nodePoolName

			err = tc.CreateNodePoolFromParam20240610(ctx,
				GinkgoLogr,
				resourceGroupName,
				managedResourceGroupName,
				clusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create node pool %q for cluster %q", nodePoolName, clusterName)

			By("getting admin REST config to access the HCP cluster")
			hcpClusterClient := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20240610(
				ctx,
				hcpClusterClient,
				resourceGroupName,
				clusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for cluster %q", clusterName)
			DeferCleanup(func(ctx context.Context) {
				By("revoking admin credentials for cluster")
				revokeErr := tc.RevokeCredentialsAndWait20240610(ctx, hcpClusterClient, resourceGroupName, clusterName, 15*time.Minute)
				Expect(revokeErr).NotTo(HaveOccurred(), "failed to revoke admin credentials for cluster %q", clusterName)
			})

			By("listing nodes belonging to node pool and computing their hash")
			kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create kubernetes client for cluster %q", clusterName)

			nodeList, err := kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to list nodes for cluster %q", clusterName)

			poolNodes, err := framework.SelectNodesBelongingToNodePool(nodeList.Items, nodePoolName)
			Expect(err).NotTo(HaveOccurred(), "failed to select nodes for node pool %q", nodePoolName)

			// Sort by node name for a deterministic hash regardless of list ordering.
			sort.Slice(poolNodes, func(i, j int) bool {
				return poolNodes[i].Name < poolNodes[j].Name
			})

			h := sha256.New()
			for _, node := range poolNodes {
				// UID changes whenever the node object is recreated (e.g. VMSS instance replaced),
				// catching redeployments that preserve the node name, version, and OS image.
				fmt.Fprintf(h, "%s/%s/%s/%s\n",
					node.Name,
					node.UID,
					node.Status.NodeInfo.KubeletVersion,
					node.Status.NodeInfo.OSImage,
				)
			}
			nodePoolHash := hex.EncodeToString(h.Sum(nil))

			By("assembling and writing e2e-setup.json")

			setup := integration.SetupModel{
				E2ESetup: integration.E2ESetup{
					Name: "e2e-setup-" + suffix,
					Tags: []string{"e2e-setup"},
				},
				CustomerEnv: integration.CustomerEnv{
					CustomerRGName:   resourceGroupName,
					CustomerVNetName: clusterParams.VnetName,
					CustomerNSGName:  clusterParams.NsgName,
				},
				Cluster: integration.Cluster{
					Name: clusterName,
				},
				Nodepools: []integration.Nodepool{
					{
						Name: nodePoolName,
						Hash: nodePoolHash,
					},
				},
			}

			setupJSON, err := json.MarshalIndent(setup, "", "  ")
			Expect(err).NotTo(HaveOccurred(), "failed to marshal SetupModel to JSON")

			err = integration.WriteE2ESetupFile(setupJSON)
			Expect(err).NotTo(HaveOccurred(), "failed to write e2e-setup.json")

			GinkgoLogr.Info("e2e-setup.json written", "cluster", clusterName, "nodepool", nodePoolName)
		})
})
