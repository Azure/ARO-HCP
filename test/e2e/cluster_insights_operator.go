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

				pullSecretMergeTimeout   = 10 * time.Minute
				insightsHealthTimeout    = 5 * time.Minute
				consoleVisibilityTimeout = 20 * time.Minute
			)
			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
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

			By("verifying the Insights Operator is healthy")
			err = verifiers.VerifyInsightsOperatorHealthy(insightsHealthTimeout).
				Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "Insights Operator is not healthy")

			By("checking for cloud.openshift.com token and OCM credentials for console verification")
			cloudTokenPath := filepath.Join(tc.PullSecretPath(), "cloud-openshift-token")
			ocmCredentialsPath := filepath.Join(tc.PullSecretPath(), "ocm-credentials")

			cloudTokenData, cloudTokenErr := os.ReadFile(cloudTokenPath)
			ocmCredentialsData, ocmCredentialsErr := os.ReadFile(ocmCredentialsPath)

			if !errors.Is(cloudTokenErr, os.ErrNotExist) && !errors.Is(ocmCredentialsErr, os.ErrNotExist) {
				Expect(cloudTokenErr).NotTo(HaveOccurred(), "failed to read cloud-openshift-token file from %s", cloudTokenPath)
				Expect(ocmCredentialsErr).NotTo(HaveOccurred(), "failed to read OCM credentials file from %s", ocmCredentialsPath)

				var cloudOpenShiftAuth framework.RegistryAuth
				err = json.Unmarshal(cloudTokenData, &cloudOpenShiftAuth)
				Expect(err).NotTo(HaveOccurred(), "failed to parse cloud-openshift-token file")

				var ocmCredentials struct {
					ClientID     string `json:"client_id"`
					ClientSecret string `json:"client_secret"`
				}
				err = json.Unmarshal(ocmCredentialsData, &ocmCredentials)
				Expect(err).NotTo(HaveOccurred(), "failed to parse OCM credentials file")

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

				By("verifying the Insights Operator is still healthy after token injection")
				err = verifiers.VerifyInsightsOperatorHealthy(insightsHealthTimeout).
					Verify(ctx, adminRESTConfig)
				Expect(err).NotTo(HaveOccurred(), "Insights Operator became unhealthy after cloud.openshift.com token was added")

				By("verifying the cluster is visible in console.redhat.com")
				err = verifiers.VerifyClusterVisibleInConsole(ocmCredentials.ClientID, ocmCredentials.ClientSecret, consoleVisibilityTimeout).
					Verify(ctx, adminRESTConfig)
				Expect(err).NotTo(HaveOccurred(), "cluster did not become visible in console.redhat.com")
			}
		})
})
