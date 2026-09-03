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
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	hcpsdk20260630preview "github.com/Azure/ARO-HCP/test/sdk/v20260630preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

// This test creates a fully private cluster with all three visibility
// dimensions set to Private: api.visibility, ingress.type, and
// keyVault.visibility. It validates that there are no interaction effects
// between the private settings and the cluster is fully operational.
var _ = Describe("Customer", func() {
	It("should create a fully private cluster with private KAS, private ingress, and private KeyVault and verify all are operational",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.CreateCluster,
		labels.MIContainers(1),
		func(ctx context.Context) {
			const (
				customerClusterName  = "full-priv-kv"
				customerNodePoolName = "np-1"
			)

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "full-priv-kv", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for fully private with KV test")

			By("creating cluster parameters with all visibility set to Private")
			clusterParams := framework.NewDefaultClusterParams20260630()
			clusterParams.ClusterName = customerClusterName
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.APIVisibility = "Private"
			clusterParams.IngressType = "Private"
			clusterParams.KeyVaultVisibility = "Private"
			clusterParams.OpenshiftVersionId = "4.22"

			By("creating customer resources with private KeyVault")
			clusterParams, err = tc.CreateClusterCustomerResources20260630(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{
					"privateKeyVault": true,
				},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for fully private cluster with private KV")

			By("deploying test VM in customer VNet")
			vmName, _, err := tc.DeployTestVM(ctx, TestArtifactsFS, *resourceGroup.Name, customerClusterName, clusterParams.VnetName, clusterParams.SubnetName)
			Expect(err).NotTo(HaveOccurred(), "failed to deploy test VM for fully private with KV verification")

			By("creating the HCP cluster with all private settings")
			clusterResource, err := framework.BuildHCPClusterFromParams20260630(clusterParams, tc.Location(), nil)
			Expect(err).NotTo(HaveOccurred(), "failed to build HCP cluster resource from params")

			if clusterResource.Properties != nil && clusterResource.Properties.Etcd != nil &&
				clusterResource.Properties.Etcd.DataEncryption != nil &&
				clusterResource.Properties.Etcd.DataEncryption.CustomerManaged != nil &&
				clusterResource.Properties.Etcd.DataEncryption.CustomerManaged.Kms != nil {
				clusterResource.Properties.Etcd.DataEncryption.CustomerManaged.Kms.Visibility = to.Ptr(hcpsdk20260630preview.KeyVaultVisibilityPrivate)
			}

			_, err = framework.CreateHCPClusterAndWait20260630(
				ctx,
				GinkgoLogr,
				tc.Get20260630ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				clusterResource,
				framework.ClusterCreationTimeout,
			)
			if isAPINotDeployedError(err) {
				if time.Now().Before(framework.V20260630PreviewDeploymentDeadline) {
					Skip(fmt.Sprintf("v20260630preview API not yet deployed; skipping until %s", framework.V20260630PreviewDeploymentDeadline.Format(time.RFC3339)))
				}
				Fail(fmt.Sprintf("v20260630preview API still not deployed as of %s deadline", framework.V20260630PreviewDeploymentDeadline.Format(time.RFC3339)))
			}
			Expect(err).NotTo(HaveOccurred(), "failed to create fully private HCP cluster %q with private KV", customerClusterName)

			By("verifying all visibility settings are Private via ARM GET")
			hcpClient := tc.Get20260630ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
			cluster, err := hcpClient.Get(ctx, *resourceGroup.Name, customerClusterName, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to get cluster %q to verify fully private config with KV", customerClusterName)

			Expect(cluster.Properties).ToNot(BeNil(), "cluster %q Properties was nil", customerClusterName)

			Expect(cluster.Properties.API).ToNot(BeNil(), "cluster %q Properties.API was nil", customerClusterName)
			Expect(cluster.Properties.API.Visibility).ToNot(BeNil(), "cluster %q Properties.API.Visibility was nil", customerClusterName)
			Expect(*cluster.Properties.API.Visibility).To(Equal(hcpsdk20260630preview.VisibilityPrivate),
				"cluster %q API visibility should be Private", customerClusterName)

			Expect(cluster.Properties.Ingress).ToNot(BeNil(), "cluster %q Properties.Ingress was nil", customerClusterName)
			Expect(cluster.Properties.Ingress.Type).ToNot(BeNil(), "cluster %q Properties.Ingress.Type was nil", customerClusterName)
			Expect(*cluster.Properties.Ingress.Type).To(Equal(hcpsdk20260630preview.IngressTypePrivate),
				"cluster %q ingress type should be Private", customerClusterName)

			Expect(cluster.Properties.Etcd).ToNot(BeNil(), "cluster %q Properties.Etcd was nil", customerClusterName)
			Expect(cluster.Properties.Etcd.DataEncryption).ToNot(BeNil(), "cluster %q Properties.Etcd.DataEncryption was nil", customerClusterName)
			Expect(cluster.Properties.Etcd.DataEncryption.CustomerManaged).ToNot(BeNil(), "cluster %q Properties.Etcd.DataEncryption.CustomerManaged was nil", customerClusterName)
			Expect(cluster.Properties.Etcd.DataEncryption.CustomerManaged.Kms).ToNot(BeNil(), "cluster %q Properties.Etcd.DataEncryption.CustomerManaged.Kms was nil", customerClusterName)
			Expect(cluster.Properties.Etcd.DataEncryption.CustomerManaged.Kms.Visibility).ToNot(BeNil(), "cluster %q KV Visibility was nil", customerClusterName)
			Expect(*cluster.Properties.Etcd.DataEncryption.CustomerManaged.Kms.Visibility).To(Equal(hcpsdk20260630preview.KeyVaultVisibilityPrivate),
				"cluster %q KeyVault visibility should be Private", customerClusterName)

			Expect(cluster.Properties.API.URL).ToNot(BeNil(), "cluster %q Properties.API.URL was nil", customerClusterName)
			apiURL := *cluster.Properties.API.URL
			GinkgoLogr.Info("Cluster created fully private with private KV",
				"clusterName", customerClusterName,
				"apiURL", apiURL,
				"keyVaultName", clusterParams.KeyVaultName)

			By("creating the node pool")
			nodePoolParams := framework.NewDefaultNodePoolParams20260630()
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.Replicas = int32(2)

			err = tc.CreateNodePoolFromParam20260630(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams.ManagedResourceGroupName,
				customerClusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create node pool %q for fully private cluster with KV %q",
				customerNodePoolName, customerClusterName)

			By("getting admin credentials for the cluster")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20240610(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for fully private cluster with KV %q", customerClusterName)

			By("verifying KAS is reachable from VM inside the VNet")
			internalIP, err := framework.GetPrivateKASInternalIP(ctx, tc, clusterParams.ManagedResourceGroupName)
			Expect(err).NotTo(HaveOccurred(), "failed to find private KAS internal LB IP in managed resource group %q", clusterParams.ManagedResourceGroupName)
			GinkgoLogr.Info("Found private KAS internal LB", "ip", internalIP)

			// Connect to the internal LB IP to prove network reachability to KAS from
			// inside the VNet. Skip TLS hostname verification because the server URL
			// uses an IP address rather than the hostname the cert was issued for.
			adminRESTConfig.Insecure = true
			adminRESTConfig.Host = fmt.Sprintf("https://%s:443", internalIP)

			kubeconfig, err := framework.GenerateKubeconfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to generate kubeconfig from admin REST config")
			kubeconfigB64 := base64.StdEncoding.EncodeToString([]byte(kubeconfig))

			Eventually(func(g Gomega) {
				versionCmd := fmt.Sprintf(
					"KUBECONFIG=$(mktemp) && "+
						"trap 'rm -f $KUBECONFIG' EXIT && "+
						"echo '%s' | base64 -d > $KUBECONFIG && "+
						"chmod 600 $KUBECONFIG && "+
						"kubectl --kubeconfig=$KUBECONFIG version 2>&1",
					kubeconfigB64,
				)
				versionOutput, runErr := framework.RunVMCommand(ctx, tc, *resourceGroup.Name, vmName, versionCmd, 2*time.Minute)
				g.Expect(runErr).NotTo(HaveOccurred(), "RunVMCommand failed for kubectl version (output: %s)", versionOutput)
				g.Expect(versionOutput).To(ContainSubstring("Server Version"),
					"kubectl version should show Server Version, proving KAS is reachable from VM (output: %s)", versionOutput)
			}, 60*time.Minute, 15*time.Second).Should(Succeed(), "KAS should be reachable from VM via internal LB")
			GinkgoLogr.Info("KAS is reachable from VM inside VNet")

			By("verifying KAS is NOT reachable from outside the VNet")
			Consistently(func(g Gomega) {
				err := framework.TestHTTPSConnectivity(ctx, apiURL+"/healthz", 10*time.Second, true)
				g.Expect(err).To(HaveOccurred(),
					"private KAS should not be reachable from outside the VNet, but connection to %s succeeded", apiURL)
			}, 2*time.Minute, 15*time.Second).Should(Succeed(),
				"private KAS should consistently be unreachable from outside the VNet")

			By("verifying ingress is reachable from VM and NOT from outside (via sample app)")
			sampleAppManifests, err := framework.SampleAppManifests("e2e-sample-app")
			Expect(err).NotTo(HaveOccurred(), "failed to generate sample app manifests")
			applyCmd := fmt.Sprintf(
				"KUBECONFIG=$(mktemp) && "+
					"trap 'rm -f $KUBECONFIG' EXIT && "+
					"echo '%s' | base64 -d > $KUBECONFIG && "+
					"chmod 600 $KUBECONFIG && "+
					"kubectl --kubeconfig=$KUBECONFIG create namespace e2e-sample-app --dry-run=client -o yaml | kubectl --kubeconfig=$KUBECONFIG apply -f - && "+
					"echo '%s' | base64 -d | kubectl --kubeconfig=$KUBECONFIG apply -f - 2>&1",
				kubeconfigB64,
				base64.StdEncoding.EncodeToString([]byte(sampleAppManifests)),
			)
			applyOutput, err := framework.RunVMCommand(ctx, tc, *resourceGroup.Name, vmName, applyCmd, 2*time.Minute)
			Expect(err).NotTo(HaveOccurred(),
				"failed to deploy sample app from VM (output: %s)", applyOutput)

			var routeHost string
			Eventually(func(g Gomega) {
				routeCmd := fmt.Sprintf(
					"KUBECONFIG=$(mktemp) && "+
						"trap 'rm -f $KUBECONFIG' EXIT && "+
						"echo '%s' | base64 -d > $KUBECONFIG && "+
						"chmod 600 $KUBECONFIG && "+
						"kubectl --kubeconfig=$KUBECONFIG get route -n e2e-sample-app agnhost -o jsonpath='{.spec.host}' 2>/dev/null",
					kubeconfigB64,
				)
				output, runErr := framework.RunVMCommand(ctx, tc, *resourceGroup.Name, vmName, routeCmd, 2*time.Minute)
				g.Expect(runErr).NotTo(HaveOccurred(), "failed to get route host from VM")
				routeHost = strings.TrimSpace(output)
				g.Expect(routeHost).NotTo(BeEmpty(), "route host should not be empty")
			}, 15*time.Minute, 15*time.Second).Should(Succeed(), "sample app route should become available")

			appURL := "https://" + routeHost

			curlCmd := fmt.Sprintf("curl -k -s -o /dev/null -w '%%{http_code}' --connect-timeout 10 %s", appURL)
			Eventually(func(g Gomega) {
				output, runErr := framework.RunVMCommand(ctx, tc, *resourceGroup.Name, vmName, curlCmd, 2*time.Minute)
				g.Expect(runErr).NotTo(HaveOccurred(), "RunVMCommand failed for ingress connectivity test")
				httpCode := strings.TrimSpace(output)
				g.Expect(httpCode).To(Equal("200"),
					"expected HTTP 200 from sample app via private ingress, got %s", httpCode)
			}, 10*time.Minute, 15*time.Second).Should(Succeed())

			Consistently(func(g Gomega) {
				err := framework.TestHTTPSConnectivity(ctx, appURL, 10*time.Second, true)
				g.Expect(err).To(HaveOccurred(),
					"private ingress should not be reachable from outside the VNet, but connection succeeded")
			}, 2*time.Minute, 15*time.Second).Should(Succeed(),
				"private ingress should consistently be unreachable from outside the VNet")

			GinkgoLogr.Info("Fully private cluster with private KeyVault is fully operational",
				"clusterName", customerClusterName)
		},
	)
})
