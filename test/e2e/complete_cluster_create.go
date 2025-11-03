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
	"embed"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

//go:embed test-artifacts
var TestArtifactsFS embed.FS

var _ = Describe("Customer", func() {
	BeforeEach(func() {
		// do nothing.  per test initialization usually ages better than shared.
	})

	It("should be able to create an HCP cluster using bicep templates",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		labels.Local,
		func(ctx context.Context) {
			const (
				customerNetworkSecurityGroupName = "customer-nsg-name"
				customerVnetName                 = "customer-vnet-name"
				customerVnetSubnetName           = "customer-vnet-subnet1"
				customerClusterName              = "basic-hcp-cluster"
				customerNodePoolName             = "np-1"
				openshiftControlPlaneVersionId   = "4.19"
				openshiftNodeVersionId           = "4.19.7"
			)
			tc := framework.NewTestContext()

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "basic-cluster", tc.Location())
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
			keyVaultNameStr, ok := keyVaultName.(string)
			Expect(ok).To(BeTrue())
			etcdEncryptionKeyVersion, err := framework.GetOutputValue(customerInfraDeploymentResult, "etcdEncryptionKeyVersion")
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
			nsgID, err := framework.GetOutputValue(customerInfraDeploymentResult, "nsgID")
			Expect(err).NotTo(HaveOccurred())
			nsgResourceID, ok := nsgID.(string)
			Expect(ok).To(BeTrue())
			vnetSubnetID, err := framework.GetOutputValue(customerInfraDeploymentResult, "vnetSubnetID")
			Expect(err).NotTo(HaveOccurred())
			vnetSubnetResourceID, ok := vnetSubnetID.(string)
			Expect(ok).To(BeTrue())
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams := framework.NewDefaultClusterParams(customerClusterName)
			clusterParams.OpenshiftVersionId = openshiftControlPlaneVersionId
			clusterParams.ManagedResourceGroupName = managedResourceGroupName
			clusterParams.NsgResourceID = nsgResourceID
			clusterParams.SubnetResourceID = vnetSubnetResourceID
			clusterParams.VnetName = customerVnetName
			clusterParams.UserAssignedIdentitiesProfile, err = framework.ConvertToUserAssignedIdentitiesProfile(userAssignedIdentities)
			Expect(err).NotTo(HaveOccurred())
			clusterParams.Identity, err = framework.ConvertToManagedServiceIdentity(identity)
			Expect(err).NotTo(HaveOccurred())
			clusterParams.EncryptionKeyManagementMode = "CustomerManaged"
			clusterParams.EncryptionType = "KMS"
			clusterParams.KeyVaultName = keyVaultNameStr
			clusterParams.EtcdEncryptionKeyName, ok = etcdEncryptionKeyName.(string)
			Expect(ok).To(BeTrue())
			clusterParams.EtcdEncryptionKeyVersion, ok = etcdEncryptionKeyVersion.(string)
			Expect(ok).To(BeTrue())
			clusterParams.UserAssignedIdentitiesProfile, err = framework.ConvertToUserAssignedIdentitiesProfile(userAssignedIdentities)
			Expect(err).NotTo(HaveOccurred())
			clusterParams.Identity, err = framework.ConvertToManagedServiceIdentity(identity)
			Expect(err).NotTo(HaveOccurred())

			err = framework.CreateHCPClusterFromParam(ctx,
				tc,
				*resourceGroup.Name,
				clusterParams,
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
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred())

			By("creating the node pool")
			nodePoolParams := framework.NewDefaultNodePoolParams(customerClusterName, customerNodePoolName)
			nodePoolParams.OpenshiftVersionId = openshiftNodeVersionId
			nodePoolParams.Replicas = int32(2)

			err = framework.CreateNodePoolFromParam(ctx,
				tc,
				*resourceGroup.Name,
				customerClusterName,
				nodePoolParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())
			By("verifying a simple web app can run")
			err = verifiers.VerifySimpleWebApp().Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred())
		})
})
