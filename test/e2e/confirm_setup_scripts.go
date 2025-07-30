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

	It("should be able to use the setup scripts to create a cluster and nodepool",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		func(ctx context.Context) {
			const (
				customerNetworkSecurityGroupName = "customer-nsg-name"
				customerVnetName                 = "customer-vnet-name"
				customerVnetSubnetName           = "customer-vnet-subnet1"
				customerClusterName              = "basic-hcp-cluster"
				customerNodePoolName             = "np-1"
				openshiftVersionId               = "openshift-v4.19.0"
			)
			ic := framework.NewInvocationContext()

			By("creating a resource group")
			resourceGroup, err := ic.NewResourceGroup(ctx, "setup-scripts", "uksouth")
			Expect(err).NotTo(HaveOccurred())

			By("creating a customer-infra")
			infraDeploymentResult, err := framework.CreateBicepTemplateAndWait(ctx,
				ic.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				*resourceGroup.Name,
				"customer-infra",
				framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/setup-scripts/customer-infra.json")),
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
			managedIdentityDeploymentResult, err := framework.CreateBicepTemplateAndWait(ctx,
				ic.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				*resourceGroup.Name,
				"managed-identities",
				framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/setup-scripts/managed-identities.json")),
				map[string]interface{}{
					"clusterName": customerClusterName,
				},
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating the cluster")
			networkSecurityGroupID, err := framework.GetOutputValueString(infraDeploymentResult, "networkSecurityGroupId")
			Expect(err).NotTo(HaveOccurred())
			subnetID, err := framework.GetOutputValueString(infraDeploymentResult, "subnetId")
			Expect(err).NotTo(HaveOccurred())
			userAssignedIdentities, err := framework.GetOutputValue(managedIdentityDeploymentResult, "userAssignedIdentitiesValue")
			Expect(err).NotTo(HaveOccurred())
			identity, err := framework.GetOutputValue(managedIdentityDeploymentResult, "identityValue")
			Expect(err).NotTo(HaveOccurred())
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			_, err = framework.CreateBicepTemplateAndWait(ctx,
				ic.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				*resourceGroup.Name,
				"cluster",
				framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/setup-scripts/cluster.json")),
				map[string]interface{}{
					"openshiftVersionId":          openshiftVersionId,
					"clusterName":                 customerClusterName,
					"managedResourceGroupName":    managedResourceGroupName,
					"networkSecurityGroupId":      networkSecurityGroupID,
					"subnetId":                    subnetID,
					"userAssignedIdentitiesValue": userAssignedIdentities,
					"identityValue":               identity,
				},
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating the node pool")
			_, err = framework.CreateBicepTemplateAndWait(ctx,
				ic.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				*resourceGroup.Name,
				"node-pool",
				framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/setup-scripts/nodepool.json")),
				map[string]interface{}{
					"openshiftVersionId": openshiftVersionId,
					"clusterName":        customerClusterName,
					"nodePoolName":       customerNodePoolName,
					"replicas":           2,
				},
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())
		})
})
