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
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"

	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	hcpsdk20261001 "github.com/Azure/ARO-HCP/test/sdk/v20261001/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

// containsFold reports whether message contains substr, ignoring case. Azure's
// resource ID canonicalization can shift the casing of well-known path segments,
// so an exact-case match on an identity/DES resource ID embedded in an error
// message is not reliable.
func containsFold(message, substr string) bool {
	return strings.Contains(strings.ToLower(message), strings.ToLower(substr))
}

var _ = Describe("Nodepool OS Disk Encryption", func() {
	It("should successfully provision a nodepool with a disk encryption set when the Reader role is added after creation",
		labels.RequireNothing,
		labels.Medium,
		labels.Negative,
		labels.StageAndProdOnly,
		labels.MIContainers(1),
		labels.StartPriority(40),
		func(ctx context.Context) {
			const (
				customerClusterName  = "des-delayed-rbac"
				customerNodePoolName = "des-delayed-np"

				// The disk encryption set permission validation holds the node pool in
				// Provisioning for up to 30 minutes while the Reader role is missing
				// (nodePoolValidationFailureTimeout). The node pool's own creation
				// deadline must stay comfortably above that window, or the deadline
				// fires first and the operation fails terminally before the role fix
				// has a chance to be picked up.
				nodePoolCreationTimeout = framework.NodePoolCreationTimeout + 16*time.Minute

				consistentlyLoopDuration = 2 * time.Minute
			)

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "des-delayed-rbac", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group")

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams20261001()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources (infrastructure and managed identities)")
			clusterParams, err = tc.CreateClusterCustomerResources20261001(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{
					"assignKeyVaultCryptoOfficer":       true,
					"enableKeyVaultSoftDelete":          true,
					"enableKeyVaultPurgeProtection":     true,
					"keyVaultSoftDeleteRetentionInDays": 7,
				},
				TestArtifactsFS,
				framework.RBACScopeResourceNodePoolDES,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create cluster customer resources")

			By("resolving service managed identity principal ID")
			Expect(clusterParams.UserAssignedIdentitiesProfile).NotTo(BeNil(), "cluster params missing UserAssignedIdentitiesProfile")
			Expect(clusterParams.UserAssignedIdentitiesProfile.ServiceManagedIdentity).NotTo(BeNil(), "cluster params missing ServiceManagedIdentity resource ID")

			serviceMIResourceIDStr := *clusterParams.UserAssignedIdentitiesProfile.ServiceManagedIdentity
			serviceMIResourceID, err := azcorearm.ParseResourceID(serviceMIResourceIDStr)
			Expect(err).NotTo(HaveOccurred(), "failed to parse service managed identity resource ID")

			subscriptionID, err := tc.SubscriptionID(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to get subscription ID")

			creds, err := tc.AzureCredential()
			Expect(err).NotTo(HaveOccurred(), "failed to get Azure credentials")

			msiClientFactory, err := armmsi.NewClientFactory(subscriptionID, creds, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create MSI client factory")

			serviceMI, err := msiClientFactory.NewUserAssignedIdentitiesClient().Get(ctx, serviceMIResourceID.ResourceGroupName, serviceMIResourceID.Name, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to get service managed identity")
			Expect(serviceMI.Properties).NotTo(BeNil(), "service managed identity has no properties")
			Expect(serviceMI.Properties.PrincipalID).NotTo(BeNil(), "service managed identity has no principal ID")

			By("resolving cluster-api-azure managed identity principal ID")
			Expect(clusterParams.UserAssignedIdentitiesProfile.ControlPlaneOperators).NotTo(BeNil(), "cluster params missing ControlPlaneOperators")
			clusterAPIAzureResourceIDStr := clusterParams.UserAssignedIdentitiesProfile.ControlPlaneOperators["cluster-api-azure"]
			Expect(clusterAPIAzureResourceIDStr).NotTo(BeNil(), "cluster params missing cluster-api-azure identity")
			clusterAPIAzureResourceID, err := azcorearm.ParseResourceID(*clusterAPIAzureResourceIDStr)
			Expect(err).NotTo(HaveOccurred(), "failed to parse cluster-api-azure resource ID")
			clusterAPIAzureMI, err := msiClientFactory.NewUserAssignedIdentitiesClient().Get(ctx, clusterAPIAzureResourceID.ResourceGroupName, clusterAPIAzureResourceID.Name, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to get cluster-api-azure managed identity")
			Expect(clusterAPIAzureMI.Properties).NotTo(BeNil(), "cluster-api-azure managed identity has no properties")
			Expect(clusterAPIAzureMI.Properties.PrincipalID).NotTo(BeNil(), "cluster-api-azure managed identity has no principal ID")

			By("creating disk encryption set backed by KeyVault WITHOUT granting the Reader role to cluster identities")
			desResourceID, err := tc.CreateDiskEncryptionSet(ctx, *resourceGroup.Name, clusterParams.KeyVaultName, customerClusterName, tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create disk encryption set")

			By("creating the HCP cluster")
			err = tc.CreateHCPClusterFromParam20261001(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %s", customerClusterName)

			By("starting nodepool creation with a disk encryption set but no Reader role granted")
			nodePoolParams := framework.NewDefaultNodePoolParams20261001()
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.EncryptionSetID = desResourceID
			nodePoolParams.Tags[metadataapi.TagNodePoolValidateDiskEncryptionSetPermissions] = to.Ptr("true")
			nodePoolParams.Tags[metadataapi.TagNodePoolMaxCreationDuration] = to.Ptr((nodePoolCreationTimeout - time.Minute).String())

			vmSize, err := tc.SelectVMSize(ctx, framework.DefaultWorkerVMSizeSelector())
			Expect(err).NotTo(HaveOccurred(), "failed to resolve default VM size for node pool")
			nodePoolParams.VMSize = vmSize

			nodePool := framework.BuildNodePoolFromParams20261001(nodePoolParams, tc.Location())

			nodePoolsClient := tc.Get20261001ClientFactoryOrDie(ctx).NewNodePoolsClient()
			_, err = nodePoolsClient.BeginCreateOrUpdate(ctx, *resourceGroup.Name, customerClusterName, customerNodePoolName, nodePool, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to start nodepool creation")

			By("waiting for nodepool resource to become visible")
			Eventually(func(g Gomega) {
				_, err := nodePoolsClient.Get(ctx, *resourceGroup.Name, customerClusterName, customerNodePoolName, nil)
				g.Expect(err).NotTo(HaveOccurred(), "GET nodepool failed — RP may not have registered the resource yet")
			}, 2*time.Minute, 10*time.Second).Should(Succeed(),
				"timed out waiting for nodepool resource to become visible after BeginCreateOrUpdate")

			By("waiting for RequirementsValid to report the missing Reader role")
			Eventually(func(g Gomega) {
				resp, err := nodePoolsClient.Get(ctx, *resourceGroup.Name, customerClusterName, customerNodePoolName, nil)
				g.Expect(err).NotTo(HaveOccurred(), "failed to get nodepool")
				g.Expect(resp.Properties).NotTo(BeNil(), "nodepool Properties was nil")
				g.Expect(resp.Properties.Status).NotTo(BeNil(), "nodepool Status was nil")

				requirementsValid := findCondition(resp.Properties.Status.Conditions, "RequirementsValid")
				g.Expect(requirementsValid).NotTo(BeNil(), "RequirementsValid condition not found")
				g.Expect(requirementsValid.Status).NotTo(BeNil(), "RequirementsValid Status was nil")
				g.Expect(*requirementsValid.Status).To(Equal(hcpsdk20261001.StatusTypeFalse),
					"RequirementsValid should be False while the Reader role is missing")
				g.Expect(requirementsValid.Message).NotTo(BeNil(), "RequirementsValid Message was nil")

				message := *requirementsValid.Message
				g.Expect(containsFold(message, "does not have the Reader role on Disk Encryption Set")).To(BeTrue(),
					"RequirementsValid message should name the missing Reader role, got: %s", message)
				g.Expect(containsFold(message, desResourceID)).To(BeTrue(),
					"RequirementsValid message should name the disk encryption set, got: %s", message)
				g.Expect(containsFold(message, serviceMIResourceIDStr)).To(BeTrue(),
					"RequirementsValid message should name the service managed identity, got: %s", message)
				g.Expect(containsFold(message, *clusterAPIAzureResourceIDStr)).To(BeTrue(),
					"RequirementsValid message should name the cluster-api-azure identity, got: %s", message)
			}, 3*time.Minute, 10*time.Second).Should(Succeed(), "RequirementsValid never reported the missing Reader role")

			By("verifying nodepool stays in Provisioning while the Reader role is missing")
			var lastConsistentlyState hcpsdk20261001.ProvisioningState
			Consistently(func(g Gomega) {
				resp, err := nodePoolsClient.Get(ctx, *resourceGroup.Name, customerClusterName, customerNodePoolName, nil)
				g.Expect(err).NotTo(HaveOccurred(), "GET nodepool failed while the Reader role was missing")
				g.Expect(resp.Properties).NotTo(BeNil(), "nodepool response has nil Properties")
				g.Expect(resp.Properties.ProvisioningState).NotTo(BeNil(), "nodepool response has nil ProvisioningState")
				state := *resp.Properties.ProvisioningState
				if state != lastConsistentlyState {
					GinkgoLogr.Info("nodepool provisioning state", "state", state)
					lastConsistentlyState = state
				}
				g.Expect(state).To(Equal(hcpsdk20261001.ProvisioningStateProvisioning),
					"nodepool should stay in Provisioning while the Reader role is missing — the DES permission validation gate should hold it there, not let it fail or succeed prematurely; got %q", state)
			}, consistentlyLoopDuration, 15*time.Second).Should(Succeed())

			By("granting the Reader role on the disk encryption set to both cluster identities")
			err = tc.GrantDiskEncryptionSetReaderRole(ctx, desResourceID, *serviceMI.Properties.PrincipalID, *clusterAPIAzureMI.Properties.PrincipalID)
			Expect(err).NotTo(HaveOccurred(), "failed to grant Reader role on disk encryption set")

			By("waiting for nodepool to reach Succeeded state")
			var lastEventuallyErr string
			var lastEventuallyState hcpsdk20261001.ProvisioningState
			Eventually(func(g Gomega) {
				resp, err := nodePoolsClient.Get(ctx, *resourceGroup.Name, customerClusterName, customerNodePoolName, nil)
				if err != nil {
					lastEventuallyState = ""
					if msg := err.Error(); msg != lastEventuallyErr {
						GinkgoLogr.Info("GET nodepool returned error, retrying", "error", err)
						lastEventuallyErr = msg
					}
					g.Expect(err).NotTo(HaveOccurred(), "GET nodepool failed — RP returned an unexpected error")
				}
				lastEventuallyErr = ""
				g.Expect(resp.Properties).NotTo(BeNil(), "nodepool response has nil Properties")
				g.Expect(resp.Properties.ProvisioningState).NotTo(BeNil(), "nodepool response has nil ProvisioningState")
				state := *resp.Properties.ProvisioningState
				if state != lastEventuallyState {
					GinkgoLogr.Info("nodepool provisioning state", "state", state)
					lastEventuallyState = state
				}
				if state == hcpsdk20261001.ProvisioningStateFailed {
					StopTrying(fmt.Sprintf("nodepool %s/%s/%s entered terminal state %q after granting the Reader role; expected %q", *resourceGroup.Name, customerClusterName, customerNodePoolName, state, hcpsdk20261001.ProvisioningStateSucceeded)).Now()
				}
				g.Expect(state).To(Equal(hcpsdk20261001.ProvisioningStateSucceeded),
					"nodepool %s/%s/%s has state %q; waiting for %q", *resourceGroup.Name, customerClusterName, customerNodePoolName, state, hcpsdk20261001.ProvisioningStateSucceeded)
			}, nodePoolCreationTimeout-consistentlyLoopDuration, 30*time.Second).Should(Succeed(),
				"nodepool should eventually succeed after the Reader role is granted")

			By("verifying RequirementsValid recovered to True")
			Eventually(func(g Gomega) {
				resp, err := nodePoolsClient.Get(ctx, *resourceGroup.Name, customerClusterName, customerNodePoolName, nil)
				g.Expect(err).NotTo(HaveOccurred(), "failed to get nodepool")
				g.Expect(resp.Properties).NotTo(BeNil(), "nodepool Properties was nil")
				g.Expect(resp.Properties.Status).NotTo(BeNil(), "nodepool Status was nil")

				requirementsValid := findCondition(resp.Properties.Status.Conditions, "RequirementsValid")
				g.Expect(requirementsValid).NotTo(BeNil(), "RequirementsValid condition not found")
				g.Expect(requirementsValid.Status).NotTo(BeNil(), "RequirementsValid Status was nil")
				g.Expect(*requirementsValid.Status).To(Equal(hcpsdk20261001.StatusTypeTrue),
					"RequirementsValid should recover to True once the Reader role is granted")
			}, 2*time.Minute, 10*time.Second).Should(Succeed(), "RequirementsValid never recovered to True")

			By("getting credentials to verify cluster health")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20261001(
				ctx,
				tc.Get20261001ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config")

			By("verifying cluster health, node readiness, and VM OS disk encryption")
			computeFactory := tc.GetARMComputeClientFactoryOrDie(ctx)
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig,
				verifiers.VerifyNodeCount(customerClusterName, int(nodePoolParams.Replicas)),
				verifiers.VerifyNodesReady(),
				verifiers.VerifyVMOSDiskCustomerEncryption(computeFactory, managedResourceGroupName, customerNodePoolName, desResourceID),
			)
			Expect(err).NotTo(HaveOccurred(), "cluster verification failed")
		})
})

// findCondition returns the condition with the given type from conditions, or nil if not found.
func findCondition(conditions []*hcpsdk20261001.Condition, conditionType string) *hcpsdk20261001.Condition {
	for _, condition := range conditions {
		if condition != nil && condition.Type != nil && string(*condition.Type) == conditionType {
			return condition
		}
	}
	return nil
}
