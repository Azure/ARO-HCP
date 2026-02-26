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
	"fmt"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Customer", func() {
	BeforeEach(func() {
		// per test initialization
	})

	It("should be able to create an HCP cluster and manage pull secrets",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		labels.AroRpApiCompatible,
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

			By("checking pull secret file exists")
			pullSecretFilePath := filepath.Join(tc.PullSecretPath(), "pull-secret")
			if _, err := os.Stat(pullSecretFilePath); os.IsNotExist(err) {
				Skip(fmt.Sprintf("Pull secret file not found at %s, skipping test", pullSecretFilePath))
			}

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "pullsecret-test", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred())

			By("Creating the cluster")
			err = tc.CreateHCPClusterFromParam(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())
			By("Creating the node pool")
			nodePoolParams := framework.NewDefaultNodePoolParams()
			nodePoolParams.NodePoolName = "np-1"
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.Replicas = int32(2)
			err = tc.CreateNodePoolFromParam(ctx,
				*resourceGroup.Name,
				customerClusterName,
				nodePoolParams,
				15*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("getting credentials")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster(
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
			verifier := verifiers.VerifyPullSecretMergedIntoGlobal(testPullSecretHost)
			Eventually(func() error {
				err := verifier.Verify(ctx, adminRESTConfig)
				if err != nil {
					GinkgoLogr.Info("Verifier check", "name", verifier.Name(), "status", "failed", "error", err.Error())
				}
				return err
			}, 5*time.Minute, 15*time.Second).Should(Succeed(), "additional pull secret should be merged into global-pull-secret by HCCO")

			By("verifying the DaemonSet for global pull secret synchronization is created")
			verifier = verifiers.VerifyGlobalPullSecretSyncer()
			Eventually(func() error {
				err := verifier.Verify(ctx, adminRESTConfig)
				if err != nil {
					GinkgoLogr.Info("Verifier check", "name", verifier.Name(), "status", "failed", "error", err.Error())
				}
				return err
			}, 1*time.Minute, 10*time.Second).Should(Succeed(), "global-pull-secret-syncer DaemonSet should be created")

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
			pullSecretFileData, err := os.ReadFile(pullSecretFilePath)
			Expect(err).NotTo(HaveOccurred(), "failed to read pull-secret file from %s", pullSecretFilePath)

			By("parsing pull-secret file")
			var pullSecretConfig framework.DockerConfigJSON
			err = json.Unmarshal(pullSecretFileData, &pullSecretConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to parse pull-secret file")

			By("extracting registry.redhat.io credentials")
			const redhatRegistryHost = "registry.redhat.io"
			redhatRegistryAuth, ok := pullSecretConfig.Auths[redhatRegistryHost]
			Expect(ok).To(BeTrue(), "registry.redhat.io credentials not found in pull-secret file")

			redhatRegistryAuthString := redhatRegistryAuth.Auth
			redhatRegistryEmail := redhatRegistryAuth.Email

			By("updating additional-pull-secret to add registry.redhat.io credentials")
			// Get the current additional-pull-secret
			currentSecret, err := kubeClient.CoreV1().Secrets(pullSecretNamespace).Get(ctx, pullSecretName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to get existing additional-pull-secret")

			// Parse the current dockerconfigjson
			var currentConfig framework.DockerConfigJSON
			err = json.Unmarshal(currentSecret.Data[corev1.DockerConfigJsonKey], &currentConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to parse current pull secret")

			// Add registry.redhat.io credentials to the existing auths
			currentConfig.Auths[redhatRegistryHost] = framework.RegistryAuth{
				Auth:  redhatRegistryAuthString,
				Email: redhatRegistryEmail,
			}

			// Marshal back to JSON
			updatedDockerConfigJSON, err := json.Marshal(currentConfig)
			Expect(err).NotTo(HaveOccurred())

			// Update the secret
			currentSecret.Data[corev1.DockerConfigJsonKey] = updatedDockerConfigJSON
			_, err = kubeClient.CoreV1().Secrets(pullSecretNamespace).Update(ctx, currentSecret, metav1.UpdateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("waiting for HCCO to merge the updated pull secret (with registry.redhat.io) into global pull secret")
			verifier = verifiers.VerifyPullSecretMergedIntoGlobal(redhatRegistryHost)
			Eventually(func() error {
				err := verifier.Verify(ctx, adminRESTConfig)
				if err != nil {
					GinkgoLogr.Info("Verifier check", "name", verifier.Name(), "status", "failed", "error", err.Error())
				}
				return err
			}, 5*time.Minute, 15*time.Second).Should(Succeed(), "registry.redhat.io pull secret should be merged into global-pull-secret by HCCO")

			By("verifying both test registries are now in the global pull secret")
			err = verifiers.VerifyPullSecretMergedIntoGlobal(testPullSecretHost).Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "host.example.com should still be in global-pull-secret")

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

			By("creating Subscription for NFD operator from redhat-operators catalog")
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
						"source":              "redhat-operators",
						"sourceNamespace":     "openshift-marketplace",
						"installPlanApproval": "Automatic",
					},
				},
			}
			_, err = dynamicClient.Resource(subscriptionGVR).Namespace(nfdNamespace).Create(ctx, subscription, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("waiting for NFD operator to be installed")
			verifier = verifiers.VerifyOperatorInstalled(nfdNamespace, "nfd")
			Eventually(func() error {
				err := verifier.Verify(ctx, adminRESTConfig)
				if err != nil {
					GinkgoLogr.Info("Verifier check", "name", verifier.Name(), "status", "failed", "error", err.Error())
				}
				return err
			}, 5*time.Minute, 15*time.Second).Should(Succeed(), "NFD operator should be installed successfully")

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

			By("waiting for NFD worker DaemonSet to be created")
			Eventually(func() error {
				daemonSets, err := kubeClient.AppsV1().DaemonSets(nfdNamespace).List(ctx, metav1.ListOptions{})
				if err != nil {
					return err
				}
				for _, ds := range daemonSets.Items {
					if ds.Name == "nfd-worker" {
						if ds.Status.DesiredNumberScheduled > 0 && ds.Status.NumberReady > 0 {
							return nil
						}
						return fmt.Errorf("nfd-worker DaemonSet found but not ready: desired=%d, ready=%d",
							ds.Status.DesiredNumberScheduled, ds.Status.NumberReady)
					}
				}
				return fmt.Errorf("nfd-worker DaemonSet not found")
			}, 5*time.Minute, 15*time.Second).Should(Succeed(), "NFD worker DaemonSet should be created and have ready pods")

			By("waiting for NFD worker pods to be created and verify images from registry.redhat.io can be pulled")
			verifier = verifiers.VerifyImagePulled(nfdNamespace, "registry.redhat.io", "ose-node-feature-discovery")
			Eventually(func() error {
				err := verifier.Verify(ctx, adminRESTConfig)
				if err != nil {
					GinkgoLogr.Info("Verifier check", "name", verifier.Name(), "status", "failed", "error", err.Error())
				}
				return err
			}, 5*time.Minute, 15*time.Second).Should(Succeed(), "NFD worker images from registry.redhat.io should be pulled successfully with the added pull secret")
		})
})
