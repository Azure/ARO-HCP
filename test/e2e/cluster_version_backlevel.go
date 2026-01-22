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
	"strconv"
	"strings"
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
				customerNetworkSecurityGroupName = "customer-nsg-name-"
				customerVnetName                 = "customer-vnet-name-"
				customerVnetSubnetName           = "customer-vnet-subnet-"
				customerClusterName              = "cluster-ver-"
				customerNodePoolName             = "np-ver-"
			)
			tc := framework.NewTestContext()

			clustersCount := uint8(len(framework.BacklevelOpenshiftControlPlaneVersionId()))
			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, clustersCount, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "rg-cluster-back-version", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating clusters with back-level control plane versions in parallel")
			backlevelControlPlaneVersions := framework.BacklevelOpenshiftControlPlaneVersionId()

			var wg sync.WaitGroup
			var errors []error
			var errorsMutex sync.Mutex

			for _, controlPlaneVersion := range backlevelControlPlaneVersions {
				wg.Add(1)
				go func(controlPlaneVersion string) {
					defer wg.Done()

					clusterSuffix := strings.ReplaceAll(controlPlaneVersion, ".", "-")
					clusterName := customerClusterName + clusterSuffix

					clusterParams := framework.NewDefaultClusterParams()
					clusterParams.ClusterName = clusterName
					managedResourceGroupName := framework.SuffixName(*resourceGroup.Name+"-"+clusterSuffix, "-managed", 64)
					clusterParams.ManagedResourceGroupName = managedResourceGroupName
					clusterParams.OpenshiftVersionId = controlPlaneVersion

					clusterParams, err := tc.CreateClusterCustomerResources(ctx,
						resourceGroup,
						clusterParams,
						map[string]any{
							"customerNsgName":        customerNetworkSecurityGroupName + clusterSuffix,
							"customerVnetName":       customerVnetName + clusterSuffix,
							"customerVnetSubnetName": customerVnetSubnetName + clusterSuffix,
						},
						TestArtifactsFS,
					)
					if err != nil {
						GinkgoLogr.Error(err, "customer resources creation failed",
							"controlPlaneVersion", controlPlaneVersion,
							"cluster", clusterName)
						errorsMutex.Lock()
						errors = append(errors, err)
						errorsMutex.Unlock()
						return
					}

					By("creating HCP cluster version " + controlPlaneVersion)
					err = tc.CreateHCPClusterFromParam(
						ctx,
						GinkgoLogr,
						*resourceGroup.Name,
						clusterParams,
						45*time.Minute,
					)
					if err != nil {
						GinkgoLogr.Error(err, "cluster creation failed",
							"controlPlaneVersion", controlPlaneVersion,
							"cluster", clusterName)
						errorsMutex.Lock()
						errors = append(errors, err)
						errorsMutex.Unlock()
						return
					}

					adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster(
						ctx,
						tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
						*resourceGroup.Name,
						clusterName,
						10*time.Minute,
					)
					if err != nil {
						GinkgoLogr.Error(err, "failed to get admin credentials",
							"controlPlaneVersion", controlPlaneVersion,
							"cluster", clusterName)
						errorsMutex.Lock()
						errors = append(errors, err)
						errorsMutex.Unlock()
						return
					}

					err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
					if err != nil {
						GinkgoLogr.Error(err, "cluster verification failed",
							"controlPlaneVersion", controlPlaneVersion,
							"cluster", clusterName)
						errorsMutex.Lock()
						errors = append(errors, err)
						errorsMutex.Unlock()
						return
					}

					By("creating node pools with back-level versions")
					backlevelNodePoolVersions := framework.BacklevelOpenshiftNodePoolVersionId()

					var matchingNodePoolVersion string
					for _, nodePoolVersion := range backlevelNodePoolVersions {
						if strings.HasPrefix(nodePoolVersion, controlPlaneVersion+".") {
							matchingNodePoolVersion = nodePoolVersion
							break
						}
					}

					patchVersions := computeBackLevelVersions(matchingNodePoolVersion)

					var nodePoolWg sync.WaitGroup
					for _, nodePoolVersion := range patchVersions {
						nodePoolWg.Add(1)
						go func(nodePoolVersion string) {
							defer nodePoolWg.Done()

							nodePoolSuffix := strings.ReplaceAll(nodePoolVersion, ".", "-")
							nodePoolName := customerNodePoolName + nodePoolSuffix

							nodePoolParams := framework.NewDefaultNodePoolParams()
							nodePoolParams.ClusterName = clusterName
							nodePoolParams.NodePoolName = nodePoolName
							nodePoolParams.OpenshiftVersionId = nodePoolVersion
							By("creating node pool version " + nodePoolVersion + " and verifying a simple web app can run")
							err := tc.CreateNodePoolFromParam(ctx,
								*resourceGroup.Name,
								clusterName,
								nodePoolParams,
								45*time.Minute,
							)
							if err != nil {
								GinkgoLogr.Error(err, "node pool creation failed",
									"controlPlaneVersion", controlPlaneVersion,
									"nodePoolVersion", nodePoolVersion,
									"cluster", clusterName,
									"nodePool", nodePoolName)
								errorsMutex.Lock()
								errors = append(errors, err)
								errorsMutex.Unlock()
								return
							}

							nodePoolLabel := fmt.Sprintf("%s-%s", clusterName, nodePoolName)
							nodeSelector := map[string]string{"hypershift.openshift.io/nodePool": nodePoolLabel}
							err = verifiers.VerifySimpleWebApp(nodeSelector).Verify(ctx, adminRESTConfig)
							if err != nil {
								GinkgoLogr.Error(err, "node pool workload verification failed",
									"controlPlaneVersion", controlPlaneVersion,
									"nodePoolVersion", nodePoolVersion,
									"cluster", clusterName,
									"nodePool", nodePoolName)
								errorsMutex.Lock()
								errors = append(errors, err)
								errorsMutex.Unlock()
							}
						}(nodePoolVersion)
					}
					nodePoolWg.Wait()
				}(controlPlaneVersion)
			}

			wg.Wait()

			for _, err := range errors {
				Expect(err).NotTo(HaveOccurred())
			}

		})
})

func computeBackLevelVersions(version string) []string {
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return []string{version}
	}

	majorMinor := parts[0] + "." + parts[1]
	patchAtoi, _ := strconv.Atoi(parts[2])

	var versions []string
	versions = append(versions, majorMinor+".0")

	if patchAtoi > 0 {
		versions = append(versions, majorMinor+"."+strconv.Itoa(patchAtoi-1))
	}

	versions = append(versions, version)

	return versions
}
