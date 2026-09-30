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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	hcpsdk20251223preview "github.com/Azure/ARO-HCP/test/sdk/v20251223preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	hcpsdk20260901preview "github.com/Azure/ARO-HCP/test/sdk/v20260901preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Create HCPOpenShiftCluster with Private KeyVault", func() {
	BeforeEach(func() {
		// do nothing. per test initialization usually ages better than shared.
	})

	It("should create a cluster with a private key vault using an available API version",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.CreateCluster,
		labels.MIContainers(1),
		func(ctx context.Context) {
			const customerClusterName = "private-kv-cluster"

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "private-keyvault", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for private keyvault test")
			useOldAPI, err := tc.APIVersionAvailable(ctx, *resourceGroup.Name, metadataapi.APIVersionV20251223Preview)
			Expect(err).NotTo(HaveOccurred(), "failed to check whether v20251223preview is available")

			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			var keyVaultName, visibility string
			if useOldAPI {
				By("creating cluster parameters using 20251223preview API")
				clusterParams := framework.NewDefaultClusterParams20251223()
				clusterParams.DisableSwift = false
				clusterParams.ClusterName = customerClusterName
				clusterParams.ManagedResourceGroupName = managedResourceGroupName
				clusterParams.KeyVaultVisibility = "Private"
				By("creating customer resources with v20251223preview")
				clusterParams, err = tc.CreateClusterCustomerResources20251223(ctx, resourceGroup, clusterParams, map[string]interface{}{"privateKeyVault": true}, TestArtifactsFS, framework.RBACScopeResourceGroup)
				Expect(err).NotTo(HaveOccurred(), "failed to create customer resources with private key vault")
				keyVaultName = clusterParams.KeyVaultName
				clusterResource, err := framework.BuildHCPClusterFromParams20251223(clusterParams, tc.Location(), nil)
				Expect(err).NotTo(HaveOccurred(), "failed to build v20251223preview cluster resource")
				Expect(clusterResource.Properties).NotTo(BeNil(), "v20251223preview cluster Properties was nil")
				Expect(clusterResource.Properties.Etcd).NotTo(BeNil(), "v20251223preview cluster Etcd was nil")
				Expect(clusterResource.Properties.Etcd.DataEncryption).NotTo(BeNil(), "v20251223preview cluster data encryption was nil")
				Expect(clusterResource.Properties.Etcd.DataEncryption.CustomerManaged).NotTo(BeNil(), "v20251223preview cluster customer managed encryption was nil")
				Expect(clusterResource.Properties.Etcd.DataEncryption.CustomerManaged.Kms).NotTo(BeNil(), "v20251223preview cluster KMS profile was nil")
				clusterResource.Properties.Etcd.DataEncryption.CustomerManaged.Kms.Visibility = to.Ptr(hcpsdk20251223preview.KeyVaultVisibilityPrivate)
				By("creating the HCP cluster with v20251223preview")
				_, err = framework.CreateHCPClusterAndWait20251223(ctx, GinkgoLogr, tc.Get20251223ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(), *resourceGroup.Name, customerClusterName, clusterResource, framework.ClusterCreationTimeout)
				Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %q with private key vault", customerClusterName)
				cluster, err := tc.Get20251223ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient().Get(ctx, *resourceGroup.Name, customerClusterName, nil)
				Expect(err).NotTo(HaveOccurred(), "failed to get cluster %q using v20251223preview", customerClusterName)
				Expect(cluster.Properties).NotTo(BeNil(), "cluster %q Properties was nil", customerClusterName)
				Expect(cluster.Properties.Etcd).NotTo(BeNil(), "cluster %q Properties.Etcd was nil", customerClusterName)
				Expect(cluster.Properties.Etcd.DataEncryption).NotTo(BeNil(), "cluster %q Properties.Etcd.DataEncryption was nil", customerClusterName)
				Expect(cluster.Properties.Etcd.DataEncryption.CustomerManaged).NotTo(BeNil(), "cluster %q customer managed encryption was nil", customerClusterName)
				Expect(cluster.Properties.Etcd.DataEncryption.CustomerManaged.Kms).NotTo(BeNil(), "cluster %q KMS profile was nil", customerClusterName)
				Expect(cluster.Properties.Etcd.DataEncryption.CustomerManaged.Kms.Visibility).NotTo(BeNil(), "cluster %q Key Vault visibility was nil", customerClusterName)
				visibility = string(*cluster.Properties.Etcd.DataEncryption.CustomerManaged.Kms.Visibility)
			} else {
				By("creating cluster parameters using 20260901preview API")
				clusterParams := framework.NewDefaultClusterParams20260901()
				clusterParams.DisableSwift = false
				clusterParams.ClusterName = customerClusterName
				clusterParams.ManagedResourceGroupName = managedResourceGroupName
				clusterParams.KeyVaultVisibility = "Private"
				By("creating customer resources with v20260901preview")
				clusterParams, err = tc.CreateClusterCustomerResources20260901(ctx, resourceGroup, clusterParams, map[string]interface{}{"privateKeyVault": true}, TestArtifactsFS, framework.RBACScopeResourceGroup)
				Expect(err).NotTo(HaveOccurred(), "failed to create customer resources with private key vault")
				keyVaultName = clusterParams.KeyVaultName
				clusterResource, err := framework.BuildHCPClusterFromParams20260901(clusterParams, tc.Location(), nil)
				Expect(err).NotTo(HaveOccurred(), "failed to build v20260901preview cluster resource")
				Expect(clusterResource.Properties).NotTo(BeNil(), "v20260901preview cluster Properties was nil")
				Expect(clusterResource.Properties.Etcd).NotTo(BeNil(), "v20260901preview cluster Etcd was nil")
				Expect(clusterResource.Properties.Etcd.DataEncryption).NotTo(BeNil(), "v20260901preview cluster data encryption was nil")
				Expect(clusterResource.Properties.Etcd.DataEncryption.CustomerManaged).NotTo(BeNil(), "v20260901preview cluster customer managed encryption was nil")
				Expect(clusterResource.Properties.Etcd.DataEncryption.CustomerManaged.Kms).NotTo(BeNil(), "v20260901preview cluster KMS profile was nil")
				clusterResource.Properties.Etcd.DataEncryption.CustomerManaged.Kms.Visibility = to.Ptr(hcpsdk20260901preview.KeyVaultVisibilityPrivate)
				By("creating the HCP cluster with v20260901preview")
				_, err = framework.CreateHCPClusterAndWait20260901(ctx, GinkgoLogr, tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(), *resourceGroup.Name, customerClusterName, clusterResource, framework.ClusterCreationTimeout)
				Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %q with private key vault", customerClusterName)
				cluster, err := tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient().Get(ctx, *resourceGroup.Name, customerClusterName, nil)
				Expect(err).NotTo(HaveOccurred(), "failed to get cluster %q using v20260901preview", customerClusterName)
				Expect(cluster.Properties).NotTo(BeNil(), "cluster %q Properties was nil", customerClusterName)
				Expect(cluster.Properties.Etcd).NotTo(BeNil(), "cluster %q Properties.Etcd was nil", customerClusterName)
				Expect(cluster.Properties.Etcd.DataEncryption).NotTo(BeNil(), "cluster %q Properties.Etcd.DataEncryption was nil", customerClusterName)
				Expect(cluster.Properties.Etcd.DataEncryption.CustomerManaged).NotTo(BeNil(), "cluster %q customer managed encryption was nil", customerClusterName)
				Expect(cluster.Properties.Etcd.DataEncryption.CustomerManaged.Kms).NotTo(BeNil(), "cluster %q KMS profile was nil", customerClusterName)
				Expect(cluster.Properties.Etcd.DataEncryption.CustomerManaged.Kms.Visibility).NotTo(BeNil(), "cluster %q Key Vault visibility was nil", customerClusterName)
				visibility = string(*cluster.Properties.Etcd.DataEncryption.CustomerManaged.Kms.Visibility)
			}
			Expect(visibility).To(Equal("Private"), "cluster etcd encryption Key Vault visibility should be Private")

			GinkgoLogr.Info("Cluster created successfully with private keyvault",
				"clusterName", customerClusterName,
				"keyVaultName", keyVaultName,
				"keyVaultVisibility", visibility)

			By("creating the node pool")
			nodePoolParams := framework.NewDefaultNodePoolParams20260901()
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = "np-1"
			nodePoolParams.Replicas = int32(2)

			err = tc.CreateNodePoolFromParam20260901(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				managedResourceGroupName,
				customerClusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create node pool %q for private keyvault cluster %q", nodePoolParams.NodePoolName, customerClusterName)

			GinkgoLogr.Info("Nodepool created successfully for private keyvault cluster",
				"clusterName", customerClusterName,
				"nodePoolName", nodePoolParams.NodePoolName)

			By("getting admin credentials for the cluster")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20260901(
				ctx,
				tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for private keyvault cluster %q", customerClusterName)

			By("verifying the cluster is viable and pod logs can be fetched")
			logVerifier := verifiers.VerifyGetDeploymentLogs("openshift-ingress", "router-default", "router")
			var previousError string
			Eventually(func() error {
				err := logVerifier.Verify(ctx, adminRESTConfig)
				if err != nil {
					currentError := err.Error()
					if currentError != previousError {
						GinkgoLogr.Info("Verifier check", "name", logVerifier.Name(), "status", "failed", "error", currentError)
						previousError = currentError
					}
				}
				return err
			}, 10*time.Minute, 30*time.Second).Should(Succeed(), "router-default deployment logs should be fetchable")

		})
})
