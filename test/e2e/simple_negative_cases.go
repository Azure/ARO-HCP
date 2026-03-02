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
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
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
			clusterParams.ClusterName = "negative-tests-cluster"
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

			By("creating the HCP cluster")
			err = tc.CreateHCPClusterFromParam(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating a nodepool")
			nodePoolParams := framework.NewDefaultNodePoolParams()
			nodePoolParams.ClusterName = clusterParams.ClusterName
			nodePoolParams.NodePoolName = "test-nodepool"

			err = tc.CreateNodePoolFromParam(ctx,
				*resourceGroup.Name,
				clusterParams.ClusterName,
				nodePoolParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			nodePoolClient := tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient()

			var errs []error

			// TEST CASE: Invalid version update should be rejected
			// blocked by https://issues.redhat.com/browse/ARO-24542
			/*
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
				if err == nil {
					errs = append(errs, fmt.Errorf("version validation: expected error when updating to invalid version %s, but no error occurred", invalidNodePoolVersion))
				} else if !strings.Contains(strings.ToLower(err.Error()), "version") {
					errs = append(errs, fmt.Errorf("version validation: expected error to contain 'version', got: %s", err.Error()))
				}

			*/

			// TEST CASE: Immutable field updates should be rejected
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
				if err == nil {
					errs = append(errs, fmt.Errorf("expected error when updating immutable fields, but no error occurred"))
				} else if !strings.Contains(err.Error(), "Forbidden: field is immutable") {
					errs = append(errs, fmt.Errorf("expected 'Forbidden: field is immutable', got: %s", err.Error()))
				} else {
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

			// TEST CASE: https://issues.redhat.com/browse/ARO-22570 to be implemented here

			// TEST CASE: https://issues.redhat.com/browse/ARO-22571 to be implemented here

			if len(errs) > 0 {
				Expect(errors.Join(errs...)).NotTo(HaveOccurred())
			}
		})

})
