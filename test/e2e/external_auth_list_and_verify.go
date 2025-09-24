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
	"encoding/json"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	configclient "github.com/openshift/client-go/config/clientset/versioned"

	"github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Customer", func() {
	BeforeEach(func() {
		// do nothing.  per test initialization usually ages better than shared.
	})

	testingPrefix := "ea-list-"
	dummyUID := "00000000-0000-0000-0000-000000000000"
	dummyUID2 := "11111111-1111-1111-1111-111111111111"
	externalAuthName := "external-auth"
	externalAuthName2 := "external-auth-2"

	It("should be able to lifecycle and confirm external auth on a cluster",
		labels.RequireNothing, labels.High, labels.Positive,
		func(ctx context.Context) {
			clusterName := testingPrefix + rand.String(6)
			tc := framework.NewTestContext()

			By("creating resource group for the HCP cluster")
			resourceGroup, err := tc.NewResourceGroup(ctx, testingPrefix, tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("starting cluster-only template deployment")
			deploymentsClient := tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient()

			// Prepare the template and parameters
			templateBytes := framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/cluster-only.json"))
			bicepTemplateMap := map[string]interface{}{}
			err = json.Unmarshal(templateBytes, &bicepTemplateMap)
			Expect(err).NotTo(HaveOccurred())

			bicepParameters := map[string]interface{}{
				"clusterName": map[string]interface{}{
					"value": clusterName,
				},
			}

			// Create ARO HCP cluster
			deploymentCtx, deploymentCancel := context.WithTimeout(ctx, 45*time.Minute)
			defer deploymentCancel()

			deploymentResp, err := deploymentsClient.BeginCreateOrUpdate(
				deploymentCtx,
				*resourceGroup.Name,
				clusterName,
				armresources.Deployment{
					Properties: &armresources.DeploymentProperties{
						Template:   bicepTemplateMap,
						Parameters: bicepParameters,
						Mode:       to.Ptr(armresources.DeploymentModeIncremental),
					},
				},
				nil,
			)
			Expect(err).NotTo(HaveOccurred())

			_, err = deploymentResp.PollUntilDone(ctx, nil)
			Expect(err).NotTo(HaveOccurred())

			By("creating an external auth config with a prefix")
			extAuth := generated.ExternalAuth{
				Properties: &generated.ExternalAuthProperties{
					Issuer: &generated.TokenIssuerProfile{
						URL:       to.Ptr(fmt.Sprintf("https://login.microsoftonline.com/%s/v2.0", tc.TenantID())),
						Audiences: []*string{to.Ptr(dummyUID)},
					},
					Claim: &generated.ExternalAuthClaimProfile{
						Mappings: &generated.TokenClaimMappingsProfile{
							Username: &generated.UsernameClaimProfile{
								Claim:        to.Ptr("sub"), // objectID of SP
								PrefixPolicy: to.Ptr(generated.UsernameClaimPrefixPolicyPrefix),
								Prefix:       to.Ptr(testingPrefix),
							},
							Groups: &generated.GroupClaimProfile{
								Claim: to.Ptr("groups"),
							},
						},
					},
					Clients: []*generated.ExternalAuthClientProfile{
						{
							ClientID: to.Ptr(dummyUID),
							Component: &generated.ExternalAuthClientComponentProfile{
								Name:                to.Ptr("console"),
								AuthClientNamespace: to.Ptr("openshift-console"),
							},
							Type: to.Ptr(generated.ExternalAuthClientTypeConfidential),
						},
						{
							ClientID: to.Ptr(dummyUID),
							Component: &generated.ExternalAuthClientComponentProfile{
								Name:                to.Ptr("cli"),
								AuthClientNamespace: to.Ptr("openshift-console"),
							},
							Type: to.Ptr(generated.ExternalAuthClientTypePublic),
						},
					},
				},
			}
			_, err = framework.CreateOrUpdateExternalAuthAndWait(ctx, tc.Get20240610ClientFactoryOrDie(ctx).NewExternalAuthsClient(), *resourceGroup.Name, clusterName, externalAuthName, extAuth, 15*time.Minute)
			Expect(err).NotTo(HaveOccurred())

			By("verifying ExternalAuth is in a Succeeded state")
			eaResult, err := framework.GetExternalAuth(ctx, tc.Get20240610ClientFactoryOrDie(ctx).NewExternalAuthsClient(), *resourceGroup.Name, clusterName, externalAuthName)
			Expect(err).NotTo(HaveOccurred())
			Expect(*eaResult.Properties.ProvisioningState).To(Equal(generated.ExternalAuthProvisioningStateSucceeded))

			By("creating a second external auth config with different audience and NoPrefix policy")
			extAuth2 := generated.ExternalAuth{
				Properties: &generated.ExternalAuthProperties{
					Issuer: &generated.TokenIssuerProfile{
						URL:       to.Ptr(fmt.Sprintf("https://login.microsoftonline.com/%s/v2.0", tc.TenantID())),
						Audiences: []*string{to.Ptr(dummyUID2)},
					},
					Claim: &generated.ExternalAuthClaimProfile{
						Mappings: &generated.TokenClaimMappingsProfile{
							Username: &generated.UsernameClaimProfile{
								Claim:        to.Ptr("preferred_username"),
								PrefixPolicy: to.Ptr(generated.UsernameClaimPrefixPolicyNoPrefix),
							},
							Groups: &generated.GroupClaimProfile{
								Claim: to.Ptr("groups"),
							},
						},
					},
					Clients: []*generated.ExternalAuthClientProfile{
						{
							ClientID: to.Ptr(dummyUID2),
							Component: &generated.ExternalAuthClientComponentProfile{
								Name:                to.Ptr("console"),
								AuthClientNamespace: to.Ptr("openshift-console"),
							},
							Type: to.Ptr(generated.ExternalAuthClientTypeConfidential),
						},
					},
				},
			}
			_, err = framework.CreateOrUpdateExternalAuthAndWait(ctx, tc.Get20240610ClientFactoryOrDie(ctx).NewExternalAuthsClient(), *resourceGroup.Name, clusterName, externalAuthName2, extAuth2, 15*time.Minute)
			Expect(err).NotTo(HaveOccurred())

			By("verifying second ExternalAuth is in a Succeeded state")
			eaResult2, err := framework.GetExternalAuth(ctx, tc.Get20240610ClientFactoryOrDie(ctx).NewExternalAuthsClient(), *resourceGroup.Name, clusterName, externalAuthName2)
			Expect(err).NotTo(HaveOccurred())
			Expect(*eaResult2.Properties.ProvisioningState).To(Equal(generated.ExternalAuthProvisioningStateSucceeded))

			By("listing all external auth configs to verify both exist")
			externalAuthClient := tc.Get20240610ClientFactoryOrDie(ctx).NewExternalAuthsClient()
			pager := externalAuthClient.NewListByParentPager(*resourceGroup.Name, clusterName, nil)
			var allExternalAuths []generated.ExternalAuth
			for pager.More() {
				page, err := pager.NextPage(ctx)
				Expect(err).NotTo(HaveOccurred())
				for _, eaPtr := range page.ExternalAuthListResult.Value {
					if eaPtr != nil {
						allExternalAuths = append(allExternalAuths, *eaPtr)
					}
				}
			}
			Expect(len(allExternalAuths)).To(Equal(2))

			// Verify the external auth configs by name
			var foundEA1, foundEA2 bool
			for _, ea := range allExternalAuths {
				if *ea.Name == externalAuthName {
					foundEA1 = true
					Expect(*ea.Properties.ProvisioningState).To(Equal(generated.ExternalAuthProvisioningStateSucceeded))
					Expect(*ea.Properties.Issuer.Audiences[0]).To(Equal(dummyUID))
					Expect(*ea.Properties.Claim.Mappings.Username.PrefixPolicy).To(Equal(generated.UsernameClaimPrefixPolicyPrefix))
					Expect(*ea.Properties.Claim.Mappings.Username.Prefix).To(Equal(testingPrefix))
				} else if *ea.Name == externalAuthName2 {
					foundEA2 = true
					Expect(*ea.Properties.ProvisioningState).To(Equal(generated.ExternalAuthProvisioningStateSucceeded))
					Expect(*ea.Properties.Issuer.Audiences[0]).To(Equal(dummyUID2))
					Expect(*ea.Properties.Claim.Mappings.Username.PrefixPolicy).To(Equal(generated.UsernameClaimPrefixPolicyNoPrefix))
					Expect(ea.Properties.Claim.Mappings.Username.Prefix).To(BeNil())
				}
			}
			Expect(foundEA1).To(BeTrue(), "First external auth config not found")
			Expect(foundEA2).To(BeTrue(), "Second external auth config not found")

			By("verifying the external auth configs match the in-cluster authentication CR")
			adminRESTConfig, err := framework.GetAdminRESTConfigForHCPCluster(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				clusterName,
				10*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(adminRESTConfig).NotTo(BeNil())

			configCli, err := configclient.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred())

			authList, err := configCli.ConfigV1().Authentications().List(ctx, metav1.ListOptions{})
			Expect(err).NotTo(HaveOccurred())

			Expect(len(authList.Items)).To(Equal(1))
			auth := authList.Items[0]
			Expect(auth.Spec.Type).To(Equal("OIDC"))

			// Verify we have the expected number of OIDC providers
			Expect(len(auth.Spec.OpenID.OIDCProviders)).To(Equal(2))

			// Verify both external auth configs are represented in the authentication CR
			var foundProvider1, foundProvider2 bool
			for _, provider := range auth.Spec.OpenID.OIDCProviders {
				if provider.Issuer.URL == fmt.Sprintf("https://login.microsoftonline.com/%s/v2.0", tc.TenantID()) {
					for _, audience := range provider.Issuer.Audiences {
						if audience == dummyUID {
							foundProvider1 = true
							Expect(provider.UsernameClaim).To(Equal("sub"))
							Expect(provider.UsernamePrefix).To(Equal(testingPrefix))
							Expect(provider.GroupsClaim).To(Equal("groups"))
						} else if audience == dummyUID2 {
							foundProvider2 = true
							Expect(provider.UsernameClaim).To(Equal("preferred_username"))
							Expect(provider.UsernamePrefix).To(Equal("")) // NoPrefix means empty prefix
							Expect(provider.GroupsClaim).To(Equal("groups"))
						}
					}
				}
			}
			Expect(foundProvider1).To(BeTrue(), "First OIDC provider not found in authentication CR")
			Expect(foundProvider2).To(BeTrue(), "Second OIDC provider not found in authentication CR")
		})
})
