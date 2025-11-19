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

	for _, version := range []string{
		"4.18",
		// TODO add other disabled versions here.
	} {
		It("should not be able to create a "+version+" HCP cluster",
			labels.RequireNothing,
			labels.Critical,
			labels.Negative,
			func(ctx context.Context) {
				const (
					customerNetworkSecurityGroupName = "customer-nsg-name"
					customerVnetName                 = "customer-vnet-name"
					customerVnetSubnetName           = "customer-vnet-subnet1"
					customerClusterName              = "illegal-hcp-cluster"
				)
				tc := framework.NewTestContext()

				if tc.UsePooledIdentities() {
					err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
					Expect(err).NotTo(HaveOccurred())
				}

				By("creating a resource group")
				resourceGroup, err := tc.NewResourceGroup(ctx, "illegal-ocp-version", tc.Location())
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

				By("creating the hcp cluster")
				userAssignedIdentities, err := framework.GetOutputValue(managedIdentityDeploymentResult, "userAssignedIdentitiesValue")
				Expect(err).NotTo(HaveOccurred())
				identity, err := framework.GetOutputValue(managedIdentityDeploymentResult, "identityValue")
				Expect(err).NotTo(HaveOccurred())
				etcdEncryptionKeyName, err := framework.GetOutputValue(customerInfraDeploymentResult, "etcdEncryptionKeyName")
				Expect(err).NotTo(HaveOccurred())
				managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "managed", 64)
				_, err = tc.CreateBicepTemplateAndWait(ctx,
					framework.WithTemplateFromFS(TestArtifactsFS, "test-artifacts/generated-test-artifacts/modules/cluster.json"),
					framework.WithDeploymentName("cluster"),
					framework.WithScope(framework.BicepDeploymentScopeResourceGroup),
					framework.WithClusterResourceGroup(*resourceGroup.Name),
					framework.WithParameters(map[string]interface{}{
						"openshiftVersionId":          version,
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
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError(MatchRegexp("Version .* (doesn't exist|is disabled)")))
			},
		)
	}
})
