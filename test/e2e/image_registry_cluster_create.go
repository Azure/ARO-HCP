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
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/test/util/verifiers"

	hcpapi20240610 "github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Customer", func() {
	BeforeEach(func() {
		// do nothing.  per test initialization usually ages better than shared.
	})

	It("should be able to create an HCP cluster with Image Registry not present",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		func(ctx context.Context) {
			const (
				customerNetworkSecurityGroupName = "customer-nsg-name"
				customerVnetName                 = "customer-vnet-name"
				customerVnetSubnetName           = "customer-vnet-subnet1"
				customerClusterName              = "disabled-image-registry-hcp-cluster"
				openshiftVersionId               = "4.19"
			)
			tc := framework.NewTestContext()

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "disabled-image-registry", "uksouth")
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

			By("creating the hcp cluster with the image registry disabled")
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
				"hcp-cluster",
				framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/image-registry/disabled-image-registry-cluster.json")),
				map[string]interface{}{
					"openshiftVersionId":          openshiftVersionId,
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

			actualHCPCluster, err := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient().Get(ctx, *resourceGroup.Name, customerClusterName, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(actualHCPCluster.Properties.ClusterImageRegistry).NotTo(BeNil())
			Expect(actualHCPCluster.Properties.ClusterImageRegistry.State).NotTo(BeNil())
			Expect(ptr.Deref(actualHCPCluster.Properties.ClusterImageRegistry.State, "")).To(Equal(hcpapi20240610.ClusterImageRegistryProfileStateDisabled))

			By("getting credentials")
			adminRESTConfig, err := framework.GetAdminRESTConfigForHCPCluster(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				10*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig, verifiers.VerifyImageRegistryDisabled())
			Expect(err).NotTo(HaveOccurred())
		})
})
