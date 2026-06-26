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
	"os"
	"sort"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/kubernetes"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

// nodePoolHash computes a deterministic SHA-256 over the Kubernetes node objects
// belonging to nodePoolName. The hash covers node Name, UID, KubeletVersion, and
// OSImage so that both identity (name) and version (kubelet/OS) changes are detected.
// UID inclusion catches VM replacements that preserve the node name.
func nodePoolHash(ctx context.Context, kubeClient kubernetes.Interface, nodePoolName string) (string, error) {
	nodeList, err := kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("list nodes: %w", err)
	}

	poolNodes, err := framework.SelectNodesBelongingToNodePool(nodeList.Items, nodePoolName)
	if err != nil {
		return "", fmt.Errorf("select nodes for pool %q: %w", nodePoolName, err)
	}

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
	return hex.EncodeToString(h.Sum(nil)), nil
}

var _ = Describe("HypershiftOperator in-place upgrade", func() {
	It("validates node pool rollout after upgrade",
		labels.Critical,
		labels.Positive,
		labels.UpgradeInPlace,
		func(ctx context.Context) {
			const (
				// Time allowed for the node pool rollout to produce a hash change after
				// the pipeline deploy completes.
				rolloutObservationWindow = 20 * time.Minute
				rolloutPollInterval      = 30 * time.Second
			)

			// Validate required env vars before provisioning any resources.
			overrideConfigFile := os.Getenv("OVERRIDE_CONFIG_FILE")
			Expect(overrideConfigFile).NotTo(BeEmpty(),
				"OVERRIDE_CONFIG_FILE must be set to the hypershift operator override config path")

			deployEnv := os.Getenv("DEPLOY_ENV")
			Expect(deployEnv).NotTo(BeEmpty(), "DEPLOY_ENV must be set for make pipeline/RP.HypershiftOperator")

			suffix := rand.String(6)
			clusterName := framework.SuffixName("e2e-upgrade", suffix, 64)
			nodePoolName := "nodepool-" + suffix

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "e2e-upgrade", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for upgrade test")

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

			By("capturing baseline node pool hash before upgrade")
			hcpClusterClient := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20240610(
				ctx,
				hcpClusterClient,
				resourceGroupName,
				clusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for cluster %q", clusterName)

			kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create kubernetes client for cluster %q", clusterName)

			baselineHash, err := nodePoolHash(ctx, kubeClient, nodePoolName)
			Expect(err).NotTo(HaveOccurred(), "failed to compute baseline node pool hash for %q", nodePoolName)
			GinkgoLogr.Info("baseline node pool hash captured", "nodepool", nodePoolName, "hash", baselineHash)

			By("running make pipeline/RP.HypershiftOperator to deploy upgraded operator")
			// MakeRunner inherits all environment variables from the test process so that
			// OVERRIDE_CONFIG_FILE, DEPLOY_ENV, and any pipeline flags (SKIP_CONFIRM,
			// PERSIST) set by the openshift/release step script are passed through.
			// stdout/stderr are forwarded to GinkgoWriter so they appear in the test log.
			makeRunner := &framework.MakeRunner{
				ExtraEnv: []string{"SKIP_CONFIRM=true"},
				Logger:   GinkgoLogr,
			}
			err = makeRunner.RunWithOutput(ctx, "pipeline/RP.HypershiftOperator", GinkgoWriter, GinkgoWriter)
			Expect(err).NotTo(HaveOccurred(), "make pipeline/RP.HypershiftOperator failed")
			GinkgoLogr.Info("HypershiftOperator pipeline deploy completed")

			By("confirming node pool hash remains stable after upgrade")
			GinkgoLogr.Info("starting stability observation",
				"nodepool", nodePoolName,
				"baselineHash", baselineHash,
				"window", rolloutObservationWindow,
			)
			observationStart := time.Now()
			Consistently(func(g Gomega) {
				currentHash, hashErr := nodePoolHash(ctx, kubeClient, nodePoolName)
				g.Expect(hashErr).NotTo(HaveOccurred(), "failed to compute post-upgrade node pool hash for %q", nodePoolName)
				g.Expect(currentHash).To(Equal(baselineHash),
					"node pool %q hash changed after %s (cluster %q): was %s, now %s",
					nodePoolName, time.Since(observationStart).Round(time.Second), clusterName, baselineHash, currentHash,
				)
			}, rolloutObservationWindow, rolloutPollInterval).Should(Succeed(),
				"node pool %q showed unexpected hash change within %s after upgrade (cluster %q)",
				nodePoolName, rolloutObservationWindow, clusterName,
			)
		})
})
