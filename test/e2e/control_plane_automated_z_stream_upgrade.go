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
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/util/rand"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	clusterversion "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/version"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controlplaneversion"
	hcpsdk20260901preview "github.com/Azure/ARO-HCP/test/sdk/v20260901preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
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

			channelGroup := framework.DefaultOpenshiftChannelGroup()
			// The nightly channel group is not served by the OpenShift update service graph API, so we
			// cannot resolve a concrete z-stream to install one release behind the tip. There is nothing
			// to exercise here for nightly.
			if channelGroup == "nightly" {
				Skip("automated z-stream upgrade is not supported for the nightly channel group")
			}

			tc := framework.NewTestContext()

			// Install one z-stream behind the channel tip (normalOffset+1) so the backend has a newer
			// z-stream to automatically upgrade to once the customer pins the bare minor version. If the
			// channel has no release at that offset (err) or none is resolved (nil), there is no
			// automated z-stream upgrade to exercise, so skip.
			normalOffset := clusterversion.GetZStreamOffset(channelGroup)
			desiredVersion, err := controlplaneversion.SelectControlPlaneVersion(ctx, http.DefaultTransport.RoundTrip, nil, fmt.Sprintf("%s-%s", channelGroup, minorVersion), normalOffset+1)
			if err != nil {
				Skip(fmt.Sprintf("no version resolved for channel %s-%s at offset %d: %v", channelGroup, minorVersion, normalOffset+1, err))
			}
			if desiredVersion == nil {
				Skip(fmt.Sprintf("no version resolved for channel %s-%s at offset %d", channelGroup, minorVersion, normalOffset+1))
			}
			installVersion := desiredVersion.Version

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			versionLabel := strings.ReplaceAll(minorVersion, ".", "-") // e.g. "4.20" -> "4-20"
			suffix := rand.String(6)
			clusterName := customerClusterNamePrefix + versionLabel + "-" + suffix
			clusterParams := framework.NewDefaultClusterParams20260901()
			clusterParams.ClusterName = clusterName
			clusterParams.OpenshiftVersionId = installVersion
			clusterParams.ChannelGroup = channelGroup

			By("creating resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "rg-zstream-upgrade-"+versionLabel, tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for z-stream upgrade of %s", minorVersion)

			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name+"-zstream-"+suffix, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20260901(ctx,
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
				clusterCreationTimeout = 35 * time.Minute
			}

			By(fmt.Sprintf("creating the HCP cluster at exact install version '%s' on %s channel", installVersion, channelGroup))
			err = tc.CreateHCPClusterFromParam20260901(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil,
				clusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %q with version %s on %s channel", clusterName, installVersion, channelGroup)

			By("verifying the cluster is viable")
			hcpClient := tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
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

			By(fmt.Sprintf("pinning the cluster to minor version %s to trigger an automated z-stream upgrade", minorVersion))
			update := hcpsdk20260901preview.HcpOpenShiftClusterUpdate{
				Properties: &hcpsdk20260901preview.HcpOpenShiftClusterPropertiesUpdate{
					Version: &hcpsdk20260901preview.VersionProfileUpdate{
						ID:           to.Ptr(minorVersion),
						ChannelGroup: to.Ptr(channelGroup),
					},
				},
			}
			_, err = framework.UpdateHCPCluster20260901(ctx, hcpClient, *resourceGroup.Name, clusterName, update, framework.HCPClusterVersionUpgradeTimeout)
			Expect(err).NotTo(HaveOccurred(), "failed to pin cluster %q to minor version %s", clusterName, minorVersion)

			By("verifying that only a z-stream upgrade was performed")
			Eventually(func() error {
				return verifiers.VerifyHCPCluster(ctx, adminRESTConfig, verifiers.VerifyHostedControlPlaneZStreamUpgradeOnly(installVersion))
			}, framework.HCPClusterVersionUpgradeTimeout, 2*time.Minute).Should(Succeed())
			GinkgoLogr.Info("z-stream upgrade verification passed", "installVersion", installVersion)
		},

		Entry("for 4.20", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "4.20"),
		Entry("for 4.21", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "4.21"),
		Entry("for 4.22", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.AllowRetry, "4.22"), // owner: @raelga, tracking: AROSLSRE-1319. Known-issue test, retriable during EV2 gating. Remove this label when the issue is fixed.
		Entry("for 4.23", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "4.23"),
		Entry("for 5.0", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.0"),
		Entry("for 5.1", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.1"),
		Entry("for 5.2", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.2"),
		Entry("for 5.3", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.3"),
		Entry("for 5.4", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.4"),
		Entry("for 5.5", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.5"),
		Entry("for 5.6", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.6"),
		Entry("for 5.7", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.7"),
		Entry("for 5.8", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.8"),
		Entry("for 5.9", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.9"),
		Entry("for 5.10", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.10"),
		Entry("for 5.11", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.11"),
		Entry("for 5.12", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.12"),
	)
})
