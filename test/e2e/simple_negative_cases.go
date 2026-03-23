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
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Customer", func() {

	It("should not be able to perform various invalid operations on cluster resources",
		labels.RequireNothing,
		labels.Negative,
		labels.Medium,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			const (
				customerNetworkSecurityGroupName = "customer-nsg-name"
				customerVnetName                 = "customer-vnet-name"
				customerVnetSubnetName           = "customer-vnet-subnet"
				customerClusterName              = "negative-tests-cluster"
				customerNodePoolName             = "np-1"
			)
			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "rg-negative-tests", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources(ctx,
				resourceGroup,
				clusterParams,
				map[string]any{
					"customerNsgName":        customerNetworkSecurityGroupName,
					"customerVnetName":       customerVnetName,
					"customerVnetSubnetName": customerVnetSubnetName,
				},
				TestArtifactsFS,
				framework.RBACScopeResource,
			)
			Expect(err).NotTo(HaveOccurred())

			nodePoolClient := tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient()
			var errs []error

			if false { // blocked by https://redhat.atlassian.net/browse/ARO-25089
				// TEST CASE: ARO-22570
				By("attempting to list clusters in a non-existent resource group")
				clusterClient := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
				nonExistentRgName := "non-existent-rg"
				clusterPager := clusterClient.NewListByResourceGroupPager(nonExistentRgName, nil)
				_, err = clusterPager.NextPage(ctx)
				checkExpectedError(&errs, "cluster listing in non-existent resource group", err, "resource group not found")

				// TEST CASE: ARO-22571
				By("attempting to list node pools in a resource group without a cluster")
				emptyRgNodePoolPager := nodePoolClient.NewListByParentPager(*resourceGroup.Name, clusterParams.ClusterName, nil)
				_, err = emptyRgNodePoolPager.NextPage(ctx)
				checkExpectedError(&errs, "node pool listing in RG with no clusters", err, "parent resource not found")

				By("attempting to list node pools in a non-existent resource group")
				nodePoolPager := nodePoolClient.NewListByParentPager(nonExistentRgName, clusterParams.ClusterName, nil)
				_, err = nodePoolPager.NextPage(ctx)
				checkExpectedError(&errs, "node pool listing in non-existent resource group", err, "resource group not found")
			}

			By("creating the HCP cluster")
			err = tc.CreateHCPClusterFromParam(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("getting credentials")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster(
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

			nodePoolParams := framework.NewDefaultNodePoolParams()
			nodePoolParams.ClusterName = clusterParams.ClusterName
			nodePoolParams.NodePoolName = customerNodePoolName

			// TEST CASE: ARO-22576
			nodePoolParamsInvalidInstance := nodePoolParams
			nodePoolParamsInvalidInstance.VMSize = "Standard_A1_v2" // real, but unsupported Azure instance type

			nodePoolParamsInvalidQuota := nodePoolParams
			nodePoolParamsInvalidQuota.Replicas = int32(201)

			By("attempting to create a node pool with invalid instance type")
			err = tc.CreateNodePoolFromParam(ctx,
				*resourceGroup.Name,
				clusterParams.ClusterName,
				nodePoolParamsInvalidInstance,
				5*time.Minute,
			)
			checkExpectedError(&errs, "node pool creation with invalid instance type", err, "machine type not supported")

			By("attempting to create a node pool with invalid quota")
			err = tc.CreateNodePoolFromParam(ctx,
				*resourceGroup.Name,
				clusterParams.ClusterName,
				nodePoolParamsInvalidQuota,
				5*time.Minute,
			)
			checkExpectedError(&errs, "node pool creation with invalid quota", err, "invalid value must be less than or equal to")

			By("creating a nodepool")
			err = tc.CreateNodePoolFromParam(ctx,
				*resourceGroup.Name,
				clusterParams.ClusterName,
				nodePoolParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			// TEST CASE: ARO-23182
			if false { // blocked by https://issues.redhat.com/browse/ARO-24542
				By("attempting to update nodepool version to higher than cluster version")
				clusterVersion := clusterParams.OpenshiftVersionId
				parts := strings.Split(clusterVersion, ".")
				minor, _ := strconv.Atoi(parts[1])
				invalidNodePoolVersion := fmt.Sprintf("%s.%d.0", parts[0], minor+1) // +1 y-stream, z set to 0
				versionUpdate := hcpsdk20240610preview.NodePoolUpdate{
					Properties: &hcpsdk20240610preview.NodePoolPropertiesUpdate{
						Version: &hcpsdk20240610preview.NodePoolVersionProfile{
							ID: &invalidNodePoolVersion,
						},
					},
				}

				_, err = framework.UpdateNodePoolAndWait(ctx,
					tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient(),
					*resourceGroup.Name,
					clusterParams.ClusterName,
					nodePoolParams.NodePoolName,
					versionUpdate,
					10*time.Minute,
				)
				checkExpectedError(&errs, "node pool version update validation", err, "version")
			}

			// TEST CASE: ARO-24877
			By("attempting to update immutable platform profile fields")
			nodePool, err := nodePoolClient.Get(ctx, *resourceGroup.Name, clusterParams.ClusterName, nodePoolParams.NodePoolName, nil)
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to get nodepool: %w", err))
			} else if nodePool.Properties == nil {
				errs = append(errs, fmt.Errorf("nodepool properties are nil"))
			} else if nodePool.Properties.Platform == nil {
				errs = append(errs, fmt.Errorf("nodepool platform properties are nil"))
			} else {
				nodePool.Properties.Platform.VMSize = to.Ptr("Standard_D16s_v3")
				nodePool.Properties.Platform.AvailabilityZone = to.Ptr("2")
				if nodePool.Properties.Platform.OSDisk != nil {
					nodePool.Properties.Platform.OSDisk.SizeGiB = to.Ptr[int32](256)
					nodePool.Properties.Platform.OSDisk.DiskStorageAccountType = to.Ptr(hcpsdk20240610preview.DiskStorageAccountTypePremiumLRS)
				}

				_, err = nodePoolClient.BeginCreateOrUpdate(ctx, *resourceGroup.Name, clusterParams.ClusterName, nodePoolParams.NodePoolName, nodePool.NodePool, nil)
				checkExpectedError(&errs, "updating immutable fields", err, "forbidden field immutable")
				if err != nil {
					updatedNodePool, getErr := nodePoolClient.Get(ctx, *resourceGroup.Name, clusterParams.ClusterName, nodePoolParams.NodePoolName, nil)
					if getErr != nil {
						errs = append(errs, fmt.Errorf("failed to verify nodepool after failed update: %w", getErr))
					} else if updatedNodePool.Properties == nil || updatedNodePool.Properties.Platform == nil {
						errs = append(errs, fmt.Errorf("updated nodepool properties or platform are nil"))
					} else {
						platform := updatedNodePool.Properties.Platform

						if platform.VMSize != nil && *platform.VMSize != "Standard_D8s_v3" {
							errs = append(errs, fmt.Errorf("vmSize was modified despite immutable error"))
						}

						if platform.AvailabilityZone != nil {
							errs = append(errs, fmt.Errorf("availabilityZone was modified despite immutable error"))
						}

						if platform.OSDisk != nil {
							if platform.OSDisk.SizeGiB != nil && *platform.OSDisk.SizeGiB != 64 {
								errs = append(errs, fmt.Errorf("osDisk.sizeGiB was modified despite immutable error"))
							}

							if platform.OSDisk.DiskStorageAccountType != nil && *platform.OSDisk.DiskStorageAccountType != hcpsdk20240610preview.DiskStorageAccountTypeStandardSSDLRS {
								errs = append(errs, fmt.Errorf("osDisk.diskStorageAccountType was modified despite immutable error"))
							}
						}
					}
				}
			}
			// TEST CASE: https://issues.redhat.com/browse/ARO-22240 to be implemented here

			if len(errs) > 0 {
				Expect(errors.Join(errs...)).NotTo(HaveOccurred())
			}
		})

})

func checkExpectedError(errs *[]error, operation string, err error, expectedErrorKeywords string) {
	GinkgoLogr.Error(err, operation)

	if err == nil {
		*errs = append(*errs, fmt.Errorf("%s: expected error but none occurred", operation))
		return
	}

	pattern := ".*" + strings.ReplaceAll(strings.ToLower(expectedErrorKeywords), " ", ".*") + ".*"
	lowerError := strings.ToLower(err.Error())

	if matched, _ := regexp.MatchString(pattern, lowerError); !matched {
		*errs = append(*errs, fmt.Errorf("%s: expected error containing keywords '%s', got: %s", operation, expectedErrorKeywords, err.Error()))
	}
}
