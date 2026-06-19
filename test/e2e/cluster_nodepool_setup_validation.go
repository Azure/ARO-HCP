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
	"fmt"
	"sort"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/integration"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Setup validation", func() {
	var (
		customerEnv *integration.CustomerEnv
		clusterInfo *integration.Cluster
		nodePools   []integration.Nodepool
	)

	BeforeEach(func() {
		By("preparing environment values from e2e-setup.json")
		customerEnv = &e2eSetup.CustomerEnv
		clusterInfo = &e2eSetup.Cluster
		nodePools = e2eSetup.Nodepools
	})

	It("confirms node pool node hashes remain unchanged for 15 minutes",
		labels.RequireHappyPathInfra,
		labels.Critical,
		labels.Positive,
		labels.SetupValidation,
		func(ctx context.Context) {
			const (
				pollInterval    = 30 * time.Second
				stabilityPeriod = 15 * time.Minute
			)

			tc := framework.NewTestContext()

			By("registering the pre-existing resource group for debug collection and cleanup")
			tc.RegisterResourceGroup(customerEnv.CustomerRGName)

			By("getting admin REST config to access the HCP cluster")
			hcpClusterClient := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20240610(
				ctx,
				hcpClusterClient,
				customerEnv.CustomerRGName,
				clusterInfo.Name,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for cluster %q", clusterInfo.Name)
			DeferCleanup(func(ctx context.Context) {
				By("revoking admin credentials for cluster")
				revokeErr := tc.RevokeCredentialsAndWait20240610(ctx, hcpClusterClient, customerEnv.CustomerRGName, clusterInfo.Name, 15*time.Minute)
				Expect(revokeErr).NotTo(HaveOccurred(), "failed to revoke admin credentials for cluster %q", clusterInfo.Name)
			})

			kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create kubernetes client for cluster %q", clusterInfo.Name)

			By("asserting node pool hashes remain unchanged for 15 minutes")
			Consistently(func(g Gomega) {
				nodeList, listErr := kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
				g.Expect(listErr).NotTo(HaveOccurred(), "failed to list nodes for cluster %q", clusterInfo.Name)

				for _, np := range nodePools {
					if np.Hash == "" {
						continue
					}

					poolNodes, selectErr := framework.SelectNodesBelongingToNodePool(nodeList.Items, np.Name)
					g.Expect(selectErr).NotTo(HaveOccurred(), "failed to select nodes for node pool %q", np.Name)

					sort.Slice(poolNodes, func(i, j int) bool {
						return poolNodes[i].Name < poolNodes[j].Name
					})

					h := sha256.New()
					for _, node := range poolNodes {
						// UID changes whenever the node object is recreated, catching VM replacements.
						fmt.Fprintf(h, "%s/%s/%s/%s\n",
							node.Name,
							node.UID,
							node.Status.NodeInfo.KubeletVersion,
							node.Status.NodeInfo.OSImage,
						)
					}
					currentHash := hex.EncodeToString(h.Sum(nil))

					g.Expect(currentHash).To(Equal(np.Hash),
						"node pool %q hash changed (cluster %q): baseline %s, current %s",
						np.Name, clusterInfo.Name, np.Hash, currentHash,
					)
				}
			}, stabilityPeriod, pollInterval).Should(Succeed(),
				"node pool hashes should remain stable for %s (cluster %q)", stabilityPeriod, clusterInfo.Name,
			)
		})
})
