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

	"k8s.io/apimachinery/pkg/util/rand"

	"github.com/Azure/ARO-HCP/internal/cincinatti"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Control plane automated z-stream upgrade with candidate channel", func() {
	DescribeTable("should perform an automated control plane z-stream upgrade",
		func(ctx context.Context, version string) {
			const (
				customerNetworkSecurityGroupName = "customer-nsg-zstream-"
				customerVnetName                 = "customer-vnet-zstream-"
				customerVnetSubnetName           = "customer-vnet-subnet-zstream-"
				customerClusterNamePrefix        = "cluster-zstream-"
			)

			installVersion, hasUpgradePath, err := framework.GetInstallVersionForZStreamUpgrade(ctx, "candidate", version)
			if err != nil {
				if cincinatti.IsCincinnatiVersionNotFoundError(err) {
					Skip(fmt.Sprintf("Cincinnati returned version not found for %s", version))
				}
				Expect(err).NotTo(HaveOccurred())
			}

			tc := framework.NewTestContext()
			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			versionLabel := strings.ReplaceAll(version, ".", "-") // e.g. "4.20" -> "4-20"
			suffix := rand.String(6)
			clusterName := customerClusterNamePrefix + versionLabel + "-" + suffix
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = clusterName
			clusterParams.ChannelGroup = "candidate"
			clusterParams.OpenshiftVersionId = installVersion

			By("creating resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "rg-zstream-upgrade-"+versionLabel, tc.Location())
			Expect(err).NotTo(HaveOccurred())

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

			By(fmt.Sprintf("creating the HCP cluster with version '%s' on candidate channel", installVersion))
			err = tc.CreateHCPClusterFromParam(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				framework.ClusterCreationTimeout,
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

			if !hasUpgradePath {
				By("skipping z-stream upgrade verification: no upgrade path (cluster installed at latest)")
				return
			}

			By("verifying that only a z-stream upgrade was performed")
			Eventually(func() error {
				return verifiers.VerifyHCPCluster(ctx, adminRESTConfig, verifiers.VerifyHostedControlPlaneZStreamUpgradeOnly(installVersion))
			}, 40*time.Minute, 2*time.Minute).Should(Succeed())
			GinkgoLogr.Info("z-stream upgrade verification passed", "installVersion", installVersion)
		},
		Entry("for 4.19", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "4.19"),
		Entry("for 4.20", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "4.20"),
		Entry("for 4.21", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "4.21"),
		Entry("for 4.22", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "4.22"),
		Entry("for 4.23", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "4.23"),
	)
})
