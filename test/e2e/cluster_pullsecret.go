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

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

type dockerConfig struct {
	Auths map[string]dockerConfigAuth `json:"auths"`
}

type dockerConfigAuth struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Email    string `json:"email"`
	Auth     string `json:"auth"`
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
			err = framework.VerifyHCPCluster(ctx, adminRESTConfig)
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
						Username: username,
						Password: testPullSecretPassword,
						Email:    testPullSecretEmail,
						Auth:     auth,
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

			By("parsing the current global pull secret")
			var currentDockerConfig dockerConfig
			err = json.Unmarshal(globalPullSecret.Data[corev1.DockerConfigJsonKey], &currentDockerConfig)
			Expect(err).NotTo(HaveOccurred())

			By("adding test pull secret to global pull secret")
			if currentDockerConfig.Auths == nil {
				currentDockerConfig.Auths = make(map[string]dockerConfigAuth)
			}
			currentDockerConfig.Auths[testPullSecretHost] = testDockerConfig.Auths[testPullSecretHost]

			By("updating the global pull secret")
			updatedDockerConfigJSON, err := json.Marshal(currentDockerConfig)
			Expect(err).NotTo(HaveOccurred())

			globalPullSecret.Data[corev1.DockerConfigJsonKey] = updatedDockerConfigJSON
			_, err = kubeClient.CoreV1().Secrets("openshift-config").Update(ctx, globalPullSecret, metav1.UpdateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("verifying the pull secret was added to the global pull secret")
			updatedGlobalPullSecret, err := kubeClient.CoreV1().Secrets("openshift-config").Get(ctx, "pull-secret", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())

			var verifyDockerConfig dockerConfig
			err = json.Unmarshal(updatedGlobalPullSecret.Data[corev1.DockerConfigJsonKey], &verifyDockerConfig)
			Expect(err).NotTo(HaveOccurred())

			By("checking that the test pull secret is present in the global pull secret")
			Expect(verifyDockerConfig.Auths).To(HaveKey(testPullSecretHost))
			Expect(verifyDockerConfig.Auths[testPullSecretHost].Username).To(Equal(username))
			Expect(verifyDockerConfig.Auths[testPullSecretHost].Password).To(Equal(testPullSecretPassword))
			Expect(verifyDockerConfig.Auths[testPullSecretHost].Email).To(Equal(testPullSecretEmail))
			Expect(verifyDockerConfig.Auths[testPullSecretHost].Auth).To(Equal(auth))
		})
})
