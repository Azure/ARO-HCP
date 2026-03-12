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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/util/rand"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Control plane automated z-stream upgrade with candidate channel", func() {
	It("should perform an automated control plane z-stream upgrade using the candidate channel",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			const (
				customerNetworkSecurityGroupName = "customer-nsg-zstream-"
				customerVnetName                 = "customer-vnet-zstream-"
				customerVnetSubnetName           = "customer-vnet-subnet-zstream-"
				customerClusterNamePrefix        = "cluster-zstream-"
			)
			tc := framework.NewTestContext()
			By("creating cluster parameters with candidate channel")
			clusterParams := framework.NewDefaultClusterParams()
			suffix := rand.String(6)
			clusterName := customerClusterNamePrefix + suffix
			clusterParams.ClusterName = clusterName
			clusterParams.ChannelGroup = "candidate"
			installVersion, err := framework.GetInstallVersionForZStreamUpgrade(ctx, "candidate")
			Expect(err).NotTo(HaveOccurred())
			if len(installVersion) == 0 {
				Skip("no install version that has an available z-stream upgrade found for candidate channel")
			}

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "rg-zstream-upgrade", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			clusterParams.OpenshiftVersionId = installVersion
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name+"-zstream-"+suffix, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{
					"customerNsgName":        customerNetworkSecurityGroupName + suffix,
					"customerVnetName":       customerVnetName + suffix,
					"customerVnetSubnetName": customerVnetSubnetName + suffix,
				},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating the HCP cluster with version '" + installVersion + "' on candidate channel")
			err = tc.CreateHCPClusterFromParam(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the cluster is viable")
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

			By("verifying that only a z-stream upgrade was performed")
			Eventually(func() error {
				return verifiers.VerifyHCPCluster(ctx, adminRESTConfig, verifiers.VerifyHostedControlPlaneZStreamUpgradeOnly(installVersion))
			}, 40*time.Minute, 2*time.Minute).Should(Succeed())
		})
})
