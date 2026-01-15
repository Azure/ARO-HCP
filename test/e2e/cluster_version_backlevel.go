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
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Customer", func() {

	It("should be able to create an HCP cluster with back-level version",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			const (
				customerNetworkSecurityGroupName = "customer-nsg-name"
				customerVnetName                 = "customer-vnet-name"
				customerVnetSubnetName           = "customer-vnet-subnet1"
				customerClusterName              = "cluster-back-version"
				customerNodePoolName             = "np-ver-"
			)
			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "rg-cluster-back-version", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName
			clusterParams.OpenshiftVersionId = "4.19"

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources(ctx,
				resourceGroup,
				clusterParams,
				map[string]any{
					"persistTagValue":        false,
					"customerNsgName":        customerNetworkSecurityGroupName,
					"customerVnetName":       customerVnetName,
					"customerVnetSubnetName": customerVnetSubnetName,
				},
				TestArtifactsFS,
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

			By("creating different version nodepools in parallel")
			nodePoolVersions := map[string]struct {
				nodePoolVersion string
				nodePoolSuffix  string
				description     string
			}{
				"node pool version 4.y.0": {
					nodePoolVersion: "4.19.0",
					nodePoolSuffix:  "4-19-0",
					description:     "4.y.0",
				},
				"node pool version 4.y.z-1": {
					nodePoolVersion: "4.19.6",
					nodePoolSuffix:  "4-19-6",
					description:     "4.y.z-1",
				},
				"node pool version 4.y.z": {
					nodePoolVersion: "4.19.7",
					nodePoolSuffix:  "4-19-7",
					description:     "4.y.z",
				},
			}

			var wg sync.WaitGroup
			var errors []error
			var errorsMutex sync.Mutex

			for _, npVer := range nodePoolVersions {
				wg.Add(1)
				go func(npVer struct {
					nodePoolVersion string
					nodePoolSuffix  string
					description     string
				}) {
					defer wg.Done()

					nodePoolParams := framework.NewDefaultNodePoolParams()
					nodePoolParams.ClusterName = customerClusterName
					nodePoolParams.NodePoolName = customerNodePoolName + npVer.nodePoolSuffix
					nodePoolParams.Replicas = int32(1)
					nodePoolParams.OpenshiftVersionId = npVer.nodePoolVersion

					err := tc.CreateNodePoolFromParam(ctx,
						*resourceGroup.Name,
						customerClusterName,
						nodePoolParams,
						45*time.Minute,
					)
					if err != nil {
						GinkgoLogr.Error(err, "nodepool creation failed",
							"version", npVer.nodePoolVersion,
							"description", npVer.description,
							"name", nodePoolParams.NodePoolName)

						errorsMutex.Lock()
						errors = append(errors, err)
						errorsMutex.Unlock()
					}
				}(npVer)
			}

			wg.Wait()

			for _, err := range errors {
				Expect(err).NotTo(HaveOccurred())
			}

		})
})
