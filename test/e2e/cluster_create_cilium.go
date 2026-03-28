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
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	operatorclient "github.com/openshift/client-go/operator/clientset/versioned"
	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart/v2/loader"
	"helm.sh/helm/v4/pkg/cli"
	"helm.sh/helm/v4/pkg/kube"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/utils/ptr"

	hcpsdk20251223preview "github.com/Azure/ARO-HCP/test/sdk/v20251223preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Create HCPOpenShiftCluster with Cilium CNI", func() {
	It("should create a no-CNI cluster with private etcd and install Cilium",
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

			// Adding timebomb for validating permissions as we should have permissions
			// in our built in role by this date.
			permissionsDeadline := mustParseDate("2026-04-03")

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "e2e-cilium", tc.Location())
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
			if time.Now().Before(permissionsDeadline) {
				By("validating SMI identity has correct service managed identity role binding")
				// Parse the SMI resource ID to get the identity name and resource group
				// This avoids double-leasing in pooled mode and works for both pooled and non-pooled
				Expect(clusterParams.UserAssignedIdentitiesProfile).NotTo(BeNil())
				Expect(clusterParams.UserAssignedIdentitiesProfile.ServiceManagedIdentity).NotTo(BeNil())

				smiResourceID, err := azcorearm.ParseResourceID(*clusterParams.UserAssignedIdentitiesProfile.ServiceManagedIdentity)
				Expect(err).NotTo(HaveOccurred())

				// Extract identity name from the parsed resource ID
				smiIdentityName := smiResourceID.Name
				smiIdentityResourceGroup := smiResourceID.ResourceGroupName

				// Validate the SMI Identity
				// TODO: Remove this once we have updated rolebinding
				// we should no longer see tests not adding permissions
				err = tc.EnsureIdentityRoleAssignments(ctx, framework.ServiceManagedIdentityName, smiIdentityName, smiIdentityResourceGroup)
				Expect(err).NotTo(HaveOccurred())
			}

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
			kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred())

			opClient, err := operatorclient.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred())

			networkPatch := []byte(`{"spec": {"deployKubeProxy": false}}`)
			_, err = opClient.OperatorV1().Networks().Patch(
				ctx, "cluster", types.MergePatchType, networkPatch, metav1.PatchOptions{},
			)
			Expect(err).NotTo(HaveOccurred())
			GinkgoLogr.Info("Disabled kube-proxy via network operator patch")

			By("installing Cilium via helm SDK")
			kubeconfigPath, err := writeKubeconfigFromRESTConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred())
			defer os.Remove(kubeconfigPath)

			err = installCiliumChart(ctx, kubeconfigPath, customerClusterName)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for Cilium pods to be running")
			Eventually(func() error {
				pods, err := kubeClient.CoreV1().Pods("kube-system").List(ctx, metav1.ListOptions{
					LabelSelector: "k8s-app=cilium",
				})
				if err != nil {
					return fmt.Errorf("failed to list cilium pods: %w", err)
				}
				if len(pods.Items) == 0 {
					return fmt.Errorf("no cilium pods found")
				}
				for _, pod := range pods.Items {
					if pod.Status.Phase != "Running" {
						return fmt.Errorf("cilium pod %s is in phase %s", pod.Name, pod.Status.Phase)
					}
				}
				return nil
			}, 10*time.Minute, 30*time.Second).Should(Succeed(), "cilium pods should be running")

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

			_, err = framework.CreateNodePoolAndWait20251223(
				ctx,
				tc.Get20251223ClientFactoryOrDie(ctx).NewNodePoolsClient(),
				*resourceGroup.Name,
				customerClusterName,
				customerNodePoolName,
				nodePool,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("verifying nodes become Ready with Cilium CNI")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig, verifiers.VerifyNodesReady())
			Expect(err).NotTo(HaveOccurred())

			By("creating a test pod that logs a known message")
			const (
				testNamespace = "default"
				testPodName   = "cilium-log-test"
				testMessage   = "cilium-e2e-smoke-test-ok"
			)
			testPod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testPodName,
					Namespace: testNamespace,
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:    "logger",
							Image:   "registry.access.redhat.com/ubi9-micro:latest",
							Command: []string{"sh", "-c", fmt.Sprintf("echo '%s' && sleep 300", testMessage)},
						},
					},
				},
			}
			_, err = kubeClient.CoreV1().Pods(testNamespace).Create(ctx, testPod, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the test pod to be running and verifying its logs")
			Eventually(func() error {
				pod, err := kubeClient.CoreV1().Pods(testNamespace).Get(ctx, testPodName, metav1.GetOptions{})
				if err != nil {
					return fmt.Errorf("failed to get test pod: %w", err)
				}
				if pod.Status.Phase != corev1.PodRunning {
					return fmt.Errorf("test pod is in phase %s, waiting for Running", pod.Status.Phase)
				}

				logStream, err := kubeClient.CoreV1().Pods(testNamespace).GetLogs(testPodName, &corev1.PodLogOptions{}).Stream(ctx)
				if err != nil {
					return fmt.Errorf("failed to get pod logs: %w", err)
				}
				defer logStream.Close()

				logBytes, err := io.ReadAll(logStream)
				if err != nil {
					return fmt.Errorf("failed to read pod logs: %w", err)
				}

				if !strings.Contains(string(logBytes), testMessage) {
					return fmt.Errorf("expected log message %q not found in pod logs: %s", testMessage, string(logBytes))
				}
				return nil
			}, 5*time.Minute, 15*time.Second).Should(Succeed(), "test pod should be running and log the expected message")

			GinkgoLogr.Info("Cluster with Cilium CNI and private etcd created and verified successfully",
				"clusterName", customerClusterName)
		},
	)
})

// installCiliumChart installs the Cilium helm chart using the helm Go SDK.
func installCiliumChart(ctx context.Context, kubeconfigPath, clusterName string) error {
	const (
		releaseName      = "cilium"
		releaseNamespace = "kube-system"
		ciliumRepoURL    = "https://helm.cilium.io/"
		chartName        = "cilium"
	)

	// Initialize helm action configuration with the kubeconfig
	actionCfg := &action.Configuration{}
	cliOpts := &genericclioptions.ConfigFlags{
		KubeConfig: ptr.To(kubeconfigPath),
		Namespace:  ptr.To(releaseNamespace),
	}
	if err := actionCfg.Init(cliOpts, releaseNamespace, ""); err != nil {
		return fmt.Errorf("failed to init helm action config: %w", err)
	}

	// Locate and download the chart from the Cilium repo
	installClient := action.NewInstall(actionCfg)
	installClient.ReleaseName = releaseName
	installClient.Namespace = releaseNamespace
	installClient.WaitStrategy = kube.StatusWatcherStrategy
	installClient.WaitForJobs = true
	installClient.Timeout = 10 * time.Minute
	installClient.ChartPathOptions.RepoURL = ciliumRepoURL

	settings := cli.New()
	chartPath, err := installClient.ChartPathOptions.LocateChart(chartName, settings)
	if err != nil {
		return fmt.Errorf("failed to locate cilium chart: %w", err)
	}

	chart, err := loader.Load(chartPath)
	if err != nil {
		return fmt.Errorf("failed to load cilium chart: %w", err)
	}

	values := map[string]any{
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
				"clusterPoolIPv4PodCIDRList": "10.132.0.0/14",
				"clusterPoolIPv4MaskSize":    23,
			},
		},
		"cluster": map[string]any{
			"name": clusterName,
		},
		"operator": map[string]any{
			"replicas": 1,
		},
		"routingMode":    "tunnel",
		"tunnelProtocol": "vxlan",
	}

	_, err = installClient.RunWithContext(ctx, chart, values)
	if err != nil {
		return fmt.Errorf("failed to install cilium chart: %w", err)
	}

	return nil
}

// writeKubeconfigFromRESTConfig creates a temporary kubeconfig file from a rest.Config
// for use with the helm SDK which requires a kubeconfig file path.
func writeKubeconfigFromRESTConfig(cfg *rest.Config) (string, error) {
	kubeconfig := clientcmdapi.NewConfig()

	cluster := clientcmdapi.NewCluster()
	cluster.Server = cfg.Host
	if len(cfg.CAData) > 0 {
		cluster.CertificateAuthorityData = cfg.CAData
	}
	if cfg.Insecure {
		cluster.InsecureSkipTLSVerify = true
	}
	kubeconfig.Clusters["default"] = cluster

	authInfo := clientcmdapi.NewAuthInfo()
	if cfg.BearerToken != "" {
		authInfo.Token = cfg.BearerToken
	}
	if len(cfg.CertData) > 0 {
		authInfo.ClientCertificateData = cfg.CertData
	}
	if len(cfg.KeyData) > 0 {
		authInfo.ClientKeyData = cfg.KeyData
	}
	if cfg.Username != "" {
		authInfo.Username = cfg.Username
		authInfo.Password = cfg.Password
	}
	kubeconfig.AuthInfos["default"] = authInfo

	kubeCtx := clientcmdapi.NewContext()
	kubeCtx.Cluster = "default"
	kubeCtx.AuthInfo = "default"
	kubeconfig.Contexts["default"] = kubeCtx
	kubeconfig.CurrentContext = "default"

	tmpFile, err := os.CreateTemp("", "kubeconfig-*.yaml")
	if err != nil {
		return "", fmt.Errorf("failed to create temp kubeconfig: %w", err)
	}

	content, err := clientcmd.Write(*kubeconfig)
	if err != nil {
		os.Remove(tmpFile.Name())
		return "", fmt.Errorf("failed to serialize kubeconfig: %w", err)
	}

	if _, err := tmpFile.Write(content); err != nil {
		os.Remove(tmpFile.Name())
		return "", fmt.Errorf("failed to write kubeconfig: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpFile.Name())
		return "", fmt.Errorf("failed to close kubeconfig: %w", err)
	}

	return tmpFile.Name(), nil
}
