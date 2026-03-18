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

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var channelGroup = framework.DefaultOpenshiftChannelGroup()

var _ = Describe("Control plane install with "+channelGroup+" channel", func() {
	DescribeTable("should perform a control plane install with "+channelGroup+" channel",
		func(ctx context.Context, version string) {

			customerNetworkSecurityGroupName := "customer-nsg-" + channelGroup + "-"
			customerVnetName := "customer-vnet-" + channelGroup + "-"
			customerVnetSubnetName := "customer-vnet-subnet-" + channelGroup + "-"
			customerClusterNamePrefix := "cluster-" + channelGroup + "-"

			tc := framework.NewTestContext()
			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			installVersion := ""
			var err error
			if channelGroup == "nightly" {
				installVersion, err = framework.GetInstallVersionForNightlyUpgrade(ctx, version)
				if err != nil {
					Expect(err).NotTo(HaveOccurred())
				}
			} else {
				Fail("Unsupported channel group: " + channelGroup)
			}

			versionLabel := strings.ReplaceAll(version, ".", "-") // e.g. "4.20" -> "4-20"
			suffix := rand.String(6)
			clusterName := customerClusterNamePrefix + versionLabel + "-" + suffix
			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = clusterName
			clusterParams.ChannelGroup = channelGroup
			clusterParams.OpenshiftVersionId = installVersion

			By("creating resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "rg-"+channelGroup+"-"+versionLabel, tc.Location())
			Expect(err).NotTo(HaveOccurred())

			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name+"-"+channelGroup+"-"+suffix, "-managed", 64)
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

			By(fmt.Sprintf("creating the HCP cluster with version '%s' on %s channel", installVersion, channelGroup))
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
		},
		Entry("for 4.19", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Nightly, "4.19"),
		Entry("for 4.20", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Nightly, "4.20"),
		Entry("for 4.21", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Nightly, "4.21"),
		Entry("for 4.22", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Nightly, "4.22"),
		Entry("for 4.23", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Nightly, "4.23"),
	)
})
