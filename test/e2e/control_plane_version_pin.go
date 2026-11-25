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

	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/metadataapi"
	clusterversion "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/version"
	hcpsdk20261001 "github.com/Azure/ARO-HCP/test/sdk/v20261001/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

type controlPlaneVersionPinResponse struct {
	ExactVersion      string `json:"exactVersion"`
	UntilExactVersion string `json:"untilExactVersion"`
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
		labels.StartPriority(60),
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

	It("rejects a customer minor upgrade that conflicts with an SRE pin and leaves the cluster healthy admin api",
		labels.RequireNothing,
		labels.High,
		labels.Negative,
		labels.CoreInfraService,
		labels.DevelopmentOnly,
		labels.AroRpApiCompatible,
		labels.MIContainers(1),
		labels.StartPriority(60),
		func(ctx context.Context) {
			// This covers two requirements in one cluster, because mechanically they are
			// the same run:
			//
			//  1. The pin is observable at all. There is no read endpoint for it: none of
			//     the per-cluster admin GET routes return
			//     ServiceProviderClusterSpec.PinnedVersion. The only observable surface is
			//     the cluster-update operation check, which fails a version.id change
			//     whose release line differs from the forced version and names that
			//     forced version in its message.
			//  2. A pinned cluster does not follow a customer-requested Y-stream upgrade.
			//     MinorUpgradeNormalClusterDesiredVersion declines to assign a desired
			//     version while a forced version is present (rollout/versions.go
			//     hasForcedVersion), and the operation check fails the request outright.
			//     The rejection is clean: the control plane stays where the pin put it.
			//
			// Pinning to the version the cluster is already running keeps this cheap:
			// nothing upgrades, so no second version has to exist.
			channelGroup := framework.DefaultOpenshiftChannelGroup()
			if channelGroup == string(metadataapi.ChannelGroupNightly) {
				Skip("nightly is not served by the update service graph API, so no concrete install version can be resolved")
			}
			requestedVersion, err := semver.ParseTolerant(framework.DefaultOpenshiftControlPlaneVersionId())
			Expect(err).NotTo(HaveOccurred(), "failed to parse default control-plane version")
			minorVersion := fmt.Sprintf("%d.%d", requestedVersion.Major, requestedVersion.Minor)
			nextMinorVersion := fmt.Sprintf("%d.%d", requestedVersion.Major, requestedVersion.Minor+1)
			channel := fmt.Sprintf("%s-%s", channelGroup, minorVersion)

			installRelease, err := framework.SelectControlPlaneVersion(ctx, http.DefaultTransport.RoundTrip, nil, channel, clusterversion.GetZStreamOffset(channelGroup))
			if err != nil || installRelease == nil {
				Skip(fmt.Sprintf("no install release available for %s: %v", channel, err))
			}
			installVersion, err := semver.Parse(installRelease.Version)
			Expect(err).NotTo(HaveOccurred(), "failed to parse install release %q", installRelease.Version)

			tc := framework.NewTestContext()
			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign identity containers")
			}
			clusterName := "sre-pin-observe-" + rand.String(6)
			resourceGroup, err := tc.NewResourceGroup(ctx, "admin-api-pin-observe", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for pin observability test")
			clusterParams := framework.NewDefaultClusterParams20261001()
			clusterParams.ClusterName = clusterName
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.OpenshiftVersionId = installVersion.String()
			clusterParams.ChannelGroup = channelGroup
			clusterParams, err = tc.CreateClusterCustomerResources20261001(ctx,
				resourceGroup,
				clusterParams,
				map[string]any{
					"customerNsgName":        "sre-pin-observe-nsg",
					"customerVnetName":       "sre-pin-observe-vnet",
					"customerVnetSubnetName": "sre-pin-observe-subnet",
				},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for pin observability cluster")

			By(fmt.Sprintf("creating cluster %s at exact version %s", clusterName, installVersion))
			err = tc.CreateHCPClusterFromParam20261001(ctx, GinkgoLogr, *resourceGroup.Name, clusterParams, nil, framework.ClusterCreationTimeout)
			Expect(err).NotTo(HaveOccurred(), "failed to create cluster %q at %s", clusterName, installVersion)

			hcpClient := tc.Get20261001ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20261001(ctx, hcpClient,
				*resourceGroup.Name, clusterName, framework.GetAdminRESTConfigTimeout)
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

			// The creation-time exact-version tag produces the same conflict message as
			// an SRE pin, so the message only proves the pin once the tag is gone.
			// Build the PATCH tag set from the cluster's current tags, not from
			// ClusterParams.Tags: BuildHCPClusterFromParams20261001 adds tags of its own
			// at creation (the exact-version tag, and disable-swift via
			// buildClusterNetworking) that ClusterParams.Tags never sees, and an ARM
			// PATCH replaces the whole tag set.
			By("removing the creation-time exact override")
			current, err := hcpClient.Get(ctx, *resourceGroup.Name, clusterName, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to read current tags for cluster %q", clusterName)
			versionLine, overrideTags := framework.ControlPlaneExactVersionPatchTags(minorVersion)
			patchTags := framework.TagsForPatch(current.Tags, overrideTags)
			_, err = framework.UpdateHCPCluster20261001(ctx, hcpClient, *resourceGroup.Name, clusterName,
				hcpsdk20261001.HcpOpenShiftCluster{
					Tags: patchTags,
					Properties: &hcpsdk20261001.HcpOpenShiftClusterProperties{
						Version: &hcpsdk20261001.VersionProfile{
							ID:           to.Ptr(versionLine),
							ChannelGroup: to.Ptr(channelGroup),
						},
					},
				}, framework.UpdateHCPClusterTimeout)
			Expect(err).NotTo(HaveOccurred(), "failed to remove creation-time exact override for cluster %q", clusterName)

			hcpResourceID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/%s",
				metadataapi.Must(tc.SubscriptionID(ctx)), *resourceGroup.Name, clusterName)
			httpClient, adminAPIAddress, err := tc.NewAdminAPIHTTPClient(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to create admin API HTTP client")
			pinURL := fmt.Sprintf("%s/admin/v1/hcp%s/controlplaneversionpin", adminAPIAddress, hcpResourceID)

			By(fmt.Sprintf("pinning the control plane to its current version %s", installVersion))
			pinResponse, err := framework.DoAdminAPIRequest[controlPlaneVersionPinResponse](ctx, httpClient, http.MethodPost,
				pinURL, http.StatusOK, strings.NewReader(fmt.Sprintf(`{"exactVersion":%q}`, installVersion.String())))
			Expect(err).NotTo(HaveOccurred(), "failed to pin cluster %q to %s", clusterName, installVersion)
			Expect(pinResponse.ExactVersion).To(Equal(installVersion.String()), "Admin API returned an unexpected pinned version")
			Expect(pinResponse.UntilExactVersion).To(BeEmpty(), "Admin API returned an auto-release threshold that was never requested")

			By(fmt.Sprintf("requesting a minor upgrade to %s, which conflicts with the pin", nextMinorVersion))
			_, err = framework.UpdateHCPCluster20261001(ctx, hcpClient, *resourceGroup.Name, clusterName,
				hcpsdk20261001.HcpOpenShiftCluster{
					Tags: patchTags,
					Properties: &hcpsdk20261001.HcpOpenShiftClusterProperties{
						Version: &hcpsdk20261001.VersionProfile{
							ID:           to.Ptr(nextMinorVersion),
							ChannelGroup: to.Ptr(channelGroup),
						},
					},
				}, framework.UpdateHCPClusterTimeout)
			Expect(err).To(HaveOccurred(), "minor upgrade of pinned cluster %q to %s unexpectedly succeeded", clusterName, nextMinorVersion)
			Expect(err.Error()).To(ContainSubstring(fmt.Sprintf("conflicts with forced control plane version %s", installVersion)),
				"the failed update for cluster %q did not report the SRE pin; got: %v", clusterName, err)

			By(fmt.Sprintf("verifying the rejected upgrade left cluster %q healthy at %s", clusterName, installVersion))
			Expect(verifiers.VerifyHCPCluster(ctx, adminRESTConfig)).To(Succeed(),
				"cluster %q is no longer viable after the rejected minor upgrade", clusterName)
			Consistently(currentDesiredVersion, 2*time.Minute, 30*time.Second).Should(Equal(installVersion.String()),
				"cluster %q moved off the pinned version %s after the rejected minor upgrade", clusterName, installVersion)
		})

	It("holds a pinned cluster until the fleet best version reaches the auto-release threshold admin api",
		labels.RequireNothing,
		labels.High,
		labels.Positive,
		labels.CoreInfraService,
		labels.DevelopmentOnly,
		labels.AroRpApiCompatible,
		labels.MIContainers(1),
		labels.StartPriority(60),
		func(ctx context.Context) {
			channelGroup := framework.DefaultOpenshiftChannelGroup()
			if channelGroup == string(metadataapi.ChannelGroupNightly) {
				Skip("nightly channels have no rollout best version, so untilExactVersion is rejected")
			}
			requestedVersion, err := semver.ParseTolerant(framework.DefaultOpenshiftControlPlaneVersionId())
			Expect(err).NotTo(HaveOccurred(), "failed to parse default control-plane version")
			minorVersion := fmt.Sprintf("%d.%d", requestedVersion.Major, requestedVersion.Minor)
			channel := fmt.Sprintf("%s-%s", channelGroup, minorVersion)
			bestOffset := clusterversion.GetZStreamOffset(channelGroup)

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
				Skip(fmt.Sprintf("channel %s has no newer release than %s to release the pin onto", channel, installVersion))
			}
			// An unreachable threshold in the pinned release line. The fleet's best
			// version can never reach it, so the pin holds until the test lowers it.
			unreachableVersion := semver.Version{Major: bestVersion.Major, Minor: bestVersion.Minor, Patch: bestVersion.Patch + 100}

			tc := framework.NewTestContext()
			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign identity containers")
			}
			clusterName := "sre-pin-hold-" + rand.String(6)
			resourceGroup, err := tc.NewResourceGroup(ctx, "admin-api-pin-hold", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for pin hold test")
			clusterParams := framework.NewDefaultClusterParams20261001()
			clusterParams.ClusterName = clusterName
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.OpenshiftVersionId = installVersion.String()
			clusterParams.ChannelGroup = channelGroup
			clusterParams, err = tc.CreateClusterCustomerResources20261001(ctx,
				resourceGroup,
				clusterParams,
				map[string]any{
					"customerNsgName":        "sre-pin-hold-nsg",
					"customerVnetName":       "sre-pin-hold-vnet",
					"customerVnetSubnetName": "sre-pin-hold-subnet",
				},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for pin hold cluster")

			By(fmt.Sprintf("creating cluster %s at exact version %s", clusterName, installVersion))
			err = tc.CreateHCPClusterFromParam20261001(ctx, GinkgoLogr, *resourceGroup.Name, clusterParams, nil, framework.ClusterCreationTimeout)
			Expect(err).NotTo(HaveOccurred(), "failed to create cluster %q at %s", clusterName, installVersion)

			hcpClient := tc.Get20261001ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20261001(ctx, hcpClient,
				*resourceGroup.Name, clusterName, framework.GetAdminRESTConfigTimeout)
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

			By(fmt.Sprintf("pinning the control plane at %s until the fleet best version reaches %s", installVersion, unreachableVersion))
			pinResponse, err := framework.DoAdminAPIRequest[controlPlaneVersionPinResponse](ctx, httpClient, http.MethodPost,
				pinURL, http.StatusOK, strings.NewReader(fmt.Sprintf(`{"exactVersion":%q,"untilExactVersion":%q}`,
					installVersion.String(), unreachableVersion.String())))
			Expect(err).NotTo(HaveOccurred(), "failed to pin cluster %q at %s", clusterName, installVersion)
			Expect(pinResponse.ExactVersion).To(Equal(installVersion.String()), "Admin API returned an unexpected pinned version")
			Expect(pinResponse.UntilExactVersion).To(Equal(unreachableVersion.String()), "Admin API returned an unexpected auto-release threshold")

			// With the creation-time exact override gone, the pin is the only thing
			// holding the cluster below the channel's best version. Build the PATCH tag
			// set from the cluster's current tags, not from ClusterParams.Tags:
			// BuildHCPClusterFromParams20261001 adds tags of its own at creation (the
			// exact-version tag, and disable-swift via buildClusterNetworking) that
			// ClusterParams.Tags never sees, and an ARM PATCH replaces the whole tag set.
			By("removing the creation-time exact override")
			current, err := hcpClient.Get(ctx, *resourceGroup.Name, clusterName, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to read current tags for cluster %q", clusterName)
			versionLine, overrideTags := framework.ControlPlaneExactVersionPatchTags(minorVersion)
			_, err = framework.UpdateHCPCluster20261001(ctx, hcpClient, *resourceGroup.Name, clusterName,
				hcpsdk20261001.HcpOpenShiftCluster{
					Tags: framework.TagsForPatch(current.Tags, overrideTags),
					Properties: &hcpsdk20261001.HcpOpenShiftClusterProperties{
						Version: &hcpsdk20261001.VersionProfile{
							ID:           to.Ptr(versionLine),
							ChannelGroup: to.Ptr(channelGroup),
						},
					},
				}, framework.UpdateHCPClusterTimeout)
			Expect(err).NotTo(HaveOccurred(), "failed to remove creation-time exact override for cluster %q", clusterName)

			By(fmt.Sprintf("verifying the pin holds the control plane at %s", installVersion))
			Consistently(currentDesiredVersion, 3*time.Minute, 30*time.Second).Should(Equal(installVersion.String()),
				"cluster %q moved off the pinned version %s before the auto-release threshold was reached", clusterName, installVersion)

			// This crosses the release threshold by lowering untilExactVersion, not by
			// advancing the fleet's BestExactVersion. It exercises the same controller
			// branch (best >= until), but it does not prove best-version discovery.
			By(fmt.Sprintf("lowering the auto-release threshold to %s so the fleet best version %s reaches it", installVersion, bestVersion))
			releaseResponse, err := framework.DoAdminAPIRequest[controlPlaneVersionPinResponse](ctx, httpClient, http.MethodPost,
				pinURL, http.StatusOK, strings.NewReader(fmt.Sprintf(`{"exactVersion":%q,"untilExactVersion":%q}`,
					installVersion.String(), installVersion.String())))
			Expect(err).NotTo(HaveOccurred(), "failed to lower the auto-release threshold for cluster %q", clusterName)
			Expect(releaseResponse.UntilExactVersion).To(Equal(installVersion.String()), "Admin API returned an unexpected auto-release threshold")

			By(fmt.Sprintf("verifying the released pin advances the control plane to the fleet best version %s", bestVersion))
			Eventually(currentDesiredVersion, framework.HCPClusterVersionUpgradeTimeout, 2*time.Minute).Should(Equal(bestVersion.String()),
				"cluster %q did not adopt the fleet best version %s after the pin auto-released", clusterName, bestVersion)
			// A control-plane-only cluster can leave ClusterVersion history Partial
			// while worker-dependent operators are unavailable. Check that the best
			// version is being applied, without requiring completion.
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
				"cluster %q did not start applying the fleet best version %s", clusterName, bestVersion)
		})
})
