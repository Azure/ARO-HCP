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
	"fmt"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/util/wait"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Customer", func() {
	It("should delete a provisioned HCP cluster through the RP and remove its managed resource group",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.CreateCluster,
		labels.MIContainers(1),
		func(ctx context.Context) {
			const customerClusterName = "delete-rp-cluster"
			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity container for cluster %q", customerClusterName)
			}

			By("creating an isolated customer resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "delete-rp", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resource group")
			Expect(resourceGroup.Name).NotTo(BeNil(), "customer resource group name was nil")
			resourceGroupName := *resourceGroup.Name
			managedResourceGroupName := framework.SuffixName(resourceGroupName, "-managed", 64)

			By("creating default customer infrastructure and cluster parameters")
			clusterParams := framework.NewDefaultClusterParams20240610()
			clusterParams.ClusterName = customerClusterName
			clusterParams.ManagedResourceGroupName = managedResourceGroupName
			clusterParams, err = tc.CreateClusterCustomerResources20240610(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer infrastructure for cluster %q", customerClusterName)

			By("creating the default HCP cluster and waiting for successful provisioning")
			err = tc.CreateHCPClusterFromParam20240610(ctx, GinkgoLogr, resourceGroupName, clusterParams, framework.ClusterCreationTimeout)
			Expect(err).NotTo(HaveOccurred(), "failed to provision cluster %q", customerClusterName)
			hcpClient := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
			cluster, err := hcpClient.Get(ctx, resourceGroupName, customerClusterName, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to get provisioned cluster %q", customerClusterName)
			Expect(cluster.Properties).NotTo(BeNil(), "cluster %q properties were nil", customerClusterName)
			Expect(cluster.Properties.ProvisioningState).NotTo(BeNil(), "cluster %q provisioning state was nil", customerClusterName)
			Expect(*cluster.Properties.ProvisioningState).To(Equal(hcpsdk20240610preview.ProvisioningStateSucceeded), "cluster %q must be Succeeded before deletion", customerClusterName)

			By("confirming the managed resource group exists before deletion")
			rgClient := tc.GetARMResourcesClientFactoryOrDie(ctx).NewResourceGroupsClient()
			_, err = rgClient.Get(ctx, managedResourceGroupName, nil)
			Expect(err).NotTo(HaveOccurred(), "managed resource group %q must exist before cluster deletion", managedResourceGroupName)

			waitForNotFound := func(resourceName, errorCode string, timeout time.Duration, getResource func(context.Context) error) error {
				lastObserved := ""
				return wait.PollUntilContextTimeout(ctx, framework.StandardPollInterval, timeout, true, func(pollCtx context.Context) (bool, error) {
					getErr := getResource(pollCtx)
					if getErr != nil {
						var responseError *azcore.ResponseError
						if framework.IsNotFoundError(getErr) && errors.As(getErr, &responseError) && responseError.ErrorCode == errorCode {
							return true, nil
						}
						return false, fmt.Errorf("expected HTTP 404 (%s) for %q, got: %w", errorCode, resourceName, getErr)
					}
					observed := "resource still exists"
					if observed != lastObserved {
						GinkgoLogr.Info("waiting for resource deletion", "resource", resourceName,
							"expected", fmt.Sprintf("HTTP 404 (%s)", errorCode), "observed", observed)
						lastObserved = observed
					}
					return false, nil
				})
			}

			By("deleting the cluster through the RP and waiting for asynchronous completion")
			err = framework.DeleteHCPCluster20240610(ctx, hcpClient, resourceGroupName, customerClusterName, framework.HCPClusterDeletionTimeout)
			Expect(err).NotTo(HaveOccurred(), "RP deletion must complete for cluster %q in resource group %q", customerClusterName, resourceGroupName)

			By("waiting for cluster GET to return HTTP 404 ResourceNotFound")
			err = waitForNotFound(customerClusterName, "ResourceNotFound", framework.HCPClusterDeletionTimeout, func(pollCtx context.Context) error {
				_, getErr := hcpClient.Get(pollCtx, resourceGroupName, customerClusterName, nil)
				return getErr
			})
			Expect(err).NotTo(HaveOccurred(), "cluster %q must disappear from resource group %q after RP deletion", customerClusterName, resourceGroupName)

			By("waiting for backend cleanup to remove the managed resource group")
			err = waitForNotFound(managedResourceGroupName, "ResourceGroupNotFound", 15*time.Minute, func(pollCtx context.Context) error {
				_, getErr := rgClient.Get(pollCtx, managedResourceGroupName, nil)
				return getErr
			})
			Expect(err).NotTo(HaveOccurred(), "managed resource group %q must be removed by cluster deletion", managedResourceGroupName)

			By("confirming the customer resource group survives cluster deletion")
			_, err = rgClient.Get(ctx, resourceGroupName, nil)
			Expect(err).NotTo(HaveOccurred(), "customer resource group %q must still exist after cluster deletion", resourceGroupName)
		},
	)
})
