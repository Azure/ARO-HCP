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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	"github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
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
				openshiftControlPlaneVersionId   = "4.19"
				openshiftNodeVersionId           = "4.19.7"
				customerExternalAuthName         = "external-auth"
			)
			tc := framework.NewTestContext()

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "external-auth-cluster", "uksouth")
			Expect(err).NotTo(HaveOccurred())

			By("creating a customer-infra")
			customerInfraDeploymentResult, err := framework.CreateBicepTemplateAndWait(ctx,
				tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				*resourceGroup.Name,
				"customer-infra",
				framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/modules/customer-infra.json")),
				map[string]interface{}{
					"persistTagValue":        false,
					"customerNsgName":        customerNetworkSecurityGroupName,
					"customerVnetName":       customerVnetName,
					"customerVnetSubnetName": customerVnetSubnetName,
				},
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating a managed identities")
			keyVaultName, err := framework.GetOutputValue(customerInfraDeploymentResult, "keyVaultName")
			Expect(err).NotTo(HaveOccurred())
			managedIdentityDeploymentResult, err := framework.CreateBicepTemplateAndWait(ctx,
				tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				*resourceGroup.Name,
				"managed-identities",
				framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/modules/managed-identities.json")),
				map[string]interface{}{
					"clusterName":  customerClusterName,
					"nsgName":      customerNetworkSecurityGroupName,
					"vnetName":     customerVnetName,
					"subnetName":   customerVnetSubnetName,
					"keyVaultName": keyVaultName,
				},
				45*time.Minute,
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
			_, err = framework.CreateBicepTemplateAndWait(ctx,
				tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				*resourceGroup.Name,
				"cluster",
				framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/modules/cluster.json")),
				map[string]interface{}{
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
				},
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("getting credentials")
			adminRESTConfig, err := framework.GetAdminRESTConfigForHCPCluster(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				10*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("ensuring the cluster is viable")
			err = framework.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred())

			By("creating the node pool")
			_, err = framework.CreateBicepTemplateAndWait(ctx,
				tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				*resourceGroup.Name,
				"node-pool",
				framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/modules/nodepool.json")),
				map[string]interface{}{
					"openshiftVersionId": openshiftNodeVersionId,
					"clusterName":        customerClusterName,
					"nodePoolName":       customerNodePoolName,
					"replicas":           2,
				},
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating an app registration with a client secret")
			app, err := tc.NewAppRegistration(ctx)
			Expect(err).NotTo(HaveOccurred())

			graphClient, err := tc.GetGraphClient(ctx)
			Expect(err).NotTo(HaveOccurred())

			_, err = graphClient.AddPassword(ctx, app.ID, "external-auth-pass", time.Now(), time.Now().Add(24*time.Hour))
			Expect(err).NotTo(HaveOccurred())

			By("creating an external auth config")
			extAuth := generated.ExternalAuth{
				Properties: &generated.ExternalAuthProperties{
					Issuer: &generated.TokenIssuerProfile{
						URL:       to.Ptr(fmt.Sprintf("https://login.microsoftonline.com/%s/v2.0", tc.TenantID())),
						Audiences: []*string{to.Ptr(app.AppID)},
					},
					Claim: &generated.ExternalAuthClaimProfile{
						Mappings: &generated.TokenClaimMappingsProfile{
							Username: &generated.UsernameClaimProfile{
								Claim: to.Ptr("email"),
							},
							Groups: &generated.GroupClaimProfile{
								Claim: to.Ptr("groups"),
							},
						},
					},
					// TODO: ARO-20830 - External Auth Client types are meant to be lowercase it appears
					// Clients: []*generated.ExternalAuthClientProfile{
					// 	{
					// 		ClientID: to.Ptr(app.ID),
					// 		Component: &generated.ExternalAuthClientComponentProfile{
					// 			Name:                to.Ptr("console"),
					// 			AuthClientNamespace: to.Ptr("openshift-console"),
					// 		},
					// 		Type: to.Ptr(generated.ExternalAuthClientTypeConfidential),
					// 	},
					// 	{
					// 		ClientID: to.Ptr(app.AppID),
					// 		Component: &generated.ExternalAuthClientComponentProfile{
					// 			Name:                to.Ptr("cli"),
					// 			AuthClientNamespace: to.Ptr("openshift-console"),
					// 		},
					// 		Type: to.Ptr(generated.ExternalAuthClientTypePublic),
					// 	},
					// },
				},
			}

			_, err = framework.CreateExternalAuthAndWait(ctx, tc.Get20240610ClientFactoryOrDie(ctx).NewExternalAuthsClient(), *resourceGroup.Name, customerClusterName, customerExternalAuthName, extAuth, 15*time.Minute)
			Expect(err).NotTo(HaveOccurred())

			By("verifying ExternalAuth via GET")
			_, err = framework.GetExternalAuth(ctx, tc.Get20240610ClientFactoryOrDie(ctx).NewExternalAuthsClient(), *resourceGroup.Name, customerClusterName, customerExternalAuthName, 5*time.Minute)
			Expect(err).NotTo(HaveOccurred())

		})
})
