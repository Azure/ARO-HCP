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

	"github.com/Azure/ARO-HCP/internal/api"
	hcpsdk20251223preview "github.com/Azure/ARO-HCP/test/sdk/v20251223preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = FDescribe("Create HCPOpenShiftCluster with Private KeyVault", func() {
	BeforeEach(func() {
		// do nothing. per test initialization usually ages better than shared.
	})

	It("should create a cluster with private keyvault using v20251223preview API",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.CreateCluster,
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

			const channelGroup = "candidate"

			By("resolving 5.0 install version")
			cpVersion, err := framework.GetLatestInstallVersion(ctx, channelGroup, "5.0")
			Expect(err).NotTo(HaveOccurred(), "failed to resolve 5.0 install version")

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams20251223()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName
			clusterParams.KeyVaultVisibility = "Private"
			clusterParams.OpenshiftVersionId = cpVersion
			clusterParams.ChannelGroup = channelGroup
			clusterParams.Tags[api.TagClusterCPOImageOverride] = to.Ptr("arohcpocpdev.azurecr.io/control-plane-operator@sha256:edb375fd935a683a08e56d7594513595d2fd05c8c9d10b4afab3e450fca0b674") // d565a6ed5b

			By("creating customer resources (infrastructure and managed identities)")
			clusterParams, err = tc.CreateClusterCustomerResources20251223(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{
					"privateKeyVault": true,
				},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources with private key vault")

			By("creating the HCP cluster")
			clusterResource, err := framework.BuildHCPClusterFromParams20251223(clusterParams, tc.Location(), nil)
			Expect(err).NotTo(HaveOccurred(), "failed to build HCP cluster resource from params")

			// Set KeyVault visibility
			if clusterResource.Properties != nil && clusterResource.Properties.Etcd != nil &&
				clusterResource.Properties.Etcd.DataEncryption != nil &&
				clusterResource.Properties.Etcd.DataEncryption.CustomerManaged != nil &&
				clusterResource.Properties.Etcd.DataEncryption.CustomerManaged.Kms != nil {
				clusterResource.Properties.Etcd.DataEncryption.CustomerManaged.Kms.Visibility = to.Ptr(hcpsdk20251223preview.KeyVaultVisibilityPrivate)
			}

			_, err = framework.CreateHCPClusterAndWait20251223(
				ctx,
				GinkgoLogr,
				tc.Get20251223ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				clusterResource,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %q with private keyvault", customerClusterName)

			By("verifying cluster was created with private keyvault visibility")
			clientFactory := tc.Get20251223ClientFactoryOrDie(ctx)
			cluster, err := clientFactory.NewHcpOpenShiftClustersClient().Get(
				ctx,
				*resourceGroup.Name,
				customerClusterName,
				nil,
			)
			Expect(err).ToNot(HaveOccurred(), "failed to get cluster %q to verify private keyvault visibility", customerClusterName)
			Expect(cluster.Properties).ToNot(BeNil(), "cluster %q Properties was nil", customerClusterName)
			Expect(cluster.Properties.Etcd).ToNot(BeNil(), "cluster %q Properties.Etcd was nil", customerClusterName)
			Expect(cluster.Properties.Etcd.DataEncryption).ToNot(BeNil(), "cluster %q Properties.Etcd.DataEncryption was nil", customerClusterName)
			Expect(cluster.Properties.Etcd.DataEncryption.CustomerManaged).ToNot(BeNil(), "cluster %q Properties.Etcd.DataEncryption.CustomerManaged was nil", customerClusterName)
			Expect(cluster.Properties.Etcd.DataEncryption.CustomerManaged.Kms).ToNot(BeNil(), "cluster %q Properties.Etcd.DataEncryption.CustomerManaged.Kms was nil", customerClusterName)

			Expect(cluster.Properties.Etcd.DataEncryption.CustomerManaged.Kms.Visibility).ToNot(BeNil(), "cluster %q Visibility field was nil", customerClusterName)
			Expect(*cluster.Properties.Etcd.DataEncryption.CustomerManaged.Kms.Visibility).To(Equal(hcpsdk20251223preview.KeyVaultVisibilityPrivate), "cluster etcd encryption key vault visibility should be Private")

			GinkgoLogr.Info("Cluster created successfully with private keyvault",
				"clusterName", customerClusterName,
				"keyVaultName", clusterParams.KeyVaultName,
				"keyVaultVisibility", *cluster.Properties.Etcd.DataEncryption.CustomerManaged.Kms.Visibility)

			By("creating the node pool")
			nodePoolParams := framework.NewDefaultNodePoolParams20240610()
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = "np-1"
			nodePoolParams.Replicas = int32(2)
			nodePoolParams.OpenshiftVersionId = cpVersion
			nodePoolParams.ChannelGroup = channelGroup

			err = tc.CreateNodePoolFromParam20240610(ctx,
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
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20240610(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
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
