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

	"github.com/blang/semver/v4"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"

	clusterversion "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/version"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	hcpsdk20261001 "github.com/Azure/ARO-HCP/test/sdk/v20261001/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

type controlPlaneVersionPinResponse struct {
	ExactVersion string `json:"exactVersion"`
}

var _ = Describe("SRE", func() {
	It("starts a control plane z-stream upgrade with an SRE pin",
		labels.RequireNothing,
		labels.High,
		labels.Positive,
		labels.CoreInfraService,
		labels.DevelopmentOnly,
		labels.AroRpApiCompatible,
		labels.MIContainers(1),
		func(ctx context.Context) {
			channelGroup := framework.DefaultOpenshiftChannelGroup()
			if channelGroup == string(metadataapi.ChannelGroupNightly) {
				Skip("nightly channels do not have a rollout best version for a forward pin")
			}
			requestedVersion, err := semver.ParseTolerant(framework.DefaultOpenshiftControlPlaneVersionId())
			Expect(err).NotTo(HaveOccurred(), "failed to parse default control-plane version")
			minorVersion := fmt.Sprintf("%d.%d", requestedVersion.Major, requestedVersion.Minor)
			channel := fmt.Sprintf("%s-%s", channelGroup, minorVersion)
			bestOffset := clusterversion.GetZStreamOffset(channelGroup)

			// Start one release behind the fleet's normal selection. The cluster's
			// creation-time exact override holds it there until the SRE pin advances it.
			installRelease, err := framework.SelectControlPlaneVersion(ctx, http.DefaultTransport.RoundTrip, nil, channel, bestOffset+1)
			if err != nil || installRelease == nil {
				Skip(fmt.Sprintf("no older install release available for %s at offset %d: %v", channel, bestOffset+1, err))
			}
			bestRelease, err := framework.SelectControlPlaneVersion(ctx, http.DefaultTransport.RoundTrip, nil, channel, bestOffset)
			if err != nil || bestRelease == nil {
				Skip(fmt.Sprintf("no best release available for %s at offset %d: %v", channel, bestOffset, err))
			}
			installVersion, err := semver.Parse(installRelease.Version)
			Expect(err).NotTo(HaveOccurred(), "failed to parse install release %q", installRelease.Version)
			bestVersion, err := semver.Parse(bestRelease.Version)
			Expect(err).NotTo(HaveOccurred(), "failed to parse best release %q", bestRelease.Version)
			if !bestVersion.GT(installVersion) {
				Skip(fmt.Sprintf("channel %s has no newer release than %s for a forward pin", channel, installVersion))
			}

			tc := framework.NewTestContext()
			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign identity containers")
			}
			clusterName := "sre-version-pin-" + rand.String(6)
			resourceGroup, err := tc.NewResourceGroup(ctx, "admin-api-version-pin", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for version pin test")
			clusterParams := framework.NewDefaultClusterParams20261001()
			clusterParams.ClusterName = clusterName
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.OpenshiftVersionId = installVersion.String()
			clusterParams.ChannelGroup = channelGroup
			clusterParams, err = tc.CreateClusterCustomerResources20261001(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{
					"customerNsgName":        "sre-version-pin-nsg",
					"customerVnetName":       "sre-version-pin-vnet",
					"customerVnetSubnetName": "sre-version-pin-subnet",
				},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for version pin cluster")

			By(fmt.Sprintf("creating cluster %s at exact version %s", clusterName, installVersion))
			err = tc.CreateHCPClusterFromParam20261001(ctx, GinkgoLogr, *resourceGroup.Name, clusterParams, nil, framework.ClusterCreationTimeout)
			Expect(err).NotTo(HaveOccurred(), "failed to create cluster %q at %s", clusterName, installVersion)

			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20261001(
				ctx,
				tc.Get20261001ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				clusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for cluster %q", clusterName)
			Expect(verifiers.VerifyHCPCluster(ctx, adminRESTConfig)).To(Succeed(), "cluster %q is not viable", clusterName)
			configClient, err := configv1client.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create cluster version client")
			currentDesiredVersion := func() (string, error) {
				clusterVersion, err := configClient.ClusterVersions().Get(ctx, "version", metav1.GetOptions{})
				if err != nil {
					return "", err
				}
				return clusterVersion.Status.Desired.Version, nil
			}
			Eventually(currentDesiredVersion, 5*time.Minute, 15*time.Second).Should(Equal(installVersion.String()),
				"control plane should start at the exact creation version")

			hcpResourceID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/%s",
				metadataapi.Must(tc.SubscriptionID(ctx)), *resourceGroup.Name, clusterName)
			httpClient, adminAPIAddress, err := tc.NewAdminAPIHTTPClient(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to create admin API HTTP client")
			pinURL := fmt.Sprintf("%s/admin/v1/hcp%s/controlplaneversionpin", adminAPIAddress, hcpResourceID)

			By(fmt.Sprintf("pinning the control plane forward to %s through the Admin API", bestVersion))
			pinResponse, err := framework.DoAdminAPIRequest[controlPlaneVersionPinResponse](ctx, httpClient, http.MethodPost,
				pinURL, http.StatusOK, strings.NewReader(fmt.Sprintf(`{"exactVersion":%q}`, bestVersion.String())))
			Expect(err).NotTo(HaveOccurred(), "failed to pin cluster %q to %s", clusterName, bestVersion)
			Expect(pinResponse.ExactVersion).To(Equal(bestVersion.String()), "Admin API returned an unexpected pinned version")

			// The creation-time exact override still requests installVersion. Seeing
			// bestVersion before changing the Cluster proves the SRE pin took precedence.
			By(fmt.Sprintf("verifying the SRE pin starts an upgrade to %s", bestVersion))
			Eventually(currentDesiredVersion, framework.HCPClusterVersionUpgradeTimeout, 2*time.Minute).Should(Equal(bestVersion.String()),
				"control plane desired version did not advance to the SRE pin on cluster %q", clusterName)
			// A control-plane-only cluster can leave ClusterVersion history Partial
			// while worker-dependent operators are unavailable. Check that the
			// exact pinned target is being applied, without requiring completion.
			Eventually(func() (string, error) {
				clusterVersion, err := configClient.ClusterVersions().Get(ctx, "version", metav1.GetOptions{})
				if err != nil {
					return "", err
				}
				if len(clusterVersion.Status.History) == 0 {
					return "<no history>", nil
				}
				return clusterVersion.Status.History[0].Version, nil
			}, framework.HCPClusterVersionUpgradeTimeout, 2*time.Minute).Should(Equal(bestVersion.String()),
				"cluster %q did not start applying the SRE-pinned version %s", clusterName, bestVersion)
			Expect(verifiers.VerifyHostedControlPlaneZStreamUpgradeOnly(installVersion.String()).Verify(ctx, adminRESTConfig)).
				NotTo(HaveOccurred(),
					"cluster %q did not start a z-stream upgrade beyond %s with the SRE pin", clusterName, installVersion)

			// Remove the creation-time exact override before clearing the SRE pin;
			// otherwise it would request the old version again.
			By("removing the creation-time exact override")
			update := hcpsdk20261001.HcpOpenShiftCluster{
				Properties: &hcpsdk20261001.HcpOpenShiftClusterProperties{
					Version: &hcpsdk20261001.VersionProfile{
						ID:           to.Ptr(minorVersion),
						ChannelGroup: to.Ptr(channelGroup),
					},
				},
			}
			_, err = framework.UpdateHCPCluster20261001(ctx,
				tc.Get20261001ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name, clusterName, update, framework.HCPClusterVersionUpgradeTimeout)
			Expect(err).NotTo(HaveOccurred(), "failed to remove creation-time exact override for cluster %q", clusterName)

			By("clearing the SRE pin through the Admin API")
			clearResponse, err := framework.DoAdminAPIRequest[controlPlaneVersionPinResponse](ctx, httpClient, http.MethodPost,
				pinURL, http.StatusOK, strings.NewReader(`{}`))
			Expect(err).NotTo(HaveOccurred(), "failed to clear version pin for cluster %q", clusterName)
			Expect(clearResponse.ExactVersion).To(BeEmpty(), "Admin API returned a pin after clear")
		})
})
