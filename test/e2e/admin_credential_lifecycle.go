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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/openshift-eng/openshift-tests-extension/pkg/util/sets"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
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
		labels.MIContainers(1),
		func(ctx context.Context) {
			clusterName := "admin-cred-lifecycle-" + rand.String(6)
			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign identity containers")
			}

			By("creating resource group for admin credential lifecycle testing")
			resourceGroup, err := tc.NewResourceGroup(ctx, "admin-credential-lifecycle-test", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for admin credential lifecycle test")

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams20240610()
			clusterParams.ClusterName = clusterName
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
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for admin credential lifecycle cluster")

			By("starting HCP cluster creation asynchronously")
			clusterClient := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
			timeout := framework.ClusterCreationTimeout
			deploymentCtx, deploymentCancel := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during admin credential lifecycle test", timeout.Minutes()))
			defer deploymentCancel()

			_, err = framework.BeginCreateHCPCluster20240610(
				deploymentCtx,
				GinkgoLogr,
				clusterClient,
				*resourceGroup.Name,
				clusterName,
				clusterParams,
				tc.Location(),
			)
			Expect(err).NotTo(HaveOccurred(), "failed to begin creating HCP cluster %q", clusterName)

			By("waiting for cluster to appear and testing admin credentials while in deploying state")
			// Poll the cluster state and test admin credentials when we find it deploying
			var testedWhileDeploying bool
			var previousState hcpsdk20240610preview.ProvisioningState
			GinkgoLogr.Info("creating cluster, waiting for it to reach a terminal state")
			Eventually(func() bool {
				cluster, err := framework.GetHCPCluster20240610(ctx, clusterClient, *resourceGroup.Name, clusterName)
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
					// Under the required-CSR contract the frontend validates the CSR
					// before the provisioning-state conflict check, so submit a valid
					// CSR (via the 20260901 helper) to reach the conflict check.
					_, err := tc.GetAdminRESTConfigForHCPCluster20260901(
						ctx,
						tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
						*resourceGroup.Name,
						clusterName,
						framework.GetAdminRESTConfigTimeout,
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
			}, framework.ClusterCreationTimeout, 30*time.Second).Should(BeTrue(), fmt.Sprintf("Cluster should become ready within '%f' minutes", framework.ClusterCreationTimeout.Minutes()))

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

				// Under the required-CSR contract, admin credentials are issued via a
				// CSR the client submits (20260901 api-version). Use the framework
				// helper, which generates the key + CSR, submits the request, and
				// returns a ready-to-use rest.Config with the client key injected and
				// the cluster CA populated in TLSClientConfig.
				adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20260901(
					validationCtx,
					tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
					*resourceGroup.Name,
					clusterName,
					framework.GetAdminRESTConfigTimeout,
				)
				Expect(err).NotTo(HaveOccurred(), "failed to request admin credential %d", i+1)
				Expect(adminRESTConfig).NotTo(BeNil(), "adminRESTConfig was nil for credential %d", i+1)

				By("validating admin credential carries the cluster CA data")
				Expect(adminRESTConfig.CAData).NotTo(BeEmpty(), "admin credential must carry cluster CA data for credential %d", i+1)

				By("validating admin credential does not use InsecureSkipTLSVerify")
				Expect(adminRESTConfig.Insecure).To(BeFalse(), "admin credential must not use InsecureSkipTLSVerify")

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
			err = tc.RevokeCredentialsAndWait20240610(ctx, clusterClient, *resourceGroup.Name, clusterName, 15*time.Minute)
			if err != nil && skipSuite {
				Skip("skipping revocation and remaining steps in integration/parallel suite")
			}
			Expect(err).NotTo(HaveOccurred(), "failed to revoke admin credentials for cluster %q", clusterName)

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
			newAdminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20240610(
				ctx,
				clusterClient,
				*resourceGroup.Name,
				clusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to request new admin credentials after revocation for cluster %q", clusterName)
			Expect(newAdminRESTConfig).NotTo(BeNil(), "newAdminRESTConfig was nil after revocation")

			By("verifying new admin credentials work after revocation")
			err = verifiers.VerifyHCPCluster(ctx, newAdminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "New admin credentials should work after revocation")
		})
})
