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
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Create HCPOpenShiftCluster with Public KeyVault and CPO override", func() {
	BeforeEach(func() {
		// do nothing. per test initialization usually ages better than shared.
	})

	It("should create a cluster with public keyvault and CPO override using v20251223preview API",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.CreateCluster,
		labels.MIContainers(1),
		func(ctx context.Context) {
			const customerClusterName = "public-kv-cluster"

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "public-keyvault", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for public keyvault test")

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams20251223()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName
			clusterParams.KeyVaultVisibility = "Public"
			// The CPO image override is built for a specific OpenShift minor, so the
			// cluster version must be pinned to match it (otherwise the override CPO
			// cannot parse a newer release payload and the control plane never comes up).
			clusterParams.OpenshiftVersionId = "4.20"
			clusterParams.ChannelGroup = "candidate"
			clusterParams.Tags[metadataapi.TagClusterCPOImageOverride] = to.Ptr(cpoOverrideImage)

			By("creating customer resources (infrastructure and managed identities)")
			clusterParams, err = tc.CreateClusterCustomerResources20251223(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{
					"privateKeyVault": false,
				},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources with public key vault")

			By("creating the HCP cluster")
			clusterResource, err := framework.BuildHCPClusterFromParams20251223(clusterParams, tc.Location(), nil)
			Expect(err).NotTo(HaveOccurred(), "failed to build HCP cluster resource from params")

			// Set KeyVault visibility
			if clusterResource.Properties != nil && clusterResource.Properties.Etcd != nil &&
				clusterResource.Properties.Etcd.DataEncryption != nil &&
				clusterResource.Properties.Etcd.DataEncryption.CustomerManaged != nil &&
				clusterResource.Properties.Etcd.DataEncryption.CustomerManaged.Kms != nil {
				clusterResource.Properties.Etcd.DataEncryption.CustomerManaged.Kms.Visibility = to.Ptr(hcpsdk20251223preview.KeyVaultVisibilityPublic)
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
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %q with public keyvault", customerClusterName)

			By("verifying cluster was created with public keyvault visibility")
			clientFactory := tc.Get20251223ClientFactoryOrDie(ctx)
			cluster, err := clientFactory.NewHcpOpenShiftClustersClient().Get(
				ctx,
				*resourceGroup.Name,
				customerClusterName,
				nil,
			)
			Expect(err).ToNot(HaveOccurred(), "failed to get cluster %q to verify public keyvault visibility", customerClusterName)
			Expect(cluster.Properties).ToNot(BeNil(), "cluster %q Properties was nil", customerClusterName)
			Expect(cluster.Properties.Etcd).ToNot(BeNil(), "cluster %q Properties.Etcd was nil", customerClusterName)
			Expect(cluster.Properties.Etcd.DataEncryption).ToNot(BeNil(), "cluster %q Properties.Etcd.DataEncryption was nil", customerClusterName)
			Expect(cluster.Properties.Etcd.DataEncryption.CustomerManaged).ToNot(BeNil(), "cluster %q Properties.Etcd.DataEncryption.CustomerManaged was nil", customerClusterName)
			Expect(cluster.Properties.Etcd.DataEncryption.CustomerManaged.Kms).ToNot(BeNil(), "cluster %q Properties.Etcd.DataEncryption.CustomerManaged.Kms was nil", customerClusterName)

			Expect(cluster.Properties.Etcd.DataEncryption.CustomerManaged.Kms.Visibility).ToNot(BeNil(), "cluster %q Visibility field was nil", customerClusterName)
			Expect(*cluster.Properties.Etcd.DataEncryption.CustomerManaged.Kms.Visibility).To(Equal(hcpsdk20251223preview.KeyVaultVisibilityPublic), "cluster etcd encryption key vault visibility should be Public")

			GinkgoLogr.Info("Cluster created successfully with public keyvault",
				"clusterName", customerClusterName,
				"keyVaultName", clusterParams.KeyVaultName,
				"keyVaultVisibility", *cluster.Properties.Etcd.DataEncryption.CustomerManaged.Kms.Visibility)

			By("creating the node pool")
			nodePoolParams := framework.NewDefaultNodePoolParams20251223()
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = "np-1"
			nodePoolParams.Replicas = int32(2)
			// Pin the node pool to a concrete 4.20 patch to match the pinned control plane version.
			nodePoolParams.OpenshiftVersionId = "4.20.15"
			nodePoolParams.ChannelGroup = "candidate"

			err = tc.CreateNodePoolFromParam20251223(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				managedResourceGroupName,
				customerClusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create node pool %q for public keyvault cluster %q", nodePoolParams.NodePoolName, customerClusterName)

			GinkgoLogr.Info("Nodepool created successfully for public keyvault cluster",
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
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for public keyvault cluster %q", customerClusterName)

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
