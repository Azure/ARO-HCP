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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Customer", func() {
	BeforeEach(func() {
		// per test initialization
	})

	// Tests the HyperShift HCCO global pull secret reconciliation flow:
	// additional-pull-secret in kube-system -> HCCO merges into global-pull-secret -> DaemonSet syncs to nodes.
	// Upstream documentation: https://hypershift.pages.dev/how-to/aws/global-pull-secret/
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
				redhatRegistryHost     = "registry.redhat.io"

				// Timeouts for verifications
				pullSecretMergeTimeout = 10 * time.Minute
				daemonSetSyncTimeout   = 10 * time.Minute // moving from 5 to 10 minutes due to observed slowness in pre-merge CI
				imagePullTimeout       = 1 * time.Minute

				// Image pull test constants
				pullTestNamespace = "pullsecret-image-test"
				pullTestImage     = "registry.redhat.io/ubi9/ubi-minimal:latest"
			)
			tc := framework.NewTestContext()

			By("checking pull secret file exists")
			pullSecretFilePath := filepath.Join(tc.PullSecretPath(), "pull-secret")
			if _, err := os.Stat(pullSecretFilePath); errors.Is(err, os.ErrNotExist) {
				Skip(fmt.Sprintf("Pull secret file not found at %s, skipping test", pullSecretFilePath))
			}

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "pullsecret-test", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for pull secret test")

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams20240610()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20240610(ctx,
				resourceGroup,
				clusterParams,
				map[string]any{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for pull secret cluster")

			By("Creating the cluster")
			err = tc.CreateHCPClusterFromParam20240610(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster for pull secret test")
			By("Creating the node pool")
			nodePoolParams := framework.NewDefaultNodePoolParams20240610()
			nodePoolParams.NodePoolName = "np-1"
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.Replicas = int32(2)
			err = tc.CreateNodePoolFromParam20240610(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				managedResourceGroupName,
				customerClusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create node pool np-1 for pull secret cluster")

			By("getting credentials")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20240610(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for pull secret cluster")

			By("ensuring the cluster is viable")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to verify HCP cluster viability")

			By("creating kubernetes client")
			kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create kubernetes client")

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
			Expect(err).NotTo(HaveOccurred(), "failed to create test docker config secret")

			// HCCO watches specifically for a secret named "additional-pull-secret" in kube-system.
			By("creating the test pull secret in the cluster")
			_, err = kubeClient.CoreV1().Secrets(pullSecretNamespace).Create(ctx, testPullSecret, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to create additional-pull-secret in kube-system namespace")

			By("waiting for HCCO to merge the additional pull secret with the global pull secret")
			err = verifiers.VerifyPullSecretMergedIntoGlobal(testPullSecretHost, pullSecretMergeTimeout).
				Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to wait for additional pull secret to be merged into global-pull-secret by HCCO")

			// The merged secret doesn't reach nodes until global-pull-secret-syncer syncs it to /var/lib/kubelet/config.json.
			By("verifying the DaemonSet for global pull secret synchronization is created")
			err = verifiers.VerifyGlobalPullSecretSyncer(daemonSetSyncTimeout).
				Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to wait for global-pull-secret-syncer DaemonSet to be created and ready")

			By("verifying the pull secret was merged into the global pull secret")
			err = verifiers.VerifyPullSecretAuthData(
				"global-pull-secret",
				pullSecretNamespace,
				testPullSecretHost,
				auth,
				testPullSecretEmail,
			).Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to verify pull secret auth data for host.example.com in global-pull-secret")

			By("reading pull-secret file from aro-hcp-qe-pull-secret directory")
			pullSecretFileData, err := os.ReadFile(pullSecretFilePath)
			Expect(err).NotTo(HaveOccurred(), "failed to read pull-secret file from %s", pullSecretFilePath)

			By("parsing pull-secret file")
			var pullSecretConfig framework.DockerConfigJSON
			err = json.Unmarshal(pullSecretFileData, &pullSecretConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to parse pull-secret file")

			By("extracting registry.redhat.io credentials")
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
			Expect(err).NotTo(HaveOccurred(), "failed to marshal updated docker config JSON with registry.redhat.io credentials")

			// Update the secret
			currentSecret.Data[corev1.DockerConfigJsonKey] = updatedDockerConfigJSON
			_, err = kubeClient.CoreV1().Secrets(pullSecretNamespace).Update(ctx, currentSecret, metav1.UpdateOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to update additional-pull-secret with registry.redhat.io credentials")

			By("waiting for HCCO to merge the updated pull secret (with registry.redhat.io) into global pull secret")
			err = verifiers.VerifyPullSecretMergedIntoGlobal(redhatRegistryHost, pullSecretMergeTimeout).
				Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to wait for registry.redhat.io pull secret to be merged into global-pull-secret by HCCO")

			By("waiting for global-pull-secret-syncer DaemonSet to sync updated secret to all nodes")
			err = verifiers.VerifyGlobalPullSecretSyncer(daemonSetSyncTimeout).
				Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to wait for global-pull-secret-syncer to sync pull secret to all nodes")

			By("verifying both test registries are now in the global pull secret")
			err = verifiers.VerifyPullSecretMergedIntoGlobal(testPullSecretHost, pullSecretMergeTimeout).Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "host.example.com should still be in global-pull-secret")

			err = verifiers.VerifyPullSecretAuthData(
				"global-pull-secret",
				pullSecretNamespace,
				redhatRegistryHost,
				redhatRegistryAuthString,
				redhatRegistryEmail,
			).Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to verify registry.redhat.io auth data in global-pull-secret")

			By("creating test namespace for image pull verification")
			_, err = kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: pullTestNamespace,
				},
			}, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to create namespace %s for image pull test", pullTestNamespace)
			DeferCleanup(func(ctx context.Context) {
				_ = kubeClient.CoreV1().Namespaces().Delete(ctx, pullTestNamespace, metav1.DeleteOptions{})
			})

			By("creating a pod for image pull verification")
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pull-secret-test",
					Namespace: pullTestNamespace,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:            "pull-test",
							Image:           pullTestImage,
							Command:         []string{"true"},
							ImagePullPolicy: corev1.PullAlways,
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: to.Ptr(false),
								RunAsNonRoot:             to.Ptr(true),
								SeccompProfile: &corev1.SeccompProfile{
									Type: corev1.SeccompProfileTypeRuntimeDefault,
								},
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
								},
							},
						},
					},
					RestartPolicy: corev1.RestartPolicyNever,
				},
			}
			_, err = kubeClient.CoreV1().Pods(pullTestNamespace).Create(ctx, pod, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to create pull-secret-test pod in %s", pullTestNamespace)

			By("waiting for image from registry.redhat.io to be pulled successfully")
			err = verifiers.VerifyImagePulled(pullTestNamespace, "registry.redhat.io", "ubi-minimal", imagePullTimeout).
				Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to wait for image from registry.redhat.io to be pulled successfully with the added pull secret")
		})
})
