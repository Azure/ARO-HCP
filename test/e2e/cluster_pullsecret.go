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

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
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

			By("reading pull-secret file from aro-hcp-qe-pull-secret directory")
			pullSecretFilePath := filepath.Join("/var/run/aro-hcp-qe-pull-secret", "pull-secret")
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

			By("creating dynamic client for operator installation")
			dynamicClient, err := dynamic.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred())

			By("creating namespace for NFD operator")
			const nfdNamespace = "openshift-nfd"
			_, err = kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: nfdNamespace,
				},
			}, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("creating OperatorGroup for NFD operator")
			operatorGroupGVR := schema.GroupVersionResource{
				Group:    "operators.coreos.com",
				Version:  "v1",
				Resource: "operatorgroups",
			}
			operatorGroup := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "operators.coreos.com/v1",
					"kind":       "OperatorGroup",
					"metadata": map[string]interface{}{
						"name":      "nfd-operator-group",
						"namespace": nfdNamespace,
					},
					"spec": map[string]interface{}{
						"targetNamespaces": []interface{}{nfdNamespace},
					},
				},
			}
			_, err = dynamicClient.Resource(operatorGroupGVR).Namespace(nfdNamespace).Create(ctx, operatorGroup, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("creating Subscription for NFD operator from certified-operators catalog")
			subscriptionGVR := schema.GroupVersionResource{
				Group:    "operators.coreos.com",
				Version:  "v1alpha1",
				Resource: "subscriptions",
			}
			subscription := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "operators.coreos.com/v1alpha1",
					"kind":       "Subscription",
					"metadata": map[string]interface{}{
						"name":      "nfd",
						"namespace": nfdNamespace,
					},
					"spec": map[string]interface{}{
						"channel":             "stable",
						"name":                "nfd",
						"source":              "certified-operators",
						"sourceNamespace":     "openshift-marketplace",
						"installPlanApproval": "Automatic",
					},
				},
			}
			_, err = dynamicClient.Resource(subscriptionGVR).Namespace(nfdNamespace).Create(ctx, subscription, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("waiting for NFD operator to be installed")
			Eventually(func() error {
				return verifiers.VerifyOperatorInstalled(nfdNamespace, "nfd").Verify(ctx, adminRESTConfig)
			}, 300*time.Second, 5*time.Second).Should(Succeed(), "NFD operator should be installed successfully")

			By("creating NodeFeatureDiscovery CR to deploy NFD worker")
			nfdGVR := schema.GroupVersionResource{
				Group:    "nfd.openshift.io",
				Version:  "v1",
				Resource: "nodefeaturediscoveries",
			}
			nfdCR := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "nfd.openshift.io/v1",
					"kind":       "NodeFeatureDiscovery",
					"metadata": map[string]interface{}{
						"name":      "nfd-instance",
						"namespace": nfdNamespace,
					},
					"spec": map[string]interface{}{
						"operand": map[string]interface{}{
							"image": "registry.redhat.io/openshift4/ose-node-feature-discovery:latest",
						},
					},
				},
			}
			_, err = dynamicClient.Resource(nfdGVR).Namespace(nfdNamespace).Create(ctx, nfdCR, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("waiting for NFD worker DaemonSet to be ready")
			Eventually(func() error {
				return verifiers.VerifyNFDWorkerDaemonSet(nfdNamespace).Verify(ctx, adminRESTConfig)
			}, 300*time.Second, 5*time.Second).Should(Succeed(), "NFD worker DaemonSet should be ready")

			By("verifying NFD is working by checking node labels")
			Eventually(func() error {
				return verifiers.VerifyNFDNodeLabels().Verify(ctx, adminRESTConfig)
			}, 120*time.Second, 5*time.Second).Should(Succeed(), "NFD should label nodes with hardware features")
		})
})
