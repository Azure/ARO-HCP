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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Customer", func() {

	backlevelVersions := []string{"4.19"}

	for _, version := range backlevelVersions {
		version := version // capture loop variable
		It("should be able to create an HCP cluster with back-level version "+version,
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

				if tc.UsePooledIdentities() {
					err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
					Expect(err).NotTo(HaveOccurred())
				}

				By("creating a resource group")
				resourceGroup, err := tc.NewResourceGroup(ctx, "rg-cluster-back-version", tc.Location())
				Expect(err).NotTo(HaveOccurred())

				clusterSuffix := strings.ReplaceAll(version, ".", "-")
				clusterName := customerClusterName + clusterSuffix

				clusterParams := framework.NewDefaultClusterParams()
				clusterParams.ClusterName = clusterName
				managedResourceGroupName := framework.SuffixName(*resourceGroup.Name+"-"+clusterSuffix, "-managed", 64)
				clusterParams.ManagedResourceGroupName = managedResourceGroupName
				clusterParams.OpenshiftVersionId = version

				// copied 4.19 defaults from 01/22/2026 snapshot of NewDefaultClusterParams
				clusterParams.Network = framework.NetworkConfig{
					NetworkType: "OVNKubernetes",
					PodCIDR:     "10.128.0.0/14",
					ServiceCIDR: "172.30.0.0/16",
					MachineCIDR: "10.0.0.0/16",
					HostPrefix:  23,
				}
				clusterParams.EncryptionKeyManagementMode = "CustomerManaged"
				clusterParams.EncryptionType = "KMS"
				clusterParams.APIVisibility = "Public"
				clusterParams.ImageRegistryState = "Enabled"
				clusterParams.ChannelGroup = "stable"

				clusterParams, err = tc.CreateClusterCustomerResources(ctx,
					resourceGroup,
					clusterParams,
					map[string]any{
						"customerNsgName":        customerNetworkSecurityGroupName + clusterSuffix,
						"customerVnetName":       customerVnetName + clusterSuffix,
						"customerVnetSubnetName": customerVnetSubnetName + clusterSuffix,
					},
					TestArtifactsFS,
					framework.RBACScopeResourceGroup,
				)
				Expect(err).NotTo(HaveOccurred())

				By("creating HCP cluster version " + version)
				err = tc.CreateHCPClusterFromParam(
					ctx,
					GinkgoLogr,
					*resourceGroup.Name,
					clusterParams,
					45*time.Minute,
				)
				Expect(err).NotTo(HaveOccurred())

				adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster(
					ctx,
					tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
					*resourceGroup.Name,
					clusterName,
					10*time.Minute,
				)
				Expect(err).NotTo(HaveOccurred())

				err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
				Expect(err).NotTo(HaveOccurred())

				By("creating node pool with back-level version")
				backlevelNodePoolVersions := []string{"4.19.7"}

				var matchingNodePoolVersion string
				for _, nodePoolVersion := range backlevelNodePoolVersions {
					if strings.HasPrefix(nodePoolVersion, version+".") {
						matchingNodePoolVersion = nodePoolVersion
						break
					}
				}

				if matchingNodePoolVersion != "" {
					nodePoolSuffix := strings.ReplaceAll(matchingNodePoolVersion, ".", "-")
					nodePoolName := customerNodePoolName + nodePoolSuffix
					nodePoolParams := framework.NewDefaultNodePoolParams()
					nodePoolParams.ClusterName = clusterName
					nodePoolParams.NodePoolName = nodePoolName
					nodePoolParams.OpenshiftVersionId = matchingNodePoolVersion

					// copied 4.19 defaults from 01/22/2026 snapshot of NewDefaultNodePoolParams
					nodePoolParams.Replicas = int32(2)
					nodePoolParams.VMSize = "Standard_D8s_v3"
					nodePoolParams.OSDiskSizeGiB = int32(64)
					nodePoolParams.DiskStorageAccountType = "StandardSSD_LRS"
					nodePoolParams.ChannelGroup = "stable"

					By("creating node pool version " + matchingNodePoolVersion + " and verifying a simple web app can run")
					err = tc.CreateNodePoolFromParam(ctx,
						*resourceGroup.Name,
						clusterName,
						nodePoolParams,
						45*time.Minute,
					)
					Expect(err).NotTo(HaveOccurred())

					nodePoolLabel := fmt.Sprintf("%s-%s", clusterName, nodePoolName)
					nodeSelector := map[string]string{"hypershift.openshift.io/nodePool": nodePoolLabel}
					err = verifiers.VerifySimpleWebApp(nodeSelector).Verify(ctx, adminRESTConfig)
					Expect(err).NotTo(HaveOccurred())
				}

			})
	}
})
