// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package e2e

import (
	"context"
	"net/url"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/google/uuid"

	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	hcpsdk20261001 "github.com/Azure/ARO-HCP/test/sdk/v20261001/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Customer", func() {
	It("should authenticate a Keycloak user through the console and Kubernetes API",
		labels.RequireNothing,
		labels.High,
		labels.Positive,
		labels.CreateCluster,
		labels.AroRpApiCompatible,
		labels.Slow,
		labels.MIContainers(1),
		func(ctx context.Context) {
			const (
				customerNodePoolName     = "keycloak-np"
				customerExternalAuthName = "keycloak"
				consoleNamespace         = "openshift-console"
				keycloakRealm            = "e2e"
				keycloakGroup            = "keycloak-admins"
				consoleClientID          = "openshift-console"
				commandLineClientID      = "openshift-cli"
			)

			clusterName := "keycloak-" + rand.String(6)
			keycloakUsername := "e2e-user-" + rand.String(6)
			keycloakPassword := uuid.NewString() + "-Aa1!"
			consoleClientSecret := uuid.NewString()
			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "keycloak-external-auth", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for Keycloak external auth test")

			By("creating customer resources for the HCP cluster")
			clusterParams := framework.NewDefaultClusterParams20261001()
			clusterParams.ClusterName = clusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName
			clusterParams, err = tc.CreateClusterCustomerResources20261001(
				ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for Keycloak external auth test")

			By("creating the HCP cluster")
			err = tc.CreateHCPClusterFromParam20261001(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster for Keycloak external auth test")

			By("creating a node pool")
			nodePoolParams := framework.NewDefaultNodePoolParams20261001()
			nodePoolParams.ClusterName = clusterName
			nodePoolParams.NodePoolName = customerNodePoolName
			err = tc.CreateNodePoolFromParam20261001(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				managedResourceGroupName,
				clusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create node pool %q", customerNodePoolName)

			By("getting cluster credentials and the console URL")
			hcpClient := tc.Get20261001ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20261001(
				ctx,
				hcpClient,
				*resourceGroup.Name,
				clusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for cluster %q", clusterName)
			adminClient, err := kubernetes.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create admin Kubernetes client for cluster %q", clusterName)

			clusterResult, err := framework.GetHCPCluster20261001(ctx, hcpClient, *resourceGroup.Name, clusterName)
			Expect(err).NotTo(HaveOccurred(), "failed to get HCP cluster %q", clusterName)
			Expect(clusterResult.Properties).NotTo(BeNil(), "cluster %q Properties was nil", clusterName)
			Expect(clusterResult.Properties.Console).NotTo(BeNil(), "cluster %q Properties.Console was nil", clusterName)
			Expect(clusterResult.Properties.Console.URL).NotTo(BeNil(), "cluster %q Properties.Console.URL was nil", clusterName)
			consoleURL := strings.TrimRight(*clusterResult.Properties.Console.URL, "/")
			consoleCallbackURL := consoleURL + "/auth/callback"

			By("deploying an ephemeral public Keycloak identity provider")
			keycloakProvider, err := framework.DeployKeycloakTestProvider(ctx, adminRESTConfig, framework.KeycloakTestProviderOptions{
				Realm:                  keycloakRealm,
				Username:               keycloakUsername,
				Password:               keycloakPassword,
				Group:                  keycloakGroup,
				ConsoleClientID:        consoleClientID,
				ConsoleClientSecret:    consoleClientSecret,
				ConsoleRedirectURI:     consoleCallbackURL,
				ConsoleWebOrigin:       consoleURL,
				CommandLineClientID:    commandLineClientID,
				BootstrapAdminUser:     "e2e-admin",
				BootstrapAdminPassword: uuid.NewString(),
			})
			Expect(err).NotTo(HaveOccurred(), "failed to deploy the Keycloak test provider")
			Expect(keycloakProvider.IssuerURL).NotTo(BeEmpty(), "Keycloak issuer URL was empty")
			Expect(keycloakProvider.AuthorizationEndpoint).NotTo(BeEmpty(), "Keycloak authorization endpoint was empty")
			Expect(keycloakProvider.TokenEndpoint).NotTo(BeEmpty(), "Keycloak token endpoint was empty")

			By("creating the Keycloak external auth configuration")
			externalAuth := hcpsdk20261001.ExternalAuth{
				Properties: &hcpsdk20261001.ExternalAuthProperties{
					Issuer: &hcpsdk20261001.TokenIssuerProfile{
						URL: to.Ptr(keycloakProvider.IssuerURL),
						Audiences: []*string{
							to.Ptr(consoleClientID),
							to.Ptr(commandLineClientID),
						},
					},
					Claim: &hcpsdk20261001.ExternalAuthClaimProfile{
						Mappings: &hcpsdk20261001.TokenClaimMappingsProfile{
							Username: &hcpsdk20261001.UsernameClaimProfile{
								Claim:        to.Ptr("preferred_username"),
								PrefixPolicy: to.Ptr(hcpsdk20261001.UsernameClaimPrefixPolicyNoPrefix),
							},
							Groups: &hcpsdk20261001.GroupClaimProfile{
								Claim: to.Ptr("groups"),
							},
						},
					},
					Clients: []*hcpsdk20261001.ExternalAuthClientProfile{
						{
							ClientID: to.Ptr(consoleClientID),
							Component: &hcpsdk20261001.ExternalAuthClientComponentProfile{
								Name:                to.Ptr("console"),
								AuthClientNamespace: to.Ptr(consoleNamespace),
							},
							ExtraScopes: []*string{to.Ptr("profile")},
							Type:        to.Ptr(hcpsdk20261001.ExternalAuthClientTypeConfidential),
						},
						{
							ClientID: to.Ptr(commandLineClientID),
							Component: &hcpsdk20261001.ExternalAuthClientComponentProfile{
								Name:                to.Ptr("cli"),
								AuthClientNamespace: to.Ptr(consoleNamespace),
							},
							ExtraScopes: []*string{to.Ptr("profile")},
							Type:        to.Ptr(hcpsdk20261001.ExternalAuthClientTypePublic),
						},
					},
				},
			}
			_, err = framework.CreateOrUpdateExternalAuthAndWait20261001(
				ctx,
				tc.Get20261001ClientFactoryOrDie(ctx).NewExternalAuthsClient(),
				*resourceGroup.Name,
				clusterName,
				customerExternalAuthName,
				externalAuth,
				framework.ExternalAuthCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create Keycloak external auth config")

			By("waiting for the Kubernetes API to recover after external auth configuration")
			Eventually(func() error {
				_, err := adminClient.Discovery().ServerVersion()
				return err
			}, 5*time.Minute, 10*time.Second).Should(Succeed(), "Kubernetes API should recover after Keycloak external auth configuration")

			By("creating the Keycloak console OAuth client secret")
			consoleOAuthSecretName := customerExternalAuthName + "-console-" + consoleNamespace
			_, err = adminClient.CoreV1().Secrets("openshift-config").Create(ctx, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      consoleOAuthSecretName,
					Namespace: "openshift-config",
				},
				StringData: map[string]string{
					"clientSecret": consoleClientSecret,
				},
			}, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to create Keycloak console OAuth client secret %q", consoleOAuthSecretName)

			By("waiting for the console authentication configuration to redirect to Keycloak")
			Eventually(func() error {
				return framework.CheckConsoleLoginRedirect(
					ctx,
					consoleURL+"/auth/login",
					keycloakProvider.AuthorizationEndpoint,
				)
			}, 10*time.Minute, 15*time.Second).Should(Succeed(), "console login should redirect to Keycloak after its OAuth client secret is configured")

			By("granting cluster-admin to the Keycloak group")
			_, err = adminClient.RbacV1().ClusterRoleBindings().Create(ctx, &rbacv1.ClusterRoleBinding{
				ObjectMeta: metav1.ObjectMeta{Name: "keycloak-admins"},
				RoleRef: rbacv1.RoleRef{
					APIGroup: rbacv1.GroupName,
					Kind:     "ClusterRole",
					Name:     "cluster-admin",
				},
				Subjects: []rbacv1.Subject{{
					APIGroup: rbacv1.GroupName,
					Kind:     rbacv1.GroupKind,
					Name:     keycloakGroup,
				}},
			}, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to grant cluster-admin to Keycloak group %q", keycloakGroup)

			By("completing the console authorization-code login without a browser")
			var browserLogin *framework.BrowserLoginResult
			Eventually(func() error {
				var loginErr error
				browserLogin, loginErr = framework.CompleteKeycloakConsoleLogin(
					ctx,
					consoleURL+"/auth/login",
					keycloakProvider.IssuerURL,
					keycloakUsername,
					keycloakPassword,
				)
				return loginErr
			}, 10*time.Minute, 15*time.Second).Should(Succeed(), "Keycloak user should complete the console authorization-code login")
			Expect(browserLogin).NotTo(BeNil(), "browserless console login result was nil")
			Expect(browserLogin.AuthorizationRequest).NotTo(BeNil(), "console login did not capture a Keycloak authorization request")
			Expect(browserLogin.FinalURL).NotTo(BeNil(), "console login final URL was nil")
			Expect(browserLogin.ConsoleCookies).NotTo(BeEmpty(), "console login did not establish a session cookie")

			authorizationQuery := browserLogin.AuthorizationRequest.Query()
			Expect(authorizationQuery.Get("client_id")).To(Equal(consoleClientID), "console authorization request should use the Keycloak console client")
			Expect(authorizationQuery.Get("redirect_uri")).To(Equal(consoleCallbackURL), "console authorization request should use the console callback URL")
			Expect(authorizationQuery.Get("response_type")).To(Equal("code"), "console authorization request should use the authorization-code flow")
			Expect(strings.Fields(authorizationQuery.Get("scope"))).To(ContainElements("openid", "profile"), "console authorization request should include openid and profile scopes")
			consoleParsedURL, err := url.Parse(consoleURL)
			Expect(err).NotTo(HaveOccurred(), "failed to parse console URL")
			Expect(browserLogin.FinalURL.Host).To(Equal(consoleParsedURL.Host), "completed login should return to the console host")

			By("requesting a Keycloak user access token")
			accessToken, err := framework.RequestKeycloakPasswordToken(
				ctx,
				keycloakProvider,
				commandLineClientID,
				keycloakUsername,
				keycloakPassword,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to obtain a Keycloak access token for the test user")
			Expect(accessToken).NotTo(BeEmpty(), "Keycloak access token was empty")

			By("verifying the Keycloak token username and groups through the Kubernetes API")
oidcRESTConfig := rest.AnonymousClientConfig(adminRESTConfig)
oidcRESTConfig.BearerToken = accessToken
			oidcClient, err := kubernetes.NewForConfig(oidcRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create Kubernetes client with Keycloak token")

			Eventually(func(g Gomega) {
				review, err := oidcClient.AuthenticationV1().SelfSubjectReviews().Create(
					ctx,
					&authenticationv1.SelfSubjectReview{},
					metav1.CreateOptions{},
				)
				g.Expect(err).NotTo(HaveOccurred(), "Keycloak token should be accepted by the Kubernetes API")
				g.Expect(review.Status.UserInfo.Username).To(Equal(keycloakUsername), "Keycloak preferred_username should be used without a prefix")
				g.Expect(review.Status.UserInfo.Groups).To(ContainElement(keycloakGroup), "Keycloak groups claim should include the authorized group")
			}, 10*time.Minute, 10*time.Second).Should(Succeed(), "Kubernetes should authenticate the Keycloak user with the expected claims")

			By("verifying the Keycloak group grants cluster-admin access")
			namespaces, err := oidcClient.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
			Expect(err).NotTo(HaveOccurred(), "Keycloak user should be authorized to list namespaces")
			Expect(namespaces.Items).NotTo(BeEmpty(), "Keycloak user should observe at least one namespace")

			By("verifying cluster operators are available after Keycloak authentication")
			err = verifiers.VerifyAllClusterOperatorsAvailable().Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to verify cluster operators after Keycloak authentication")

			GinkgoLogr.Info("completed Keycloak external auth verification",
				"cluster", clusterName,
				"issuer", keycloakProvider.IssuerURL,
				"keycloakNamespace", keycloakProvider.Namespace,
			)
		},
	)
})
