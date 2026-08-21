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
	"encoding/base64"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hcpsdk20251223preview "github.com/Azure/ARO-HCP/test/sdk/v20251223preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

// This test creates a cluster with private KAS (api.visibility: Private) and
// verifies that:
//   - The Kubernetes API server is only reachable from within the VNet
//   - The default ingress remains public (independence of KAS and ingress visibility)
var _ = Describe("Customer", func() {
	It("should create a cluster with private KAS and verify API server is only reachable from VNet while ingress remains public",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.CreateCluster,
		labels.MIContainers(1),
		func(ctx context.Context) {
			const (
				customerClusterName  = "private-kas"
				customerNodePoolName = "np-1"
			)

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "private-kas", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for private KAS test")

			By("creating cluster parameters with private API visibility")
			clusterParams := framework.NewDefaultClusterParams20251223()
			clusterParams.ClusterName = customerClusterName
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.APIVisibility = "Private"
			// Private KAS requires OCP >= 4.22 (CS validation rejects lower versions)
			clusterParams.OpenshiftVersionId = "4.22"

			By("creating customer resources (infrastructure and managed identities)")
			clusterParams, err = tc.CreateClusterCustomerResources20251223(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for private KAS cluster")

			By("deploying test VM in customer VNet")
			vmName, _, err := tc.DeployTestVM(ctx, TestArtifactsFS, *resourceGroup.Name, customerClusterName, clusterParams.VnetName, clusterParams.SubnetName)
			Expect(err).NotTo(HaveOccurred(), "failed to deploy test VM for private KAS verification")

			By("creating the HCP cluster with private KAS")
			err = tc.CreateHCPClusterFromParam20251223(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %q with private KAS", customerClusterName)

			By("verifying cluster API visibility is Private via ARM GET")
			hcpClient := tc.Get20251223ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
			cluster, err := hcpClient.Get(
				ctx,
				*resourceGroup.Name,
				customerClusterName,
				nil,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get cluster %q to verify private KAS", customerClusterName)
			Expect(cluster.Properties).ToNot(BeNil(), "cluster %q Properties was nil", customerClusterName)
			Expect(cluster.Properties.API).ToNot(BeNil(), "cluster %q Properties.API was nil", customerClusterName)
			Expect(cluster.Properties.API.Visibility).ToNot(BeNil(), "cluster %q Properties.API.Visibility was nil", customerClusterName)
			Expect(*cluster.Properties.API.Visibility).To(Equal(hcpsdk20251223preview.VisibilityPrivate),
				"cluster %q API visibility should be Private", customerClusterName)
			Expect(cluster.Properties.API.URL).ToNot(BeNil(), "cluster %q Properties.API.URL was nil", customerClusterName)
			apiURL := *cluster.Properties.API.URL
			GinkgoLogr.Info("Cluster created with private KAS", "clusterName", customerClusterName, "apiURL", apiURL)

			By("creating the node pool")
			nodePoolParams := framework.NewDefaultNodePoolParams20251223()
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.Replicas = int32(1)

			err = tc.CreateNodePoolFromParam20251223(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams.ManagedResourceGroupName,
				customerClusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create node pool %q for private KAS cluster %q",
				customerNodePoolName, customerClusterName)

			By("verifying KAS is reachable from VM inside the VNet")
			// Get admin credentials via ARM and override the server URL with
			// the internal LB IP so kubectl on the VM connects through the
			// private KAS endpoint.
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20251223(
				ctx,
				tc.Get20251223ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for private KAS cluster %q", customerClusterName)

			internalIP, err := framework.GetPrivateKASInternalIP(ctx, tc, clusterParams.ManagedResourceGroupName)
			Expect(err).NotTo(HaveOccurred(), "failed to find private KAS internal LB IP in managed resource group %q", clusterParams.ManagedResourceGroupName)
			GinkgoLogr.Info("Found private KAS internal LB", "ip", internalIP, "managedRG", clusterParams.ManagedResourceGroupName)

			adminRESTConfig.Host = fmt.Sprintf("https://%s:443", internalIP)

			kubeconfig, err := framework.GenerateKubeconfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to generate kubeconfig from admin REST config")
			kubeconfigB64 := base64.StdEncoding.EncodeToString([]byte(kubeconfig))

			// kubectl version hits /version (unauthenticated) through the
			// internal LB. A successful response proves the private KAS
			// network path (VM → customer subnet → internal LB → Swift →
			// KAS pods) is functional.
			versionCmd := fmt.Sprintf(
				"echo '%s' | base64 -d > /tmp/kubeconfig && "+
					"kubectl --kubeconfig=/tmp/kubeconfig version 2>/dev/null",
				kubeconfigB64,
			)
			versionOutput, err := framework.RunVMCommand(ctx, tc, *resourceGroup.Name, vmName, versionCmd, 2*time.Minute)
			Expect(err).NotTo(HaveOccurred(),
				"kubectl version should succeed from VM via private KAS internal LB (output: %s)", versionOutput)
			GinkgoLogr.Info("KAS is reachable from VM inside VNet", "output", versionOutput)

			By("verifying KAS is NOT reachable from outside the VNet")
			err = framework.TestHTTPSConnectivity(ctx, apiURL+"/healthz", 10*time.Second, true)
			Expect(err).To(HaveOccurred(),
				"private KAS should not be reachable from outside the VNet, but connection to %s succeeded", apiURL)
			GinkgoLogr.Info("Confirmed KAS is not reachable from outside the VNet", "error", err)

			By("verifying public ingress is reachable from outside the VNet")
			// The OpenShift console is deployed by default on every cluster
			// and served through the default ingress router. If the console
			// URL is reachable from outside the VNet, it proves the default
			// ingress is public — independent of private KAS.
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
					"public ingress (console) should be reachable from outside the VNet for private KAS cluster, but got error: %v", err)
			}, 10*time.Minute, 15*time.Second).Should(Succeed(),
				"public ingress should be reachable from outside the VNet despite private KAS")
			GinkgoLogr.Info("Confirmed public ingress is reachable from outside the VNet despite private KAS")
		},
	)
})
