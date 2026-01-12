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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Customer", func() {
	BeforeEach(func() {
		// do nothing.  per test initialization usually ages better than shared.
	})

	It("should be able to create several HCP clusters in their customer resource group, but not in the same managed resource group",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		func(ctx context.Context) {
			const (
				customerNetworkSecurityGroupName = "customer-nsg-name"
				customerVnetName                 = "customer-vnet-name"
				customerVnetSubnetName           = "customer-vnet-subnet1"
				customerClusterName              = "basic-hcp-cluster"

				customerNetworkSecurityGroupName2 = "customer-nsg2-name"
				customerVnetName2                 = "customer-vnet2-name"
				customerVnetSubnetName2           = "customer-vnet-subnet2"
				customerClusterName2              = "basic-hcp-cluster2"

				customerNetworkSecurityGroupName3 = "customer-nsg3-name"
				customerVnetName3                 = "customer-vnet3-name"
				customerVnetSubnetName3           = "customer-vnet-subnet3"
				customerClusterName3              = "basic-hcp-cluster3"
			)
			tc := framework.NewTestContext()
			openshiftControlPlaneVersionId := framework.DefaultOpenshiftControlPlaneVersionId()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 3, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			By("creating a managed customer resource group")
			customerResourceGroup, err := tc.NewResourceGroup(ctx, "customer-rg", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating a customer-infra")
			customerInfraDeploymentResult, err := tc.CreateBicepTemplateAndWait(ctx,
				framework.WithTemplateFromFS(TestArtifactsFS, "test-artifacts/generated-test-artifacts/modules/customer-infra.json"),
				framework.WithDeploymentName("customer-infra"),
				framework.WithScope(framework.BicepDeploymentScopeResourceGroup),
				framework.WithClusterResourceGroup(*customerResourceGroup.Name),
				framework.WithParameters(map[string]interface{}{
					"persistTagValue":        false,
					"customerNsgName":        customerNetworkSecurityGroupName,
					"customerVnetName":       customerVnetName,
					"customerVnetSubnetName": customerVnetSubnetName,
				}),
				framework.WithTimeout(45*time.Minute),
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating/reusing managed identities for first cluster")
			keyVaultName, err := framework.GetOutputValue(customerInfraDeploymentResult, "keyVaultName")
			Expect(err).NotTo(HaveOccurred())
			managedIdentityDeploymentResult, err := tc.DeployManagedIdentities(ctx,
				framework.WithTemplateFromFS(TestArtifactsFS, "test-artifacts/generated-test-artifacts/modules/managed-identities.json"),
				framework.WithDeploymentName("mi-1-"+*customerResourceGroup.Name),
				framework.WithClusterResourceGroup(*customerResourceGroup.Name),
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
			managedResourceGroupName := framework.SuffixName(*customerResourceGroup.Name, "-managed", 64)
			_, err = tc.CreateBicepTemplateAndWait(ctx,
				framework.WithTemplateFromFS(TestArtifactsFS, "test-artifacts/generated-test-artifacts/modules/cluster.json"),
				framework.WithDeploymentName("cluster"),
				framework.WithScope(framework.BicepDeploymentScopeResourceGroup),
				framework.WithClusterResourceGroup(*customerResourceGroup.Name),
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

			By("creating a second customer-infra")
			customerInfraDeploymentResult, err = tc.CreateBicepTemplateAndWait(ctx,
				framework.WithTemplateFromFS(TestArtifactsFS, "test-artifacts/generated-test-artifacts/modules/customer-infra.json"),
				framework.WithDeploymentName("customer-infra"),
				framework.WithScope(framework.BicepDeploymentScopeResourceGroup),
				framework.WithClusterResourceGroup(*customerResourceGroup.Name),
				framework.WithParameters(map[string]interface{}{
					"persistTagValue":        false,
					"customerNsgName":        customerNetworkSecurityGroupName2,
					"customerVnetName":       customerVnetName2,
					"customerVnetSubnetName": customerVnetSubnetName2,
				}),
				framework.WithTimeout(45*time.Minute),
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating/reusing managed identities for second cluster")
			keyVaultName, err = framework.GetOutputValue(customerInfraDeploymentResult, "keyVaultName")
			Expect(err).NotTo(HaveOccurred())
			managedIdentityDeploymentResult, err = tc.DeployManagedIdentities(ctx,
				framework.WithTemplateFromFS(TestArtifactsFS, "test-artifacts/generated-test-artifacts/modules/managed-identities.json"),
				framework.WithClusterResourceGroup(*customerResourceGroup.Name),
				framework.WithDeploymentName("mi-2-"+*customerResourceGroup.Name),
				framework.WithParameters(map[string]interface{}{
					"nsgName":      customerNetworkSecurityGroupName2,
					"vnetName":     customerVnetName2,
					"subnetName":   customerVnetSubnetName2,
					"keyVaultName": keyVaultName,
				}),
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating the second cluster")
			userAssignedIdentities, err = framework.GetOutputValue(managedIdentityDeploymentResult, "userAssignedIdentitiesValue")
			Expect(err).NotTo(HaveOccurred())
			identity, err = framework.GetOutputValue(managedIdentityDeploymentResult, "identityValue")
			Expect(err).NotTo(HaveOccurred())
			etcdEncryptionKeyName, err = framework.GetOutputValue(customerInfraDeploymentResult, "etcdEncryptionKeyName")
			Expect(err).NotTo(HaveOccurred())
			managedResourceGroupName = framework.SuffixName(*customerResourceGroup.Name, "-managed-2", 64)
			_, err = tc.CreateBicepTemplateAndWait(ctx,
				framework.WithTemplateFromFS(TestArtifactsFS, "test-artifacts/generated-test-artifacts/modules/cluster.json"),
				framework.WithDeploymentName("cluster-2"),
				framework.WithScope(framework.BicepDeploymentScopeResourceGroup),
				framework.WithClusterResourceGroup(*customerResourceGroup.Name),
				framework.WithParameters(map[string]interface{}{
					"openshiftVersionId":          openshiftControlPlaneVersionId,
					"clusterName":                 customerClusterName2,
					"managedResourceGroupName":    managedResourceGroupName,
					"nsgName":                     customerNetworkSecurityGroupName2,
					"subnetName":                  customerVnetSubnetName2,
					"vnetName":                    customerVnetName2,
					"userAssignedIdentitiesValue": userAssignedIdentities,
					"identityValue":               identity,
					"keyVaultName":                keyVaultName,
					"etcdEncryptionKeyName":       etcdEncryptionKeyName,
				}),
				framework.WithTimeout(45*time.Minute),
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating a third customer-infra")
			customerInfraDeploymentResult, err = tc.CreateBicepTemplateAndWait(ctx,
				framework.WithTemplateFromFS(TestArtifactsFS, "test-artifacts/generated-test-artifacts/modules/customer-infra.json"),
				framework.WithDeploymentName("customer-infra"),
				framework.WithScope(framework.BicepDeploymentScopeResourceGroup),
				framework.WithClusterResourceGroup(*customerResourceGroup.Name),
				framework.WithParameters(map[string]interface{}{
					"persistTagValue":        false,
					"customerNsgName":        customerNetworkSecurityGroupName3,
					"customerVnetName":       customerVnetName3,
					"customerVnetSubnetName": customerVnetSubnetName3,
				}),
				framework.WithTimeout(45*time.Minute),
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating/reusing managed identities for third cluster")
			keyVaultName, err = framework.GetOutputValue(customerInfraDeploymentResult, "keyVaultName")
			Expect(err).NotTo(HaveOccurred())
			managedIdentityDeploymentResult, err = tc.DeployManagedIdentities(ctx,
				framework.WithTemplateFromFS(TestArtifactsFS, "test-artifacts/generated-test-artifacts/modules/managed-identities.json"),
				framework.WithClusterResourceGroup(*customerResourceGroup.Name),
				framework.WithDeploymentName("mi-3-"+*customerResourceGroup.Name),
				framework.WithParameters(map[string]interface{}{
					"nsgName":      customerNetworkSecurityGroupName3,
					"vnetName":     customerVnetName3,
					"subnetName":   customerVnetSubnetName3,
					"keyVaultName": keyVaultName,
				}),
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating a third cluster in the same managed resource group as the previous one")
			userAssignedIdentities, err = framework.GetOutputValue(managedIdentityDeploymentResult, "userAssignedIdentitiesValue")
			Expect(err).NotTo(HaveOccurred())
			identity, err = framework.GetOutputValue(managedIdentityDeploymentResult, "identityValue")
			Expect(err).NotTo(HaveOccurred())
			etcdEncryptionKeyName, err = framework.GetOutputValue(customerInfraDeploymentResult, "etcdEncryptionKeyName")
			Expect(err).NotTo(HaveOccurred())
			_, err = tc.CreateBicepTemplateAndWait(ctx,
				framework.WithTemplateFromFS(TestArtifactsFS, "test-artifacts/generated-test-artifacts/modules/cluster.json"),
				framework.WithDeploymentName("cluster-3"),
				framework.WithScope(framework.BicepDeploymentScopeResourceGroup),
				framework.WithClusterResourceGroup(*customerResourceGroup.Name),
				framework.WithParameters(map[string]interface{}{
					"openshiftVersionId": openshiftControlPlaneVersionId,
					"clusterName":        customerClusterName3,
					// Here we're using the managed resource group from the previous cluster
					"managedResourceGroupName":    managedResourceGroupName,
					"nsgName":                     customerNetworkSecurityGroupName3,
					"subnetName":                  customerVnetSubnetName3,
					"vnetName":                    customerVnetName3,
					"userAssignedIdentitiesValue": userAssignedIdentities,
					"identityValue":               identity,
					"keyVaultName":                keyVaultName,
					"etcdEncryptionKeyName":       etcdEncryptionKeyName,
				}),
				framework.WithTimeout(45*time.Minute),
			)
			Expect(err).To(HaveOccurred())
			Expect(err).To(MatchError(MatchRegexp("please provide a unique managed resource group name")))

			By("Checking that the managed resource group still exists")
			_, err = tc.GetARMResourcesClientFactoryOrDie(ctx).NewResourceGroupsClient().Get(ctx, managedResourceGroupName, nil)
			Expect(err).NotTo(HaveOccurred())
		})
})
