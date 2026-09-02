// Copyright 2026 Microsoft Corporation
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
	"errors"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

// This test is a regression test for AROSLSRE-1167 / AROSLSRE-1709: a
// HostedCluster deletion deadlocked on prod-uksouth-mgmt-1 after the
// cluster_delete_cx_rg test (see cluster_delete_cx_rg.go) deleted the
// customer resource group, which also removed the etcd KMS Key Vault living
// inside it. With the guest API permanently unavailable, the CAPZ
// availability-prober blocked the capi-provider manager from ever starting,
// so AzureMachine finalizers could never clear and deletion hung until an SRE
// manually removed the finalizers. cluster_delete_cx_rg must keep validating
// the intended RG-deletion behavior, so this test isolates just the
// confirmed root cause (the KMS Key Vault disappearing) without touching the
// customer VNet/subnet, to directly verify the HyperShift escape-hatch fix
// (DeleteOrphanedMachines/capiProviderUnavailable) recovers automatically.
var _ = Describe("Customer", func() {
	It("should be able to delete an HCP cluster after its etcd KMS Key Vault has been removed",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		labels.MIContainers(1),
		func(ctx context.Context) {
			const (
				customerNetworkSecurityGroupName = "customer-nsg"
				customerVnetName                 = "customer-vnet"
				customerVnetSubnetName           = "customer-vnet-subnet1"
				customerClusterName              = "kms-vault-del-hcp"
				customerNodePoolName             = "np-1"
			)
			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating the customer resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "cx-rg-kms-vault-del", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resource group")

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams20240610()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20240610(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{
					"customerNsgName":        customerNetworkSecurityGroupName,
					"customerVnetName":       customerVnetName,
					"customerVnetSubnetName": customerVnetSubnetName,
				},
				TestArtifactsFS,
				framework.RBACScopeResource,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for cluster %q", customerClusterName)
			Expect(clusterParams.KeyVaultName).NotTo(BeEmpty(), "expected customer resources to populate an etcd KMS key vault name for cluster %q", customerClusterName)

			By("creating the HCP cluster")
			err = tc.CreateHCPClusterFromParam20240610(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %q", customerClusterName)

			hcpClient := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()

			By("getting credentials")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20240610(
				ctx,
				hcpClient,
				*resourceGroup.Name,
				customerClusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for cluster %q", customerClusterName)

			By("ensuring the cluster is viable")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to verify HCP cluster %q is viable", customerClusterName)

			By("creating a node pool")
			nodePoolParams := framework.NewDefaultNodePoolParams20240610()
			nodePoolParams.NodePoolName = customerNodePoolName
			err = tc.CreateNodePoolFromParam20240610(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				managedResourceGroupName,
				customerClusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create node pool %q for cluster %q", customerNodePoolName, customerClusterName)

			By("deleting the etcd KMS key vault to make the guest API permanently unavailable")
			vaultsClient := tc.GetARMKeyVaultClientFactoryOrDie(ctx).NewVaultsClient()
			_, err = vaultsClient.Delete(ctx, *resourceGroup.Name, clusterParams.KeyVaultName, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to delete etcd KMS key vault %q for cluster %q", clusterParams.KeyVaultName, customerClusterName)

			By("deleting the HCP cluster and verifying it completes without manual finalizer removal")
			err = framework.DeleteHCPCluster20240610(ctx, hcpClient, *resourceGroup.Name, customerClusterName, framework.HCPClusterDeletionTimeout)
			Expect(err).NotTo(HaveOccurred(), "HCP cluster %q deletion must complete automatically without manual finalizer removal even though its etcd KMS key vault was deleted (regression test for AROSLSRE-1167 / AROSLSRE-1709)", customerClusterName)

			By("verifying the cluster resource is deleted (Not Found)")
			_, err = hcpClient.Get(ctx, *resourceGroup.Name, customerClusterName, nil)
			Expect(err).To(HaveOccurred(), "expected an error when getting deleted cluster %q", customerClusterName)
			var respErr *azcore.ResponseError
			Expect(errors.As(err, &respErr)).To(BeTrue(), "expected azcore.ResponseError when getting deleted cluster %q, got %v", customerClusterName, err)
			Expect(respErr.StatusCode).To(Equal(http.StatusNotFound), "expected HTTP 404 when getting deleted cluster %q", customerClusterName)

			// The fix intentionally orphans the AzureMachine k8s object without
			// deleting the real Azure VM/NIC behind it ("leaving Azure
			// infrastructure intact" per the fix's own doc comment), since the
			// guest API is gone and CAPZ can never actually run the cloud
			// delete. The managed resource group is therefore left holding
			// live infrastructure that neither the RP's own deprovisioning nor
			// the standard per-test cleanup will force-delete (it carries a
			// system-protected deny assignment that the standard cleanup path
			// treats as "locked" and skips). Force-delete it explicitly here so
			// the test doesn't leak billable Azure resources.
			By("force-deleting the orphaned managed resource group")
			networkClient, err := tc.GetARMNetworkClientFactory(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to create ARM network client factory")
			rgClient := tc.GetARMResourcesClientFactoryOrDie(ctx).NewResourceGroupsClient()
			err = framework.DeleteResourceGroup(ctx, rgClient, networkClient, managedResourceGroupName, true, 60*time.Minute)
			Expect(err).NotTo(HaveOccurred(), "failed to force-delete orphaned managed resource group %q left behind by the fix", managedResourceGroupName)
		})
})
