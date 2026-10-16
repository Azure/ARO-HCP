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
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/kubernetes"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	hcpsdk20261001 "github.com/Azure/ARO-HCP/test/sdk/v20261001/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Customer", func() {
	It("should be able to configure a cluster for Entra console authentication",
		labels.RequireNothing,
		labels.High,
		labels.Positive,
		labels.CreateCluster,
		labels.AroRpApiCompatible,
		labels.MIContainers(1),
		func(ctx context.Context) {
			const (
				customerNodePoolName     = "entra-np"
				customerExternalAuthName = "entra"
				consoleNamespace         = "openshift-console"
			)

			clusterName := "entra-" + rand.String(6)
			externalAuthUsername := clusterName + "@example.com"
			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "entra-console-auth", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for Entra console authentication test")

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
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for Entra console authentication test")

			By("creating the HCP cluster")
			err = tc.CreateHCPClusterFromParam20261001(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster for Entra console authentication test")

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

			By("getting cluster credentials")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20261001(
				ctx,
				tc.Get20261001ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				clusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for cluster %q", clusterName)
			kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create Kubernetes client for cluster %q", clusterName)

			By("getting the cluster console callback URL")
			clusterResult, err := framework.GetHCPCluster20261001(
				ctx,
				tc.Get20261001ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				clusterName,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get HCP cluster %q", clusterName)
			Expect(clusterResult.Properties).NotTo(BeNil(), "cluster %q Properties was nil", clusterName)
			Expect(clusterResult.Properties.Console).NotTo(BeNil(), "cluster %q Properties.Console was nil", clusterName)
			Expect(clusterResult.Properties.Console.URL).NotTo(BeNil(), "cluster %q Properties.Console.URL was nil", clusterName)
			consoleURL := strings.TrimRight(*clusterResult.Properties.Console.URL, "/")
			consoleCallbackURL := consoleURL + "/auth/callback"

			By("creating an Entra application with console and local CLI redirect URIs")
			app, _, err := tc.NewNamedAppRegistrationWithServicePrincipal(
				ctx,
				clusterName+"-auth",
				[]string{consoleCallbackURL, "http://localhost:8000"},
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create Entra application for cluster %q", clusterName)

			graphClient, err := tc.GetGraphClient(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to get Microsoft Graph client")
			baseTime := time.Now()
			password, err := graphClient.AddPassword(
				ctx,
				app.ID,
				"external-auth-pass",
				baseTime.Add(-5*time.Minute),
				baseTime.Add(24*time.Hour),
			)
			Expect(err).NotTo(HaveOccurred(), "failed to add a client secret to Entra application %q", app.DisplayName)

			By("creating the external auth configuration used by the CLI workflow")
			externalAuth := hcpsdk20261001.ExternalAuth{
				Properties: &hcpsdk20261001.ExternalAuthProperties{
					Issuer: &hcpsdk20261001.TokenIssuerProfile{
						URL:       to.Ptr(fmt.Sprintf("https://login.microsoftonline.com/%s/v2.0", tc.TenantID())),
						Audiences: []*string{to.Ptr(app.AppID)},
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
							ClientID: to.Ptr(app.AppID),
							Component: &hcpsdk20261001.ExternalAuthClientComponentProfile{
								Name:                to.Ptr("console"),
								AuthClientNamespace: to.Ptr(consoleNamespace),
							},
							ExtraScopes: []*string{to.Ptr("profile")},
							Type:        to.Ptr(hcpsdk20261001.ExternalAuthClientTypeConfidential),
						},
						{
							ClientID: to.Ptr(app.AppID),
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
			Expect(err).NotTo(HaveOccurred(), "failed to create external auth config %q", customerExternalAuthName)

			By("verifying the external auth configuration")
			externalAuthResult, err := framework.GetExternalAuth20261001(
				ctx,
				tc.Get20261001ClientFactoryOrDie(ctx).NewExternalAuthsClient(),
				*resourceGroup.Name,
				clusterName,
				customerExternalAuthName,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get external auth config %q", customerExternalAuthName)
			Expect(externalAuthResult.Properties).NotTo(BeNil(), "external auth %q Properties was nil", customerExternalAuthName)
			Expect(externalAuthResult.Properties.ProvisioningState).NotTo(BeNil(), "external auth %q ProvisioningState was nil", customerExternalAuthName)
			Expect(*externalAuthResult.Properties.ProvisioningState).To(
				Equal(hcpsdk20261001.ExternalAuthProvisioningStateSucceeded),
				"external auth %q provisioning state should be Succeeded",
				customerExternalAuthName,
			)
			Expect(externalAuthResult.Properties.Issuer).NotTo(BeNil(), "external auth %q Issuer was nil", customerExternalAuthName)
			Expect(externalAuthResult.Properties.Issuer.URL).NotTo(BeNil(), "external auth %q issuer URL was nil", customerExternalAuthName)
			Expect(*externalAuthResult.Properties.Issuer.URL).To(Equal(fmt.Sprintf("https://login.microsoftonline.com/%s/v2.0", tc.TenantID())), "external auth issuer URL should match the tenant v2 endpoint")
			Expect(externalAuthResult.Properties.Issuer.Audiences).To(HaveLen(1), "external auth should configure one issuer audience")
			Expect(externalAuthResult.Properties.Issuer.Audiences[0]).NotTo(BeNil(), "external auth issuer returned a nil audience")
			Expect(*externalAuthResult.Properties.Issuer.Audiences[0]).To(Equal(app.AppID), "external auth issuer audience should match the Entra application")
			Expect(externalAuthResult.Properties.Claim).NotTo(BeNil(), "external auth %q Claim was nil", customerExternalAuthName)
			Expect(externalAuthResult.Properties.Claim.Mappings).NotTo(BeNil(), "external auth %q Claim.Mappings was nil", customerExternalAuthName)
			Expect(externalAuthResult.Properties.Claim.Mappings.Username).NotTo(BeNil(), "external auth %q username mapping was nil", customerExternalAuthName)
			Expect(externalAuthResult.Properties.Claim.Mappings.Groups).NotTo(BeNil(), "external auth %q groups mapping was nil", customerExternalAuthName)
			Expect(externalAuthResult.Properties.Claim.Mappings.Username.Claim).NotTo(BeNil(), "external auth %q username claim was nil", customerExternalAuthName)
			Expect(externalAuthResult.Properties.Claim.Mappings.Username.PrefixPolicy).NotTo(BeNil(), "external auth %q username prefix policy was nil", customerExternalAuthName)
			Expect(externalAuthResult.Properties.Claim.Mappings.Groups.Claim).NotTo(BeNil(), "external auth %q groups claim was nil", customerExternalAuthName)
			Expect(*externalAuthResult.Properties.Claim.Mappings.Username.Claim).To(Equal("preferred_username"), "external auth username claim should match the CLI workflow")
			Expect(*externalAuthResult.Properties.Claim.Mappings.Username.PrefixPolicy).To(Equal(hcpsdk20261001.UsernameClaimPrefixPolicyNoPrefix), "external auth username prefix policy should match the CLI workflow")
			Expect(externalAuthResult.Properties.Claim.Mappings.Username.Prefix).To(BeNil(), "external auth username prefix should be nil for NoPrefix policy")
			Expect(*externalAuthResult.Properties.Claim.Mappings.Groups.Claim).To(Equal("groups"), "external auth groups claim should match the CLI workflow")
			Expect(externalAuthResult.Properties.Clients).To(HaveLen(2), "external auth should configure console and CLI clients")

			clientsByComponent := map[string]*hcpsdk20261001.ExternalAuthClientProfile{}
			for _, client := range externalAuthResult.Properties.Clients {
				Expect(client).NotTo(BeNil(), "external auth %q returned a nil client", customerExternalAuthName)
				Expect(client.Component).NotTo(BeNil(), "external auth %q returned a client with a nil component", customerExternalAuthName)
				Expect(client.Component.Name).NotTo(BeNil(), "external auth %q returned a client with a nil component name", customerExternalAuthName)
				clientsByComponent[*client.Component.Name] = client
			}
			Expect(clientsByComponent).To(HaveKey("console"), "external auth should include the console client")
			Expect(clientsByComponent).To(HaveKey("cli"), "external auth should include the CLI client")
			Expect(clientsByComponent["console"].Type).NotTo(BeNil(), "console external auth client type was nil")
			Expect(clientsByComponent["cli"].Type).NotTo(BeNil(), "CLI external auth client type was nil")
			Expect(*clientsByComponent["console"].Type).To(Equal(hcpsdk20261001.ExternalAuthClientTypeConfidential), "console external auth client should be confidential")
			Expect(*clientsByComponent["cli"].Type).To(Equal(hcpsdk20261001.ExternalAuthClientTypePublic), "CLI external auth client should be public")
			for componentName, client := range clientsByComponent {
				Expect(client.ClientID).NotTo(BeNil(), "%s external auth client ID was nil", componentName)
				Expect(*client.ClientID).To(Equal(app.AppID), "%s external auth client ID should match the Entra application", componentName)
				Expect(client.Component.AuthClientNamespace).NotTo(BeNil(), "%s external auth client namespace was nil", componentName)
				Expect(*client.Component.AuthClientNamespace).To(Equal(consoleNamespace), "%s external auth client namespace should match the CLI workflow", componentName)
				Expect(client.ExtraScopes).To(HaveLen(1), "%s external auth client should request one extra scope", componentName)
				Expect(client.ExtraScopes[0]).NotTo(BeNil(), "%s external auth client returned a nil extra scope", componentName)
				Expect(*client.ExtraScopes[0]).To(Equal("profile"), "%s external auth client should request the profile scope", componentName)
			}

			By("waiting for the Kubernetes API to recover after external auth configuration")
			Eventually(func() error {
				_, err := kubeClient.Discovery().ServerVersion()
				return err
			}, 5*time.Minute, 10*time.Second).Should(Succeed(), "Kubernetes API should recover after external auth configuration")

			By("creating the console OAuth client secret")
			consoleOAuthSecretName := customerExternalAuthName + "-console-" + consoleNamespace
			_, err = kubeClient.CoreV1().Secrets("openshift-config").Create(ctx, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      consoleOAuthSecretName,
					Namespace: "openshift-config",
				},
				StringData: map[string]string{
					"clientSecret": password.SecretText,
				},
			}, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to create console OAuth client secret %q", consoleOAuthSecretName)

			By("waiting for the console authentication configuration to redirect to Entra")
			entraAuthorizationEndpoint := fmt.Sprintf(
				"https://login.microsoftonline.com/%s/oauth2/v2.0/authorize",
				tc.TenantID(),
			)
			Eventually(func() error {
				return framework.CheckConsoleLoginRedirect(
					ctx,
					consoleURL+"/auth/login",
					entraAuthorizationEndpoint,
				)
			}, 10*time.Minute, 15*time.Second).Should(Succeed(), "console login should redirect to Entra after its OAuth client secret is configured")

			By("granting cluster-admin to an unprefixed preferred username")
			_, err = kubeClient.RbacV1().ClusterRoleBindings().Create(ctx, &rbacv1.ClusterRoleBinding{
				ObjectMeta: metav1.ObjectMeta{Name: "aro-admin"},
				RoleRef: rbacv1.RoleRef{
					APIGroup: rbacv1.GroupName,
					Kind:     "ClusterRole",
					Name:     "cluster-admin",
				},
				Subjects: []rbacv1.Subject{
					{
						APIGroup: rbacv1.GroupName,
						Kind:     rbacv1.UserKind,
						Name:     externalAuthUsername,
					},
				},
			}, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to create aro-admin cluster role binding for %q", externalAuthUsername)

			By("verifying cluster operators are available after Entra authentication configuration")
			err = verifiers.VerifyAllClusterOperatorsAvailable().Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to verify cluster operators after Entra authentication configuration")
		},
	)
})
