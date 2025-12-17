// Copyright 2025 Microsoft Corporation
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
// Copyright 2025 Microsoft
// Licensed under the Apache License, Version 2.0.
package e2e

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Customer", func() {
	BeforeEach(func() {
		// do nothing.  per test initialization usually ages better than shared.
	})

	It("should be able to create a cluster with an external auth config and get the external auth config",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		func(ctx context.Context) {
			const (
				customerNetworkSecurityGroupName = "ea-nsg-name"
				customerVnetName                 = "ea-vnet-name"
				customerVnetSubnetName           = "ea-vnet-subnet1"
				customerClusterName              = "ea-cluster"
				customerNodePoolName             = "ea-np-1"
				customerExternalAuthName         = "external-auth"
				externalAuthSubjectPrefix        = "prefix-" // TODO: ARO-21008 preventing us setting NoPrefix
			)
			tc := framework.NewTestContext()
			openshiftControlPlaneVersionId := framework.DefaultOpenshiftControlPlaneVersionId()
			openshiftNodeVersionId := framework.DefaultOpenshiftNodePoolVersionId()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "external-auth-cluster", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating a customer-infra")
			customerInfraDeploymentResult, err := tc.CreateBicepTemplateAndWait(ctx,
				framework.WithTemplateFromFS(TestArtifactsFS, "test-artifacts/generated-test-artifacts/modules/customer-infra.json"),
				framework.WithDeploymentName("customer-infra"),
				framework.WithScope(framework.BicepDeploymentScopeResourceGroup),
				framework.WithClusterResourceGroup(*resourceGroup.Name),
				framework.WithParameters(map[string]interface{}{
					"persistTagValue":        false,
					"customerNsgName":        customerNetworkSecurityGroupName,
					"customerVnetName":       customerVnetName,
					"customerVnetSubnetName": customerVnetSubnetName,
				}),
				framework.WithTimeout(45*time.Minute),
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating/reusing managed identities")
			keyVaultName, err := framework.GetOutputValue(customerInfraDeploymentResult, "keyVaultName")
			Expect(err).NotTo(HaveOccurred())
			managedIdentityDeploymentResult, err := tc.DeployManagedIdentities(ctx,
				framework.WithTemplateFromFS(TestArtifactsFS, "test-artifacts/generated-test-artifacts/modules/managed-identities.json"),
				framework.WithClusterResourceGroup(*resourceGroup.Name),
				framework.WithParameters(map[string]interface{}{
					"nsgName":      customerNetworkSecurityGroupName,
					"vnetName":     customerVnetName,
					"subnetName":   customerVnetSubnetName,
					"keyVaultName": keyVaultName,
				}),
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating the cluster")
			userAssignedIdentities, err := framework.GetOutputValue(managedIdentityDeploymentResult, "userAssignedIdentitiesValue")
			Expect(err).NotTo(HaveOccurred())
			identity, err := framework.GetOutputValue(managedIdentityDeploymentResult, "identityValue")
			Expect(err).NotTo(HaveOccurred())
			etcdEncryptionKeyName, err := framework.GetOutputValue(customerInfraDeploymentResult, "etcdEncryptionKeyName")
			Expect(err).NotTo(HaveOccurred())
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			_, err = tc.CreateBicepTemplateAndWait(ctx,
				framework.WithTemplateFromFS(TestArtifactsFS, "test-artifacts/generated-test-artifacts/modules/cluster.json"),
				framework.WithDeploymentName("cluster"),
				framework.WithScope(framework.BicepDeploymentScopeResourceGroup),
				framework.WithClusterResourceGroup(*resourceGroup.Name),
				framework.WithParameters(map[string]interface{}{
					"openshiftVersionId":          openshiftControlPlaneVersionId,
					"clusterName":                 customerClusterName,
					"managedResourceGroupName":    managedResourceGroupName,
					"nsgName":                     customerNetworkSecurityGroupName,
					"subnetName":                  customerVnetSubnetName,
					"vnetName":                    customerVnetName,
					"userAssignedIdentitiesValue": userAssignedIdentities,
					"identityValue":               identity,
					"keyVaultName":                keyVaultName,
					"etcdEncryptionKeyName":       etcdEncryptionKeyName,
				}),
				framework.WithTimeout(45*time.Minute),
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

			By("creating the node pool")
			_, err = tc.CreateBicepTemplateAndWait(ctx,
				framework.WithTemplateFromFS(TestArtifactsFS, "test-artifacts/generated-test-artifacts/modules/nodepool.json"),
				framework.WithDeploymentName("node-pool"),
				framework.WithScope(framework.BicepDeploymentScopeResourceGroup),
				framework.WithClusterResourceGroup(*resourceGroup.Name),
				framework.WithParameters(map[string]interface{}{
					"openshiftVersionId": openshiftNodeVersionId,
					"clusterName":        customerClusterName,
					"nodePoolName":       customerNodePoolName,
					"replicas":           2,
				}),
				framework.WithTimeout(45*time.Minute),
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating an app registration with a client secret")
			app, sp, err := tc.NewAppRegistrationWithServicePrincipal(ctx)
			Expect(err).NotTo(HaveOccurred())

			graphClient, err := tc.GetGraphClient(ctx)
			Expect(err).NotTo(HaveOccurred())

			pass, err := graphClient.AddPassword(ctx, app.ID, "external-auth-pass", time.Now(), time.Now().Add(24*time.Hour))
			Expect(err).NotTo(HaveOccurred())

			By("creating an external auth config with a prefix")
			extAuth := hcpsdk20240610preview.ExternalAuth{
				Properties: &hcpsdk20240610preview.ExternalAuthProperties{
					Issuer: &hcpsdk20240610preview.TokenIssuerProfile{
						URL:       to.Ptr(fmt.Sprintf("https://login.microsoftonline.com/%s/v2.0", tc.TenantID())),
						Audiences: []*string{to.Ptr(app.AppID)},
					},
					Claim: &hcpsdk20240610preview.ExternalAuthClaimProfile{
						Mappings: &hcpsdk20240610preview.TokenClaimMappingsProfile{
							Username: &hcpsdk20240610preview.UsernameClaimProfile{
								Claim:        to.Ptr("sub"),                                                 // objectID of SP
								PrefixPolicy: to.Ptr(hcpsdk20240610preview.UsernameClaimPrefixPolicyPrefix), // TODO: ARO-21008 preventing us setting NoPrefix
								Prefix:       to.Ptr(externalAuthSubjectPrefix),
							},
							Groups: &hcpsdk20240610preview.GroupClaimProfile{
								Claim: to.Ptr("groups"),
							},
						},
					},
					Clients: []*hcpsdk20240610preview.ExternalAuthClientProfile{
						{
							ClientID: to.Ptr(app.AppID),
							Component: &hcpsdk20240610preview.ExternalAuthClientComponentProfile{
								Name:                to.Ptr("console"),
								AuthClientNamespace: to.Ptr("openshift-console"),
							},
							Type: to.Ptr(hcpsdk20240610preview.ExternalAuthClientTypeConfidential),
						},
						{
							ClientID: to.Ptr(app.AppID),
							Component: &hcpsdk20240610preview.ExternalAuthClientComponentProfile{
								Name:                to.Ptr("cli"),
								AuthClientNamespace: to.Ptr("openshift-console"),
							},
							Type: to.Ptr(hcpsdk20240610preview.ExternalAuthClientTypePublic),
						},
					},
				},
			}
			_, err = framework.CreateOrUpdateExternalAuthAndWait(ctx, tc.Get20240610ClientFactoryOrDie(ctx).NewExternalAuthsClient(), *resourceGroup.Name, customerClusterName, customerExternalAuthName, extAuth, 15*time.Minute)
			Expect(err).NotTo(HaveOccurred())

			By("verifying ExternalAuth is in a Succeeded state")
			eaResult, err := framework.GetExternalAuth(ctx, tc.Get20240610ClientFactoryOrDie(ctx).NewExternalAuthsClient(), *resourceGroup.Name, customerClusterName, customerExternalAuthName)
			Expect(err).NotTo(HaveOccurred())
			Expect(*eaResult.Properties.ProvisioningState).To(Equal(hcpsdk20240610preview.ExternalAuthProvisioningStateSucceeded))

			By("creating a cluster role binding for the entra application")
			err = framework.CreateClusterRoleBinding(ctx, externalAuthSubjectPrefix+sp.ID, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred())

			By("creating a rest config using OIDC authentication")
			Expect(tc.TenantID()).NotTo(BeEmpty())
			cred, err := azidentity.NewClientSecretCredential(tc.TenantID(), app.AppID, pass.SecretText, nil)
			Expect(err).NotTo(HaveOccurred())

			// MSGraph is eventually consistent, wait up to 2 minutes for the token to be valid
			var accessToken azcore.AccessToken
			Eventually(func() error {
				var err error
				accessToken, err = cred.GetToken(ctx, policy.TokenRequestOptions{
					Scopes: []string{fmt.Sprintf("%s/.default", app.AppID)},
				})

				if err != nil {
					GinkgoWriter.Printf("GetToken failed: %v\n", err)
				}
				return err
			}, 2*time.Minute, 10*time.Second).Should(Succeed())

			config := &rest.Config{
				Host:        adminRESTConfig.Host,
				BearerToken: accessToken.Token,
			}
			client, err := kubernetes.NewForConfig(config)
			Expect(err).NotTo(HaveOccurred())

			// TODO (bvesel): ARO-21634
			// The kube-apiserver restarts on external auth config creation, so we need to wait
			// for it to completely restart. There doesn't appear to be a way to track this in the data plane
			By("confirming we can list namespaces using entra OIDC token")
			Eventually(func() error {
				_, err := client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
				return err
			}, 5*time.Minute, 10*time.Second).Should(Succeed())
		})
})
