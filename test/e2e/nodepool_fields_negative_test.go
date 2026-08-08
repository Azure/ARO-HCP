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
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/utils/ptr"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/integration"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("NodePool Field Validation", func() {
	var (
		customerEnv *integration.CustomerEnv
		clusterEnv  *integration.Cluster
	)

	BeforeEach(func() {
		By("Preparing customer environment values")
		customerEnv = &e2eSetup.CustomerEnv
		clusterEnv = &e2eSetup.Cluster
	})

	It("validates nodepool field errors return clear user-friendly messages",
		labels.RequireHappyPathInfra,
		labels.Medium,
		labels.Negative,
		func(ctx context.Context) {
			tc := framework.NewTestContext()
			npClient := tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient()

			// Get cluster details to extract subnet ID
			hcpClient := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
			cluster, err := hcpClient.Get(ctx, customerEnv.CustomerRGName, clusterEnv.Name, nil)
			Expect(err).NotTo(HaveOccurred())
			clusterSubnetID := *cluster.Properties.Platform.SubnetID

			// Define a valid base nodepool configuration (SDK type)
			validNodePool := hcpsdk20240610preview.NodePool{
				Location: ptr.To(tc.Location()),
				Properties: &hcpsdk20240610preview.NodePoolProperties{
					Version: &hcpsdk20240610preview.NodePoolVersionProfile{
						ChannelGroup: ptr.To("stable"),
						ID:           ptr.To("4.19.7"),
					},
					Platform: &hcpsdk20240610preview.NodePoolPlatformProfile{
						SubnetID:               ptr.To(clusterSubnetID),
						VMSize:                 ptr.To("Standard_D8s_v3"),
						EnableEncryptionAtHost: ptr.To(false),
						OSDisk: &hcpsdk20240610preview.OsDiskProfile{
							SizeGiB:                ptr.To(int32(64)),
							DiskStorageAccountType: ptr.To(hcpsdk20240610preview.DiskStorageAccountTypeStandardSSDLRS),
						},
						AvailabilityZone: ptr.To(""), // Empty string means no specific zone
					},
					Replicas:   ptr.To(int32(2)),
					AutoRepair: ptr.To(true),
				},
			}

			// Test case structure
			type negativeTestCase struct {
				name         string
				nodePoolName string
				modifyFunc   func(*hcpsdk20240610preview.NodePool)
				expectedErrs []string // Multiple possible error messages
			}

			testCases := []negativeTestCase{
				// // ========================================
				// // VERSIONING TESTS
				// // ========================================
				// {
				// 	name:         "non-existent channel group",
				// 	nodePoolName: "np-bad-channel",
				// 	modifyFunc: func(np *hcpsdk20240610preview.NodePool) {
				// 		np.Properties.Version.ChannelGroup = ptr.To("non-existent-channel") // TODO what are the valid values for this?
				// 	},
				// 	expectedErrs: []string{"channel group", "channelGroup"},
				// },
				// {
				// 	name:         "invalid OpenShift version",
				// 	nodePoolName: "np-bad-version",
				// 	modifyFunc: func(np *hcpsdk20240610preview.NodePool) {
				// 		np.Properties.Version.ID = ptr.To("1.0.0")
				// 	},
				// 	expectedErrs: []string{"openshift"},
				// },

				// // ========================================
				// // PLATFORM PARAMETERS TESTS
				// // ========================================
				// {
				// 	name:         "non-existent VM size",
				// 	nodePoolName: "np-bad-vmsize",
				// 	modifyFunc: func(np *hcpsdk20240610preview.NodePool) {
				// 		np.Properties.Platform.VMSize = ptr.To("Standard_XYZ123_v99")
				// 	},
				// 	expectedErrs: []string{"machine type"},
				// },

				// clusterSubnetID: "/subscriptions/64f0619f-ebc2-4156-9d91-c4c781de7e54/resourceGroups/aklymenk-rg/providers/Microsoft.Network/virtualNetworks/customer-vnet/subnets/customer-subnet-1"
				// nodePoolSubnetID: "/subscriptions/64f0619f-ebc2-4156-9d91-c4c781de7e54/resourceGroups/aklymenk-rg/providers/Microsoft.Network/virtualNetworks/customer-vnet/subnets/different-subnet"

				// {
				// 	name:         "different subnet than cluster",
				// 	nodePoolName: "np-diff-subnet",
				// 	modifyFunc: func(np *hcpsdk20240610preview.NodePool) {
				// 		fmt.Println("clusterSubnetID:", clusterSubnetID)
				// 		// Modify the last segment of the subnet ID
				// 		parts := strings.Split(clusterSubnetID, "/")
				// 		if len(parts) > 0 {
				// 			parts[len(parts)-1] = "different-subnet"
				// 		}
				// 		np.Properties.Platform.SubnetID = ptr.To(strings.Join(parts, "/"))
				// 		fmt.Println("node pool subnet ID:", *np.Properties.Platform.SubnetID)
				// 	},
				// 	expectedErrs: []string{"not found"},
				// },
				// {
				// 	name:         "non-existent subnet",
				// 	nodePoolName: "np-bad-subnet",
				// 	modifyFunc: func(np *hcpsdk20240610preview.NodePool) {
				// 		// Use a completely invalid subnet ID that will fail validation immediately
				// 		np.Properties.Platform.SubnetID = ptr.To("a")
				// 		// np.Properties.Platform.SubnetID = ptr.To("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/non-existent-rg/providers/Microsoft.Network/virtualNetworks/non-existent-vnet/subnets/non-existent-subnet")
				// 	},
				// 	expectedErrs: []string{"not found", "does not exist", "invalid"},
				// },
				// {
				// 	name:         "non-existent availability zone",
				// 	nodePoolName: "np-bad-zone",
				// 	modifyFunc: func(np *hcpsdk20240610preview.NodePool) {
				// 		np.Properties.Platform.AvailabilityZone = ptr.To("southatl")
				// 	},
				// 	expectedErrs: []string{"esdeeeeeeeee"},
				// },

				// // ========================================
				// // SIZING AND LIMITS TESTS
				// // ========================================
				// {
				// 	name:         "osDisk size too small",
				// 	nodePoolName: "np-small-disk",
				// 	modifyFunc: func(np *hcpsdk20240610preview.NodePool) {
				// 		np.Properties.Platform.OSDisk.SizeGiB = ptr.To(int32(0)) // it does not fail if it is set to 0 (defaults to 64 GiB)
				// 	},
				// 	expectedErrs: []string{"sizeGiB", "osDisk"},
				// },
				// {
				// 	name:         "osDisk size too large",
				// 	nodePoolName: "np-large-disk",
				// 	modifyFunc: func(np *hcpsdk20240610preview.NodePool) {
				// 		np.Properties.Platform.OSDisk.SizeGiB = ptr.To(int32(40000)) // it does not fail, node pool is in provisioning state
				// 	},
				// 	expectedErrs: []string{"esdeeeeeeeee"},
				// },
				// {
				// 	name:         "replica count too small",
				// 	nodePoolName: "np-replica0",
				// 	modifyFunc: func(np *hcpsdk20240610preview.NodePool) {
				// 		np.Properties.Replicas = ptr.To(int32(-1)) // it does not fail if it is set to 0
				// 	},
				// 	expectedErrs: []string{"replicas"},
				// },
				{
					name:         "node drain timeout too small",
					nodePoolName: "np-small-drain",
					modifyFunc: func(np *hcpsdk20240610preview.NodePool) {
						np.Properties.NodeDrainTimeoutMinutes = ptr.To(int32(-1)) // it does not fail if it is set to 0
					},
					expectedErrs: []string{"node_drain_grace_period", "negative"},
				},
				{
					name:         "node drain timeout too large",
					nodePoolName: "np-large-drain",
					modifyFunc: func(np *hcpsdk20240610preview.NodePool) {
						np.Properties.NodeDrainTimeoutMinutes = ptr.To(int32(10081))
					},
					expectedErrs: []string{"node_drain_grace_period", "exceed", "maximum"},
				},

				// // ========================================
				// // AUTOSCALING TESTS
				// // ========================================
				// {
				// 	name:         "both replicas and autoscaling",
				// 	nodePoolName: "np-both-scaling",
				// 	modifyFunc: func(np *hcpsdk20240610preview.NodePool) {
				// 		np.Properties.Replicas = ptr.To(int32(2))
				// 		np.Properties.AutoScaling = &hcpsdk20240610preview.NodePoolAutoScaling{
				// 			Min: ptr.To(int32(2)),
				// 			Max: ptr.To(int32(5)),
				// 		}
				// 	},
				// 	expectedErrs: []string{"replicas"},
				// },
				// {
				// 	name:         "autoscaling max less than min",
				// 	nodePoolName: "np-bad-scaling",
				// 	modifyFunc: func(np *hcpsdk20240610preview.NodePool) {
				// 		np.Properties.Replicas = ptr.To(int32(0)) // Must be 0 when autoscaling is set
				// 		np.Properties.AutoScaling = &hcpsdk20240610preview.NodePoolAutoScaling{
				// 			Min: ptr.To(int32(5)),
				// 			Max: ptr.To(int32(3)),
				// 		}
				// 	},
				// 	expectedErrs: []string{"autoScaling", "max"},
				// },

				// // ========================================
				// // NAMING CONVENTIONS TESTS
				// // ========================================
				// {
				// 	name:         "nodepool name with illegal characters",
				// 	nodePoolName: "np_ilegal_chars",
				// 	modifyFunc:   func(np *hcpsdk20240610preview.NodePool) {},
				// 	expectedErrs: []string{"does not conform to the naming restriction"},
				// },
				// {
				// 	name:         "nodepool name starts with number",
				// 	nodePoolName: "123-nodepool",
				// 	modifyFunc:   func(np *hcpsdk20240610preview.NodePool) {},
				// 	expectedErrs: []string{"does not conform to the naming restriction."},
				// },
				// {
				// 	name:         "nodepool name too long",
				// 	nodePoolName: "np-verylongnamee",
				// 	modifyFunc:   func(np *hcpsdk20240610preview.NodePool) {},
				// 	expectedErrs: []string{"does not conform to the naming restriction."},
				// },
			}

			// Helper function to create a deep copy of the nodepool
			copyNodePool := func(src *hcpsdk20240610preview.NodePool) hcpsdk20240610preview.NodePool {
				dst := hcpsdk20240610preview.NodePool{
					Location: ptr.To(*src.Location),
					Properties: &hcpsdk20240610preview.NodePoolProperties{
						Version: &hcpsdk20240610preview.NodePoolVersionProfile{
							ChannelGroup: ptr.To(*src.Properties.Version.ChannelGroup),
							ID:           ptr.To(*src.Properties.Version.ID),
						},
						Platform: &hcpsdk20240610preview.NodePoolPlatformProfile{
							SubnetID:               ptr.To(*src.Properties.Platform.SubnetID),
							VMSize:                 ptr.To(*src.Properties.Platform.VMSize),
							EnableEncryptionAtHost: ptr.To(*src.Properties.Platform.EnableEncryptionAtHost),
							OSDisk: &hcpsdk20240610preview.OsDiskProfile{
								SizeGiB:                ptr.To(*src.Properties.Platform.OSDisk.SizeGiB),
								DiskStorageAccountType: ptr.To(*src.Properties.Platform.OSDisk.DiskStorageAccountType),
							},
							AvailabilityZone: ptr.To(*src.Properties.Platform.AvailabilityZone),
						},
						Replicas:   ptr.To(*src.Properties.Replicas),
						AutoRepair: ptr.To(*src.Properties.AutoRepair),
					},
				}
				return dst
			}

			// Run all negative test cases
			for _, tc := range testCases {
				By(fmt.Sprintf("Testing: %s", tc.name))

				// Create a deep copy of the valid nodepool and modify it
				testNodePool := copyNodePool(&validNodePool)
				tc.modifyFunc(&testNodePool)

				// Try to create the nodepool
				poller, err := npClient.BeginCreateOrUpdate(
					ctx,
					customerEnv.CustomerRGName,
					clusterEnv.Name,
					tc.nodePoolName,
					testNodePool,
					nil,
				)

				if err != nil {
					By(fmt.Sprintf("Got immediate error: %s", err.Error()))

					// Check that at least one expected error pattern is present
					matched := false
					for _, expectedErr := range tc.expectedErrs {
						if strings.Contains(strings.ToLower(err.Error()), strings.ToLower(expectedErr)) {
							matched = true
							break
						}
					}
					Expect(matched).To(BeTrue(), "Error '%s' should contain one of: %v", err.Error(), tc.expectedErrs)
				} else {
					// If no immediate error, wait for the async operation
					_, err = poller.PollUntilDone(ctx, nil) // TODO not using timeout here
					Expect(err).To(HaveOccurred(),
						"Expected nodepool creation to fail for test case: %s", tc.name)

					By(fmt.Sprintf("Got async error: %s", err.Error()))

					// Check expected error patterns
					matched := false
					for _, expectedErr := range tc.expectedErrs {
						if strings.Contains(strings.ToLower(err.Error()), strings.ToLower(expectedErr)) {
							matched = true
							break
						}
					}
					Expect(matched).To(BeTrue(), "Error '%s' should contain one of: %v", err.Error(), tc.expectedErrs)
				}
			}

			// ========================================
			// LIFECYCLE MANAGEMENT TEST
			// ========================================
			By("Testing deletion of last nodepool")

			// First, get current nodepools
			pager := npClient.NewListByParentPager(customerEnv.CustomerRGName, clusterEnv.Name, nil)
			var nodePools []string
			for pager.More() {
				page, err := pager.NextPage(ctx)
				Expect(err).NotTo(HaveOccurred())
				for _, np := range page.Value {
					if np.Name != nil {
						nodePools = append(nodePools, *np.Name)
					}
				}
			}

			Expect(len(nodePools)).To(Equal(1), "Expected exactly one nodepool to be present")

			// Try to delete the last nodepool
			By("Attempting to delete the last remaining nodepool")
			poller, err := npClient.BeginDelete(
				ctx,
				customerEnv.CustomerRGName,
				clusterEnv.Name,
				nodePools[0],
				nil,
			)

			if err != nil {
				By(fmt.Sprintf("Got immediate error: %s", err.Error()))
				Expect(err.Error()).To(ContainSubstring("last node pool"))
			} else {
				_, err = poller.PollUntilDone(ctx, nil) // TODO not using timeout here
				Expect(err).To(HaveOccurred(), "Expected deletion of last nodepool to fail")
				By(fmt.Sprintf("Got async error: %s", err.Error()))
				Expect(err.Error()).To(ContainSubstring("last node pool"))
			}
		},
	)
})
