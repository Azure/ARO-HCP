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

	"github.com/Azure/ARO-HCP/internal/cincinnati"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Service Provider", func() {
	DescribeTable("should upgrade the control plane z-stream automatically on behalf of the customer",
		labels.MIContainers(1),
		func(ctx context.Context, minorVersion string) {
			const (
				customerNetworkSecurityGroupName = "customer-nsg-zstream-"
				customerVnetName                 = "customer-vnet-zstream-"
				customerVnetSubnetName           = "customer-vnet-subnet-zstream-"
				customerClusterNamePrefix        = "cluster-zstream-"
			)

			// ARRANGE

			tc := framework.NewTestContext()

			By("checking if z-stream upgrade path exists")
			installVersion, targetVersion, err := framework.GetInstallVersionForZStreamUpgrade(ctx, "candidate", minorVersion)
			if err != nil {
				if cincinnati.IsCincinnatiVersionNotFoundError(err) {
					Skip(fmt.Sprintf("Cincinnati returned version not found for configured id %s", minorVersion))
				}
				Expect(err).NotTo(HaveOccurred(), "failed to get install version for z-stream upgrade of %s", minorVersion)
			}

			if targetVersion == "" {
				Skip(fmt.Sprintf("the z-stream version %s is latest and has no z-stream upgrade path available on the candidate channel", installVersion))
			}

			By("setting up test prerequisites")
			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			versionLabel := strings.ReplaceAll(minorVersion, ".", "-") // e.g. "4.20" -> "4-20"
			suffix := rand.String(6)
			clusterName := customerClusterNamePrefix + versionLabel + "-" + suffix

			By("creating resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "rg-zstream-upgrade-"+versionLabel, tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for z-stream upgrade of %s", minorVersion)

			By("creating customer resources")
			clusterParams := framework.NewDefaultClusterParams20240610()
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name+"-zstream-"+suffix, "-managed", 64)
			clusterParams.ClusterName = clusterName
			clusterParams.OpenshiftVersionId = installVersion
			clusterParams.ChannelGroup = "candidate" // use the candidate channel to potentially catch early z-stream upgrade issues before they reach stable.

			clusterParams, err = tc.CreateClusterCustomerResources20240610(
				ctx,
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
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for z-stream cluster %q", clusterName)

			clusterCreationTimeout := framework.ClusterCreationTimeout
			// 4.22 control plane provisioning has been consistently slower and frequently hits the default timeout.
			// Bump the create+wait budget to reduce flaky timeouts for this minor.
			if minorVersion == "4.22" {
				clusterCreationTimeout += 15 * time.Minute
			}

			// ACT

			By(fmt.Sprintf("creating the HCP cluster with version '%s' on candidate channel", installVersion))
			err = tc.CreateHCPClusterFromParam20240610(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				clusterCreationTimeout,
			)

			// ASSERT

			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %q with version %s on candidate channel", clusterName, installVersion)

			By("verifying the cluster is viable")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20260901(
				ctx,
				tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				clusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for cluster %q", clusterName)
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to verify HCP cluster %q is viable", clusterName)

			By("verifying that only a z-stream upgrade was performed")
			Eventually(func() error {
				return verifiers.VerifyHCPCluster(ctx, adminRESTConfig, verifiers.VerifyHostedControlPlaneZStreamUpgradeOnly(installVersion, targetVersion))
			}, framework.HCPClusterVersionUpgradeTimeout, 2*time.Minute).Should(Succeed())
			GinkgoLogr.Info("z-stream upgrade verification passed", "installVersion", installVersion)
		},

		Entry("for 4.19", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "4.19"),
		Entry("for 4.20", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "4.20"),
		Entry("for 4.21", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "4.21"),
		Entry("for 4.22", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.AllowRetry, "4.22"), // owner: @raelga, tracking: AROSLSRE-1319. Known-issue test, retriable during EV2 gating. Remove this label when the issue is fixed.
		Entry("for 4.23", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "4.23"),
	)
})
