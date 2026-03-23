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
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	authenticationv1 "k8s.io/api/authentication/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"

	"github.com/openshift-eng/openshift-tests-extension/pkg/util/sets"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Customer", func() {

	terminalProvisioningStates := sets.New(hcpsdk20240610preview.ProvisioningStateSucceeded, hcpsdk20240610preview.ProvisioningStateFailed, hcpsdk20240610preview.ProvisioningStateCanceled)

	It("should be able to test admin credentials before cluster ready, then full admin credential lifecycle",
		labels.RequireNothing,
		labels.High,
		labels.Positive,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			clusterName := "admin-cred-lifecycle-" + rand.String(6)
			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			By("creating resource group for admin credential lifecycle testing")
			resourceGroup, err := tc.NewResourceGroup(ctx, "admin-credential-lifecycle-test", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = clusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources(ctx,
				resourceGroup,
				clusterParams,
				map[string]any{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred())

			By("starting HCP cluster creation asynchronously")
			clusterClient := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
			timeout := 45 * time.Minute
			deploymentCtx, deploymentCancel := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during admin credential lifecycle test", timeout.Minutes()))
			defer deploymentCancel()

			_, err = framework.BeginCreateHCPCluster(
				deploymentCtx,
				GinkgoLogr,
				clusterClient,
				*resourceGroup.Name,
				clusterName,
				clusterParams,
				tc.Location(),
			)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for cluster to appear and testing admin credentials while in deploying state")
			// Poll the cluster state and test admin credentials when we find it deploying
			var testedWhileDeploying bool
			var previousState hcpsdk20240610preview.ProvisioningState
			GinkgoLogr.Info("creating cluster, waiting for it to reach a terminal state")
			Eventually(func() bool {
				cluster, err := framework.GetHCPCluster(ctx, clusterClient, *resourceGroup.Name, clusterName)
				if err != nil {
					var respErr *azcore.ResponseError
					if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
						GinkgoLogr.Info("Cluster not found yet, continuing to wait...")
						return false
					}
					Fail("Cluster GET returned error: " + err.Error())
				}

				// only log state changes
				if previousState != *cluster.Properties.ProvisioningState {
					GinkgoLogr.Info("Cluster provisioning state updated", "provisioningState", *cluster.Properties.ProvisioningState)
					previousState = *cluster.Properties.ProvisioningState
				}

				// If cluster is still deploying and we haven't tested yet, test admin credentials
				if !testedWhileDeploying && !terminalProvisioningStates.Has(*cluster.Properties.ProvisioningState) {
					By("testing admin credentials while cluster is in deploying state")
					testedWhileDeploying = true
					_, err := clusterClient.BeginRequestAdminCredential(
						ctx,
						*resourceGroup.Name,
						clusterName,
						nil,
					)
					var respErr *azcore.ResponseError
					if err != nil && errors.As(err, &respErr) && http.StatusConflict == respErr.StatusCode {
						By("verifying admin credentials request fails with HTTP 409 CONFLICT on deploying cluster")
						GinkgoLogr.Info("Admin credentials request correctly returned 409 conflict error while cluster is deploying")
					} else {
						Fail("Admin credentials did not return 409 conflict error while cluster is deploying")
					}
				}

				// If cluster is ready, we're done
				if *cluster.Properties.ProvisioningState == hcpsdk20240610preview.ProvisioningStateSucceeded {
					if !testedWhileDeploying {
						Fail("Cluster provisioned too quickly to test 409 behavior - unable to validate admin credentials fail during deployment")
					}
					return true // Success - cluster is ready
				}

				// If cluster failed, that's an error
				if *cluster.Properties.ProvisioningState == hcpsdk20240610preview.ProvisioningStateFailed {
					Fail("Cluster provisioning failed")
				}

				// Continue waiting
				return false
			}, 45*time.Minute, 30*time.Second).Should(BeTrue(), "Cluster should become ready within 45 minutes")

			// Store all admin credentials for later validation
			var credentials []*rest.Config
			credentialCount := 3

			By(fmt.Sprintf("creating %d admin credentials for the ready cluster", credentialCount))
			for i := range credentialCount {
				By(fmt.Sprintf("requesting admin credential %d", i+1))
				validationTimeout := 10 * time.Minute
				validationCtx, validationCancel := context.WithTimeoutCause(ctx, validationTimeout, fmt.Errorf(
					"timeout exceeded (%v) while requesting admin credential %d",
					validationTimeout, i+1))
				defer validationCancel()

				// request admin credential without using the helper function to ensure we can validate
				// the raw kubeconfig returned by the API. The helper function returns a rest.Config
				// that omits certain information that is required for validation (e.g. config.Clusters).
				adminCredentialRequestPoller, err := clusterClient.BeginRequestAdminCredential(
					validationCtx,
					*resourceGroup.Name,
					clusterName,
					nil,
				)
				Expect(err).NotTo(HaveOccurred())

				credResp, err := adminCredentialRequestPoller.PollUntilDone(validationCtx, &runtime.PollUntilDoneOptions{
					Frequency: framework.StandardPollInterval,
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(credResp.Kubeconfig).NotTo(BeNil())

				By("validating kubeconfig returned by the API is valid")
				kubeconfigData := []byte(*credResp.Kubeconfig)
				config, err := clientcmd.Load(kubeconfigData)
				Expect(err).NotTo(HaveOccurred(), "kubeconfig must be valid YAML")

				By("validating exactly one cluster in kubeconfig")
				Expect(config.Clusters).To(HaveLen(1), "kubeconfig must contain exactly one cluster")
				Expect(config.Clusters["cluster"]).NotTo(BeNil())
				cluster := config.Clusters["cluster"]

				By("validating cluster has CertificateAuthorityData")
				Expect(cluster.CertificateAuthorityData).NotTo(BeEmpty(), "cluster must have CertificateAuthorityData")

				By("validating cluster CA data is valid PEM")
				pemBlock, rest := pem.Decode(cluster.CertificateAuthorityData)
				Expect(pemBlock).NotTo(BeNil(), "cluster CA data must contain a valid PEM block")
				Expect(pemBlock.Type).To(Equal("CERTIFICATE"), "cluster CA PEM block must be of type CERTIFICATE")
				Expect(rest).To(BeEmpty(), "cluster CA data must contain exactly one PEM block")

				// the certificate-authority-data is always presented as a self signed certificate where
				// the subject and issuer are identical
				// https://learn.microsoft.com/en-us/archive/technet-wiki/3147.pki-certificate-chaining-engine-cce#Building_the_Certificate_Chain
				By("validating certificate authority data content uses the correct certificate")
				cert, err := x509.ParseCertificate(pemBlock.Bytes)
				Expect(err).NotTo(HaveOccurred(), "cluster CA data must contain a valid certificate")
				Expect(cert.Issuer).To(Equal(cert.Subject), "root CA data must be self-signed")

				By("validating cluster does not use InsecureSkipTLSVerify")
				Expect(cluster.InsecureSkipTLSVerify).To(BeFalse(), "cluster must not use InsecureSkipTLSVerify")

				By("converting validated kubeconfig to rest.Config")
				adminRESTConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigData)
				Expect(err).NotTo(HaveOccurred())
				Expect(adminRESTConfig).NotTo(BeNil(), "adminRESTConfig was nil for credential %d", i+1)

				credentials = append(credentials, adminRESTConfig)

				By(fmt.Sprintf("validating admin credential %d works", i+1))
				kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
				Expect(err).NotTo(HaveOccurred(), "should be able to create kube client for admin credential %d", i+1)

				response, err := kubeClient.AuthenticationV1().SelfSubjectReviews().Create(ctx, &authenticationv1.SelfSubjectReview{}, metav1.CreateOptions{})
				Expect(err).NotTo(HaveOccurred(), "should be able to create SelfSubjectReview for admin credential %d", i+1)

				// ensure the SSR identifies the client certificate as having system:masters
				if !sets.New(response.Status.UserInfo.Groups...).Has("system:masters") {
					GinkgoLogr.Info("breakglass admin does not have system:masters group", "groups", response.Status.UserInfo.Groups)
				}
				GinkgoLogr.Info("successfully verified admin credential", "credentialNumber", i+1)
			}

			skipSuite := os.Getenv("ARO_HCP_SUITE_NAME") == "integration/parallel" && time.Now().Before(time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC))

			By("revoking all cluster admin credentials via ARO HCP RP API")
			err = tc.RevokeCredentialsAndWait(ctx, clusterClient, *resourceGroup.Name, clusterName, 15*time.Minute)
			if err != nil && skipSuite {
				Skip("skipping revocation and remaining steps in integration/parallel suite")
			}
			Expect(err).NotTo(HaveOccurred())

			By("validating all admin credentials now fail after revocation")
			for i, cred := range credentials {
				By(fmt.Sprintf("verifying admin credential %d now fails", i+1))
				// TODO(bvesel) remove once OCPBUGS-62177 is implemented
				kubeClient, err := kubernetes.NewForConfig(cred)
				Expect(err).NotTo(HaveOccurred(), "should be able to create kube client for admin credential %d", i+1)

				var lastError string
				var lastResp *authenticationv1.SelfSubjectReview
				err = wait.PollUntilContextTimeout(ctx, 15*time.Second, 5*time.Minute, false, func(ctx context.Context) (done bool, err error) {
					resp, err := kubeClient.AuthenticationV1().SelfSubjectReviews().Create(ctx, &authenticationv1.SelfSubjectReview{}, metav1.CreateOptions{})
					if !apierrors.IsUnauthorized(err) {
						errMessage := "<nil>"
						if err != nil {
							errMessage = err.Error()
						}

						if lastError != errMessage || !reflect.DeepEqual(lastResp, resp) {
							GinkgoLogr.Info("admin credential still working or returned unexpected error after revocation", "credentialNumber", i+1, "error", errMessage, "response", resp)
							lastError = errMessage
							lastResp = resp
						}
						return false, nil
					}
					GinkgoLogr.Info("successfully verified admin credential fails after revocation", "credentialNumber", i+1)
					return true, nil
				})
				if err != nil && skipSuite {
					Skip("skipping remaining steps in integration/parallel suite")
				}
				Expect(err).NotTo(HaveOccurred(), "Admin credential %d should fail after revocation, last error: %v", i+1, lastError)
			}

			By("verifying new admin credentials can still be requested after revocation")
			// After revocation, new admin credential requests should still work
			// This validates the revocation endpoint doesn't break the cluster
			newAdminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster(
				ctx,
				clusterClient,
				*resourceGroup.Name,
				clusterName,
				10*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(newAdminRESTConfig).NotTo(BeNil(), "newAdminRESTConfig was nil after revocation")

			By("verifying new admin credentials work after revocation")
			err = verifiers.VerifyHCPCluster(ctx, newAdminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "New admin credentials should work after revocation")
		})
})
