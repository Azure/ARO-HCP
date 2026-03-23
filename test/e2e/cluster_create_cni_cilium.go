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
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Customer", func() {
	It("should be able to create a HCP cluster and use Cilium CNI plugin",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			const (
				customerClusterName    = "cilium-cl"
				customerNodePoolName   = "cilium-np"
				pullSecretName         = "additional-pull-secret"
				pullSecretNamespace    = "kube-system"
				redhatRegistryHost     = "registry.redhat.io"
				pullSecretMergeTimeout = 10 * time.Minute
				verifierPollInterval   = 15 * time.Second
			)
			tc := framework.NewTestContext()

			By("reading pull-secret file from aro-hcp-qe-pull-secret directory")
			// By reading the pull secret file here, we are checking that the
			// Red Hat pull secret (as provided by OpensHift CI) is available
			// early, so that the test can immeditally fail without wasting
			// cloud resources or CI runtime.
			pullSecretFilePath := filepath.Join(tc.PullSecretPath(), "pull-secret")
			pullSecretFileData, err := os.ReadFile(pullSecretFilePath)
			Expect(err).NotTo(HaveOccurred(), "failed to read pull-secret file from %s", pullSecretFilePath)

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "cni-cilium", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName
			By("setting no cni network configuration")
			clusterParams.Network.NetworkType = "Other"
			clusterParams.Network.PodCIDR = "10.128.0.0/14"
			clusterParams.Network.ServiceCIDR = "172.30.0.0/16"
			clusterParams.Network.MachineCIDR = "10.0.0.0/16"
			clusterParams.Network.HostPrefix = 23

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources(ctx,
				resourceGroup,
				clusterParams,
				map[string]any{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating HCP cluster without CNI")
			err = tc.CreateHCPClusterFromParam(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("getting credentials and verifying cluster is available")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				10*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(verifiers.VerifyHCPCluster(ctx, adminRESTConfig)).To(Succeed())

			By("parsing pull-secret file")
			var pullSecretConfig framework.DockerConfigJSON
			err = json.Unmarshal(pullSecretFileData, &pullSecretConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to parse pull-secret file")

			// TODO; the pull secret handling code should be simplified and
			// moved into a framework for reuse (with unit tests and godoc)
			// this is a hack

			By("extracting registry.redhat.io credentials")
			redhatRegistryAuth, ok := pullSecretConfig.Auths[redhatRegistryHost]
			Expect(ok).To(BeTrue(), "registry.redhat.io credentials not found in pull-secret file")

			By("decoding credentials for red hat registry")
			decodedAuth, err := base64.StdEncoding.DecodeString(redhatRegistryAuth.Auth)
			Expect(err).NotTo(HaveOccurred())
			// Format is "username:password"
			credentials := strings.SplitN(string(decodedAuth), ":", 2)
			username := ""
			password := ""
			if len(credentials) == 2 {
				username = credentials[0]
				password = credentials[1]
			}

			By("creating Red Hat pull secret")
			rhPullSecret, err := framework.CreateTestDockerConfigSecret(
				redhatRegistryHost,
				username,
				password,
				redhatRegistryAuth.Email,
				pullSecretName,
				pullSecretNamespace,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating the Red Hat pull secret in the cluster")
			kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred())
			_, err = kubeClient.CoreV1().Secrets(pullSecretNamespace).Create(ctx, rhPullSecret, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("waiting for HCCO to merge the Red Hat pull secret into global pull secret")
			var previousError string
			Eventually(func() error {
				err := verifiers.VerifyPullSecretMergedIntoGlobal(redhatRegistryHost).Verify(ctx, adminRESTConfig)
				if err != nil {
					currentError := err.Error()
					if currentError != previousError {
						GinkgoLogr.Info("Verifier check", "name", "VerifyPullSecretMergedIntoGlobal", "status", "failed", "error", currentError)
						previousError = currentError
					}
				}
				return err
			}, pullSecretMergeTimeout, verifierPollInterval).Should(Succeed(), "registry.redhat.io pull secret should be merged into global-pull-secret by HCCO")

			By("deploying cilium artefacts and a config")
			err = verifiers.VerifyCiliumSetup(
				"1.15.1",
				clusterParams.Network.PodCIDR,
				clusterParams.Network.HostPrefix).Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred())

			By("creating the node pool")
			nodePoolParams := framework.NewDefaultNodePoolParams()
			nodePoolParams.NodePoolName = customerNodePoolName
			err = tc.CreateNodePoolFromParam(ctx,
				*resourceGroup.Name,
				customerClusterName,
				nodePoolParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("checking nodes are in Ready state")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig, verifiers.VerifyNodesReady())
			Expect(err).NotTo(HaveOccurred())

			By("verifying a simple web app can run with cilium")
			err = verifiers.VerifySimpleWebApp().Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred())
		},
	)
})
