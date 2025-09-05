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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"maps"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

type dockerConfig struct {
	Auths map[string]dockerConfigAuth `json:"auths"`
}

type dockerConfigAuth struct {
	Email string `json:"email"`
	Auth  string `json:"auth"`
}

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

			testDockerConfig := dockerConfig{
				Auths: map[string]dockerConfigAuth{
					testPullSecretHost: {
						Email: testPullSecretEmail,
						Auth:  auth,
					},
				},
			}

			testDockerConfigJSON, err := json.Marshal(testDockerConfig)
			Expect(err).NotTo(HaveOccurred())

			testPullSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pull-secret",
					Namespace: "openshift-config",
				},
				Type: corev1.SecretTypeDockerConfigJson,
				Data: map[string][]byte{
					corev1.DockerConfigJsonKey: testDockerConfigJSON,
				},
			}

			By("creating the test pull secret in the cluster")
			_, err = kubeClient.CoreV1().Secrets("openshift-config").Create(ctx, testPullSecret, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("getting the current global pull secret")
			globalPullSecret, err := kubeClient.CoreV1().Secrets("openshift-config").Get(ctx, "pull-secret", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("safely merging test pull secret with existing global pull secret using raw JSON")
			// Parse existing secret as raw JSON to preserve all fields
			var existingConfig map[string]interface{}
			err = json.Unmarshal(globalPullSecret.Data[corev1.DockerConfigJsonKey], &existingConfig)
			Expect(err).NotTo(HaveOccurred())

			// Ensure auths section exists
			if existingConfig["auths"] == nil {
				existingConfig["auths"] = make(map[string]interface{})
			}

			existingAuths, ok := existingConfig["auths"].(map[string]interface{})
			Expect(ok).To(BeTrue(), "auths field should be a map")

			// Log the merge operation details
			GinkgoWriter.Printf("=== MERGE OPERATION DEBUG ===\n")
			GinkgoWriter.Printf("Existing hosts before merge: %v\n", maps.Keys(existingAuths))
			GinkgoWriter.Printf("Adding new host: %s\n", testPullSecretHost)

			// Add only our test entry without touching existing ones
			existingAuths[testPullSecretHost] = map[string]interface{}{
				"email": testPullSecretEmail,
				"auth":  auth,
			}

			GinkgoWriter.Printf("Hosts after merge: %v\n", maps.Keys(existingAuths))
			GinkgoWriter.Printf("Total auth entries: %d\n", len(existingAuths))

			By("updating the global pull secret")
			updatedDockerConfigJSON, err := json.Marshal(existingConfig)
			Expect(err).NotTo(HaveOccurred())

			// Log the update operation
			GinkgoWriter.Printf("=== UPDATE OPERATION DEBUG ===\n")
			GinkgoWriter.Printf("JSON size before update: %d bytes\n", len(globalPullSecret.Data[corev1.DockerConfigJsonKey]))
			GinkgoWriter.Printf("JSON size after merge: %d bytes\n", len(updatedDockerConfigJSON))
			GinkgoWriter.Printf("Updating secret with %d hosts\n", len(existingAuths))

			globalPullSecret.Data[corev1.DockerConfigJsonKey] = updatedDockerConfigJSON
			_, err = kubeClient.CoreV1().Secrets("openshift-config").Update(ctx, globalPullSecret, metav1.UpdateOptions{})
			if err != nil {
				GinkgoWriter.Printf("UPDATE FAILED: %v\n", err)
				// Log the current secret state for debugging
				currentSecret, _ := kubeClient.CoreV1().Secrets("openshift-config").Get(ctx, "pull-secret", metav1.GetOptions{})
				GinkgoWriter.Printf("Current secret resource version: %s\n", currentSecret.ResourceVersion)
				GinkgoWriter.Printf("Current secret data size: %d bytes\n", len(currentSecret.Data[corev1.DockerConfigJsonKey]))
			}
			Expect(err).NotTo(HaveOccurred())

			GinkgoWriter.Printf("Update operation completed successfully\n")

			By("waiting for pull secret to be stable after operator reconciliation")
			Eventually(func() bool {
				verifySecret, err := kubeClient.CoreV1().Secrets("openshift-config").Get(ctx, "pull-secret", metav1.GetOptions{})
				if err != nil {
					return false
				}
				
				var verifyConfig map[string]interface{}
				if err := json.Unmarshal(verifySecret.Data[corev1.DockerConfigJsonKey], &verifyConfig); err != nil {
					return false
				}
				
				verifyAuths, ok := verifyConfig["auths"].(map[string]interface{})
				if !ok {
					return false
				}
				
				_, exists := verifyAuths[testPullSecretHost]
				return exists
			}, 30*time.Second, 2*time.Second).Should(BeTrue(), "test pull secret should persist through operator reconciliation")

			By("verifying the pull secret was added to the global pull secret")
			updatedGlobalPullSecret, err := kubeClient.CoreV1().Secrets("openshift-config").Get(ctx, "pull-secret", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())

			// Log verification details
			GinkgoWriter.Printf("=== VERIFICATION DEBUG ===\n")
			GinkgoWriter.Printf("Retrieved secret resource version: %s\n", updatedGlobalPullSecret.ResourceVersion)
			GinkgoWriter.Printf("Secret data size: %d bytes\n", len(updatedGlobalPullSecret.Data[corev1.DockerConfigJsonKey]))

			// Verify using raw JSON to avoid struct limitations
			var verifyConfig map[string]interface{}
			err = json.Unmarshal(updatedGlobalPullSecret.Data[corev1.DockerConfigJsonKey], &verifyConfig)
			Expect(err).NotTo(HaveOccurred())

			verifyAuths, ok := verifyConfig["auths"].(map[string]interface{})
			Expect(ok).To(BeTrue(), "auths field should be a map")

			GinkgoWriter.Printf("Parsed hosts in updated secret: %v\n", maps.Keys(verifyAuths))
			GinkgoWriter.Printf("Expected host: %s\n", testPullSecretHost)
			GinkgoWriter.Printf("Host present: %t\n", func() bool { _, ok := verifyAuths[testPullSecretHost]; return ok }())

			By("checking that the test pull secret is present in the global pull secret")
			Expect(verifyAuths).To(HaveKey(testPullSecretHost))

			testEntry, ok := verifyAuths[testPullSecretHost].(map[string]interface{})
			Expect(ok).To(BeTrue(), "test entry should be a map")
			Expect(testEntry["email"]).To(Equal(testPullSecretEmail))
			Expect(testEntry["auth"]).To(Equal(auth))
		})
})
