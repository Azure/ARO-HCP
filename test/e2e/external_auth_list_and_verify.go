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
	"k8s.io/apimachinery/pkg/util/rand"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	hcpsdk "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Customer", func() {
	BeforeEach(func() {
		// do nothing.  per test initialization usually ages better than shared.
	})

	testingPrefix := "ea-list"
	dummyUID := "00000000-0000-0000-0000-000000000000"

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

			expectedExternalAuth := hcpsdk.ExternalAuth{
				Name: to.Ptr(testingPrefix),
				Properties: &hcpsdk.ExternalAuthProperties{
					Issuer: &hcpsdk.TokenIssuerProfile{
						URL:       to.Ptr(fmt.Sprintf("https://login.microsoftonline.com/%s/v2.0", tc.TenantID())),
						Audiences: []*string{to.Ptr(dummyUID)},
					},
					Claim: &hcpsdk.ExternalAuthClaimProfile{
						Mappings: &hcpsdk.TokenClaimMappingsProfile{
							Username: &hcpsdk.UsernameClaimProfile{
								Claim:        to.Ptr("sub"), // objectID of SP
								PrefixPolicy: to.Ptr(hcpsdk.UsernameClaimPrefixPolicyPrefix),
								Prefix:       to.Ptr(testingPrefix),
							},
							Groups: &hcpsdk.GroupClaimProfile{
								Claim: to.Ptr("groups"),
							},
						},
					},
				},
			}

			By("create an external auth and confirm it's in a succeeded state")
			_, err = framework.CreateOrUpdateExternalAuthAndWait(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewExternalAuthsClient(),
				*resourceGroup.Name,
				clusterName,
				*expectedExternalAuth.Name,
				expectedExternalAuth,
				15*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			result, err := framework.GetExternalAuth(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewExternalAuthsClient(),
				*resourceGroup.Name,
				clusterName,
				*expectedExternalAuth.Name,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(*result.Properties.ProvisioningState).To(Equal(hcpsdk.ExternalAuthProvisioningStateSucceeded))

			By("confirming we're only allowed to create a single external auth")
			anotherExternalAuth := expectedExternalAuth
			anotherExternalAuth.Name = to.Ptr(testingPrefix + "2")
			_, err = framework.CreateOrUpdateExternalAuthAndWait(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewExternalAuthsClient(),
				*resourceGroup.Name,
				clusterName,
				*anotherExternalAuth.Name,
				anotherExternalAuth,
				15*time.Minute,
			)
			Expect(err).To(HaveOccurred())

			By("listing all external auth configs to verify a list call works")
			externalAuthClient := tc.Get20240610ClientFactoryOrDie(ctx).NewExternalAuthsClient()
			pager := externalAuthClient.NewListByParentPager(*resourceGroup.Name, clusterName, nil)
			var extAuthResult []hcpsdk.ExternalAuth
			for pager.More() {
				page, err := pager.NextPage(ctx)
				Expect(err).NotTo(HaveOccurred())
				for _, eaPtr := range page.Value {
					if eaPtr != nil {
						extAuthResult = append(extAuthResult, *eaPtr)
					}
				}
			}
			Expect(len(extAuthResult)).To(Equal(1))

			By("comparing ARM results with expected external auth config")
			// Compare core properties
			actual := extAuthResult[0]
			Expect(*actual.Properties.Issuer.URL).To(Equal(*expectedExternalAuth.Properties.Issuer.URL))
			Expect(actual.Properties.Issuer.Audiences).To(Equal(expectedExternalAuth.Properties.Issuer.Audiences))
			Expect(*actual.Properties.Claim.Mappings.Username.Claim).To(Equal(*expectedExternalAuth.Properties.Claim.Mappings.Username.Claim))
			Expect(*actual.Properties.Claim.Mappings.Username.PrefixPolicy).To(Equal(*expectedExternalAuth.Properties.Claim.Mappings.Username.PrefixPolicy))
			Expect(*actual.Properties.Claim.Mappings.Groups.Claim).To(Equal(*expectedExternalAuth.Properties.Claim.Mappings.Groups.Claim))

			// Compare prefix (handle nil case for NoPrefix policy)
			if expectedExternalAuth.Properties.Claim.Mappings.Username.Prefix != nil {
				Expect(actual.Properties.Claim.Mappings.Username.Prefix).NotTo(BeNil())
				Expect(*actual.Properties.Claim.Mappings.Username.Prefix).To(Equal(*expectedExternalAuth.Properties.Claim.Mappings.Username.Prefix))
			} else {
				// Accept either nil or empty string for NoPrefix policy
				Expect(actual.Properties.Claim.Mappings.Username.Prefix).To(
					Or(BeNil(), BeEmpty()),
				)
			}

			By("updating the external auth to a different prefix and confirming the update works")
			expectedExternalAuth.Properties.Claim.Mappings.Username.Prefix = to.Ptr(testingPrefix + "updated")
			_, err = framework.CreateOrUpdateExternalAuthAndWait(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewExternalAuthsClient(),
				*resourceGroup.Name,
				clusterName,
				*expectedExternalAuth.Name,
				expectedExternalAuth,
				15*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			updatedResult, err := framework.GetExternalAuth(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewExternalAuthsClient(),
				*resourceGroup.Name,
				clusterName,
				*expectedExternalAuth.Name,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(*updatedResult.Properties.ProvisioningState).To(Equal(hcpsdk.ExternalAuthProvisioningStateSucceeded))
			Expect(*updatedResult.Properties.Claim.Mappings.Username.Prefix).To(Equal(*expectedExternalAuth.Properties.Claim.Mappings.Username.Prefix))

		})
})
