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

	configv1 "github.com/openshift/api/config/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Customer", func() {
	It("should be able to see an HCP cluster registered with console.redhat.com via the Insights Operator",
		labels.RequireNothing,
		labels.High,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.MIContainers(1),
		func(ctx context.Context) {
			const (
				customerClusterName = "insights-hcp-cluster"
				pullSecretName      = "additional-pull-secret"
				pullSecretNamespace = "kube-system"
				cloudOpenShiftHost  = "cloud.openshift.com"

				pullSecretMergeTimeout    = 10 * time.Minute
				insightsTransitionTimeout = 10 * time.Minute
			)
			tc := framework.NewTestContext()

			By("checking pull secret file exists and contains cloud.openshift.com token")
			pullSecretFilePath := filepath.Join(tc.PullSecretPath(), "pull-secret")
			if _, err := os.Stat(pullSecretFilePath); errors.Is(err, os.ErrNotExist) {
				Skip(fmt.Sprintf("Pull secret file not found at %s, skipping test", pullSecretFilePath))
			}

			pullSecretFileData, err := os.ReadFile(pullSecretFilePath)
			Expect(err).NotTo(HaveOccurred(), "failed to read pull-secret file from %s", pullSecretFilePath)

			var pullSecretConfig framework.DockerConfigJSON
			err = json.Unmarshal(pullSecretFileData, &pullSecretConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to parse pull-secret file")

			cloudOpenShiftAuth, hasToken := pullSecretConfig.Auths[cloudOpenShiftHost]
			if !hasToken {
				Skip("cloud.openshift.com token not found in pull-secret file, skipping Insights Operator test")
			}

			if tc.UsePooledIdentities() {
				err = tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "insights-test", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for Insights Operator test")

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams20260901()
			clusterParams.ClusterName = customerClusterName
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20260901(ctx,
				resourceGroup,
				clusterParams,
				map[string]any{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for Insights Operator cluster")

			By("creating the cluster")
			err = tc.CreateHCPClusterFromParam20260901(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster for Insights Operator test")

			By("getting credentials")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20260901(
				ctx,
				tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for Insights Operator cluster")

			By("ensuring the cluster is viable")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to verify HCP cluster viability")

			By("verifying Insights Operator does not yet have cloud.openshift.com token")
			configClient, err := configv1client.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create config client for Insights Operator pre-check")

			insightsCO, err := configClient.ClusterOperators().Get(ctx, "insights", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to get ClusterOperator insights for pre-check")

			for _, cond := range insightsCO.Status.Conditions {
				if cond.Type == configv1.OperatorAvailable && cond.Status == configv1.ConditionTrue {
					Skip("Insights Operator is already Available before cloud.openshift.com token was added, " +
						"the NoToken-to-healthy transition cannot be tested")
				}
			}

			By("creating additional-pull-secret with cloud.openshift.com token")
			kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create kubernetes client")

			dockerConfigBytes, err := json.Marshal(framework.DockerConfigJSON{
				Auths: map[string]framework.RegistryAuth{
					cloudOpenShiftHost: cloudOpenShiftAuth,
				},
			})
			Expect(err).NotTo(HaveOccurred(), "failed to marshal docker config for cloud.openshift.com")

			cloudPullSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pullSecretName,
					Namespace: pullSecretNamespace,
				},
				Type: corev1.SecretTypeDockerConfigJson,
				Data: map[string][]byte{
					corev1.DockerConfigJsonKey: dockerConfigBytes,
				},
			}

			_, err = kubeClient.CoreV1().Secrets(pullSecretNamespace).Create(ctx, cloudPullSecret, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to create additional-pull-secret with cloud.openshift.com token")

			By("waiting for HCCO to merge cloud.openshift.com token into global-pull-secret")
			err = verifiers.VerifyPullSecretMergedIntoGlobal(cloudOpenShiftHost, pullSecretMergeTimeout).
				Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to wait for cloud.openshift.com token to be merged into global-pull-secret by HCCO")

			By("verifying the Insights Operator detects the token and becomes healthy")
			err = verifiers.VerifyInsightsOperatorHealthy(insightsTransitionTimeout).
				Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to wait for Insights Operator to become healthy after cloud.openshift.com token was added")
		})
})
