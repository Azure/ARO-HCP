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
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	operatorclient "github.com/openshift/client-go/operator/clientset/versioned"

	hcpsdk20251223preview "github.com/Azure/ARO-HCP/test/sdk/v20251223preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Customer", func() {
	It("should be able to create a no-CNI private cluster with a private key vault, a nodepool and install cilium CNI successfully",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.CreateCluster,
		func(ctx context.Context) {
			const (
				customerClusterName  = "cilium-cluster"
				customerNodePoolName = "cilium-np"
			)

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "complex-cilium-kv", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName
			clusterParams.KeyVaultVisibility = "Private"
			// Use "Other" network type to deploy without a default CNI
			clusterParams.Network.NetworkType = "Other"

			By("creating customer resources (infrastructure and managed identities)")
			clusterParams, err = tc.CreateClusterCustomerResources(ctx,
				resourceGroup,
				clusterParams,
				map[string]any{
					"privateKeyVault": true,
				},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating the HCP cluster with no CNI and private etcd via v20251223preview")
			clusterResource, err := framework.BuildHCPCluster20251223FromParams(clusterParams, tc.Location(), nil)
			Expect(err).NotTo(HaveOccurred())

			// Set KeyVault visibility to Private
			if clusterResource.Properties != nil && clusterResource.Properties.Etcd != nil &&
				clusterResource.Properties.Etcd.DataEncryption != nil &&
				clusterResource.Properties.Etcd.DataEncryption.CustomerManaged != nil &&
				clusterResource.Properties.Etcd.DataEncryption.CustomerManaged.Kms != nil {
				clusterResource.Properties.Etcd.DataEncryption.CustomerManaged.Kms.Visibility = to.Ptr(hcpsdk20251223preview.KeyVaultVisibilityPrivate)
			}

			_, err = framework.CreateHCPCluster20251223AndWait(
				ctx,
				GinkgoLogr,
				tc.Get20251223ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				clusterResource,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("getting admin credentials for the cluster")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				10*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("disabling kube-proxy via networks.operator.openshift.io patch")
			opClient, err := operatorclient.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred())

			networkPatch := []byte(`{"spec": {"deployKubeProxy": false}}`)
			_, err = opClient.OperatorV1().Networks().Patch(
				ctx, "cluster", types.MergePatchType, networkPatch, metav1.PatchOptions{},
			)
			Expect(err).NotTo(HaveOccurred())
			GinkgoLogr.Info("Disabled kube-proxy via network operator patch")

			By("installing Cilium via helm SDK")
			kubeconfigContent, err := framework.GenerateKubeconfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred())
			ciliumValues := map[string]any{
				"cni": map[string]any{
					"uninstall": false,
					"binPath":   "/var/lib/cni/bin",
					"confPath":  "/var/run/multus/cni/net.d",
				},
				"kubeProxyReplacement": true,
				"k8sServiceHost":       "172.20.0.1",
				"k8sServicePort":       6443,
				"ipam": map[string]any{
					"mode": "cluster-pool",
					"operator": map[string]any{
						"clusterPoolIPv4PodCIDRList": "10.255.0.0/16",
						"clusterPoolIPv4MaskSize":    23,
					},
				},
				"cluster": map[string]any{
					"name": customerClusterName,
				},
				"operator": map[string]any{
					"replicas": 1,
				},
				"routingMode":    "tunnel",
				"tunnelProtocol": "vxlan",
			}
			err = framework.InstallCiliumChart(ctx, "1.19.2", ciliumValues, kubeconfigContent, "kube-system")
			Expect(err).NotTo(HaveOccurred())

			By("creating the node pool via v20251223preview")
			nodePoolParams := framework.NewDefaultNodePoolParams()
			nodePool := hcpsdk20251223preview.NodePool{
				Location: to.Ptr(tc.Location()),
				Properties: &hcpsdk20251223preview.NodePoolProperties{
					Version: &hcpsdk20251223preview.NodePoolVersionProfile{
						ID:           to.Ptr(nodePoolParams.OpenshiftVersionId),
						ChannelGroup: to.Ptr(nodePoolParams.ChannelGroup),
					},
					Replicas: to.Ptr(int32(2)),
					Platform: &hcpsdk20251223preview.NodePoolPlatformProfile{
						VMSize: to.Ptr(nodePoolParams.VMSize),
						OSDisk: &hcpsdk20251223preview.OsDiskProfile{
							SizeGiB:                to.Ptr(nodePoolParams.OSDiskSizeGiB),
							DiskStorageAccountType: to.Ptr(hcpsdk20251223preview.DiskStorageAccountType(nodePoolParams.DiskStorageAccountType)),
						},
					},
					AutoRepair: to.Ptr(true),
				},
			}

			_, nodePoolErr := framework.CreateNodePoolAndWait20251223(
				ctx,
				tc.Get20251223ClientFactoryOrDie(ctx).NewNodePoolsClient(),
				*resourceGroup.Name,
				customerClusterName,
				customerNodePoolName,
				nodePool,
				45*time.Minute,
			)
			// We delay checking the error on purpose to get more details
			// about the issue by running the verifiers.

			By("verifying nodes become Ready with Cilium CNI")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig, verifiers.VerifyNodesReady(), verifiers.VerifyCiliumOperational("kube-system", "k8s-app=cilium"))
			Expect(errors.Join(err, nodePoolErr)).NotTo(HaveOccurred())

			By("verifying a simple web app can run with cilium")
			err = verifiers.VerifySimpleWebApp().Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred())
		},
	)
})
