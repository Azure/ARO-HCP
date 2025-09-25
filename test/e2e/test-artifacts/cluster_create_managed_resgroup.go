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

	It("should not be able to create a second HCP cluster in a managed customer resource group",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		func(ctx context.Context) {
			const (
				customerNetworkSecurityGroupName = "customer-nsg-name"
				customerVnetName                 = "customer-vnet-name"
				customerVnetSubnetName           = "customer-vnet-subnet1"
				customerClusterName              = "basic-hcp-cluster"
				openshiftControlPlaneVersionId   = "4.19"

				customerNetworkSecurityGroupName2 = "customer-nsg-name2"
				customerVnetName2                 = "customer-vnet-name2"
				customerVnetSubnetName2           = "customer-vnet-subnet12"
				customerClusterName2              = "basic-hcp-cluster2"
			)
			tc := framework.NewTestContext()

			By("creating a managed customer resource group")
			customerResourceGroup, err := tc.NewResourceGroup(ctx, "customer-rg", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating a customer-infra")
			customerInfraDeploymentResult, err := framework.CreateBicepTemplateAndWait(ctx,
				tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				*customerResourceGroup.Name,
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
				*customerResourceGroup.Name,
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
			managedResourceGroupName := framework.SuffixName(*customerResourceGroup.Name, "-managed", 64)
			_, err = framework.CreateBicepTemplateAndWait(ctx,
				tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				*customerResourceGroup.Name,
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

			// Try to create another cluster in the same resource group
			By("creating a second customer-infra")
			customerInfraDeploymentResult, err = framework.CreateBicepTemplateAndWait(ctx,
				tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				*customerResourceGroup.Name,
				"customer-infra-2",
				framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/modules/customer-infra.json")),
				map[string]interface{}{
					"persistTagValue":        false,
					"customerNsgName":        customerNetworkSecurityGroupName2,
					"customerVnetName":       customerVnetName2,
					"customerVnetSubnetName": customerVnetSubnetName2,
				},
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating a second managed identities")
			keyVaultName, err = framework.GetOutputValue(customerInfraDeploymentResult, "keyVaultName")
			Expect(err).NotTo(HaveOccurred())
			managedIdentityDeploymentResult, err = framework.CreateBicepTemplateAndWait(ctx,
				tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				*customerResourceGroup.Name,
				"managed-identities-2",
				framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/modules/managed-identities.json")),
				map[string]interface{}{
					"clusterName":  customerClusterName2,
					"nsgName":      customerNetworkSecurityGroupName2,
					"vnetName":     customerVnetName2,
					"subnetName":   customerVnetSubnetName2,
					"keyVaultName": keyVaultName,
				},
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating the second cluster")
			userAssignedIdentities, err = framework.GetOutputValue(managedIdentityDeploymentResult, "userAssignedIdentitiesValue")
			Expect(err).NotTo(HaveOccurred())
			identity, err = framework.GetOutputValue(managedIdentityDeploymentResult, "identityValue")
			Expect(err).NotTo(HaveOccurred())
			etcdEncryptionKeyName, err = framework.GetOutputValue(customerInfraDeploymentResult, "etcdEncryptionKeyName")
			Expect(err).NotTo(HaveOccurred())
			managedResourceGroupName = framework.SuffixName(*customerResourceGroup.Name, "-managed", 64)
			_, err = framework.CreateBicepTemplateAndWait(ctx,
				tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				*customerResourceGroup.Name,
				"cluster-2",
				framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/modules/cluster.json")),
				map[string]interface{}{
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
				},
				45*time.Minute,
			)
			Expect(err).To(HaveOccurred())
		})
})
