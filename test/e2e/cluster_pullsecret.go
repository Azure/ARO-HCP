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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Cluster Pull Secret Management", func() {
	BeforeEach(func() {
		// per test initialization
	})

	It("should be able to create an HCP cluster and manage pull secrets",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		func(ctx context.Context) {
			const (
				customerClusterName    = "pullsecret-hcp-cluster"
				testPullSecretHost     = "host.example.com"
				testPullSecretPassword = "my_password"
				testPullSecretEmail    = "noreply@example.com"
				pullSecretName         = "additional-pull-secret"
				pullSecretNamespace    = "kube-system"
			)
			tc := framework.NewTestContext()

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "pullsecret-test", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("deploying the demo cluster bicep template")
			_, err = framework.CreateBicepTemplateAndWait(ctx,
				tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				*resourceGroup.Name,
				"pull-secret-cluster",
				framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/demo.json")),
				map[string]interface{}{
					"persistTagValue": false,
					"clusterName":     customerClusterName,
				},
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("getting credentials")
			adminRESTConfig, err := framework.GetAdminRESTConfigForHCPCluster(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				10*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("ensuring the cluster is viable")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred())

			By("creating kubernetes client")
			kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred())

			By("creating test pull secret")
			username := "test-user"
			auth := base64.StdEncoding.EncodeToString([]byte(username + ":" + testPullSecretPassword))

			testPullSecret, err := framework.CreateTestDockerConfigSecret(
				testPullSecretHost,
				username,
				testPullSecretPassword,
				testPullSecretEmail,
				pullSecretName,
				pullSecretNamespace,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating the test pull secret in the cluster")
			_, err = kubeClient.CoreV1().Secrets(pullSecretNamespace).Create(ctx, testPullSecret, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("waiting for HCCO to merge the additional pull secret with the global pull secret")
			Eventually(func() error {
				return verifiers.VerifyPullSecretMergedIntoGlobal(testPullSecretHost).Verify(ctx, adminRESTConfig)
			}, 300*time.Second, 2*time.Second).Should(Succeed(), "additional pull secret should be merged into global-pull-secret by HCCO")

			By("verifying the DaemonSet for global pull secret synchronization is created")
			Eventually(func() error {
				return verifiers.VerifyGlobalPullSecretSyncer().Verify(ctx, adminRESTConfig)
			}, 60*time.Second, 2*time.Second).Should(Succeed(), "global-pull-secret-syncer DaemonSet should be created")

			By("verifying the pull secret was merged into the global pull secret")
			err = verifiers.VerifyPullSecretAuthData(
				"global-pull-secret",
				pullSecretNamespace,
				testPullSecretHost,
				auth,
				testPullSecretEmail,
			).Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred())

			By("checking for CLUSTER_PROFILE_DIR environment variable")
			clusterProfileDir := os.Getenv("CLUSTER_PROFILE_DIR")
			Expect(clusterProfileDir).NotTo(BeEmpty(), "CLUSTER_PROFILE_DIR environment variable is not set")

			By("reading pull-secret file from cluster profile directory")
			pullSecretFilePath := filepath.Join(clusterProfileDir, "pull-secret")
			pullSecretFileData, err := os.ReadFile(pullSecretFilePath)
			Expect(err).NotTo(HaveOccurred(), "failed to read pull-secret file from %s", pullSecretFilePath)

			By("parsing pull-secret file")
			var pullSecretConfig map[string]interface{}
			err = json.Unmarshal(pullSecretFileData, &pullSecretConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to parse pull-secret file")

			By("extracting registry.redhat.io credentials")
			auths, ok := pullSecretConfig["auths"].(map[string]interface{})
			Expect(ok).To(BeTrue(), "auths field not found in pull-secret file")

			const redhatRegistryHost = "registry.redhat.io"
			redhatRegistryAuth, ok := auths[redhatRegistryHost].(map[string]interface{})
			Expect(ok).To(BeTrue(), "registry.redhat.io credentials not found in pull-secret file")

			redhatRegistryAuthString, ok := redhatRegistryAuth["auth"].(string)
			Expect(ok).To(BeTrue(), "auth field not found in registry.redhat.io credentials")

			redhatRegistryEmail := ""
			if email, ok := redhatRegistryAuth["email"].(string); ok {
				redhatRegistryEmail = email
			}

			By("creating registry.redhat.io pull secret in the cluster")
			decodedAuth, err := base64.StdEncoding.DecodeString(redhatRegistryAuthString)
			Expect(err).NotTo(HaveOccurred(), "failed to decode registry.redhat.io auth string")

			// Extract username and password from decoded auth
			var redhatRegistryUsername, redhatRegistryPassword string
			for i := 0; i < len(decodedAuth); i++ {
				if decodedAuth[i] == ':' {
					redhatRegistryUsername = string(decodedAuth[:i])
					redhatRegistryPassword = string(decodedAuth[i+1:])
					break
				}
			}

			const redhatRegistryPullSecretName = "redhat-registry-io-pull-secret"
			redhatRegistryPullSecret, err := framework.CreateTestDockerConfigSecret(
				redhatRegistryHost,
				redhatRegistryUsername,
				redhatRegistryPassword,
				redhatRegistryEmail,
				redhatRegistryPullSecretName,
				pullSecretNamespace,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating the registry.redhat.io pull secret in the cluster")
			_, err = kubeClient.CoreV1().Secrets(pullSecretNamespace).Create(ctx, redhatRegistryPullSecret, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("waiting for HCCO to merge the registry.redhat.io pull secret with the global pull secret")
			Eventually(func() error {
				return verifiers.VerifyPullSecretMergedIntoGlobal(redhatRegistryHost).Verify(ctx, adminRESTConfig)
			}, 300*time.Second, 2*time.Second).Should(Succeed(), "registry.redhat.io pull secret should be merged into global-pull-secret by HCCO")

			By("verifying the registry.redhat.io pull secret was merged into the global pull secret")
			err = verifiers.VerifyPullSecretAuthData(
				"global-pull-secret",
				pullSecretNamespace,
				redhatRegistryHost,
				redhatRegistryAuthString,
				redhatRegistryEmail,
			).Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred())
		})
})
