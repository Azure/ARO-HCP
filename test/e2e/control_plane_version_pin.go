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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/blang/semver/v4"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/discovery"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"

	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/coreapi"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/metadataapi"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/storage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/storage/cosmosclient"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/storage/cosmosratelimit"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/storage/fleetcosmosstorage"
	clusterversion "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/version"
	hcpsdk20261001 "github.com/Azure/ARO-HCP/test/sdk/v20261001/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

type controlPlaneVersionPinResponse struct {
	ExactVersion      string `json:"exactVersion"`
	UntilExactVersion string `json:"untilExactVersion,omitempty"`
}

var _ = Describe("SRE", func() {
	DescribeTable("",
		labels.RequireNothing,
		labels.High,
		labels.Positive,
		labels.CoreInfraService,
		labels.DevelopmentOnly,
		labels.AroRpApiCompatible,
		labels.MIContainers(1),
		labels.StartPriority(60),
		func(ctx context.Context, autoRelease bool) {
			target := os.Getenv("ARO_HCP_TEST_PIN_VERSION")
			expectedCommit := os.Getenv("ARO_HCP_TEST_PIN_KUBE_GIT_COMMIT")
			baselineVersion := os.Getenv("ARO_HCP_TEST_PIN_BASELINE_VERSION")
			if autoRelease && baselineVersion == "" {
				Skip("set ARO_HCP_TEST_PIN_BASELINE_VERSION to exercise automatic release onto stable")
			}
			if !autoRelease && target == "" && expectedCommit == "" {
				Skip("set ARO_HCP_TEST_PIN_VERSION and ARO_HCP_TEST_PIN_KUBE_GIT_COMMIT to test an unreleased build")
			}
			targetVersion, err := semver.Parse(target)
			Expect(err).NotTo(HaveOccurred(), "ARO_HCP_TEST_PIN_VERSION must be an exact release version")
			Expect(expectedCommit).To(MatchRegexp("^[0-9a-f]{40}$"), "ARO_HCP_TEST_PIN_KUBE_GIT_COMMIT must be the full Kubernetes source commit of the target build")

			By("selecting a stable install version on the target release line")
			channelGroup := string(metadataapi.ChannelGroupStable)
			channel := fmt.Sprintf("%s-%d.%d", channelGroup, targetVersion.Major, targetVersion.Minor)
			var holdUntil semver.Version
			var fleet fleetcosmosstorage.FleetDBClient
			var resources corecosmosstorage.ResourcesDBClient
			// The regional rollout is the authoritative input to the forced assignment controller.
			readBest := func(ctx context.Context) (*semver.Version, error) {
				rollout, err := fleet.ControlPlaneVersionRollouts().Get(ctx, channel)
				if err != nil {
					return nil, err
				}
				if rollout.Spec.BestExactVersion == nil {
					return nil, fmt.Errorf("rollout %s has no best version", channel)
				}
				return rollout.Spec.BestExactVersion, nil
			}
			if autoRelease {
				baseline, err := semver.Parse(baselineVersion)
				Expect(err).NotTo(HaveOccurred(), "invalid exact baseline version %q", baselineVersion)
				Expect(len(baseline.Pre)+len(baseline.Build)).To(BeZero(), "baseline must be a stable X.Y.Z release")
				Expect(targetVersion.Pre).NotTo(BeEmpty(), "the pin must name a prerelease")
				Expect(targetVersion.Build).To(BeEmpty(), "the pin cannot contain build metadata")
				Expect(baseline.Major == targetVersion.Major && baseline.Minor == targetVersion.Minor && baseline.LT(targetVersion)).To(BeTrue(),
					"baseline %s must precede prerelease %s on the same minor line", baseline, target)
				endpoint, database := os.Getenv("COSMOS_URL"), os.Getenv("COSMOS_NAME")
				Expect(endpoint).NotTo(BeEmpty(), "set COSMOS_URL to the test region's Cosmos endpoint")
				Expect(database).NotTo(BeEmpty(), "set COSMOS_NAME to the test region's database")
				fleet, err = fleetcosmosstorage.NewFleetDBClient(endpoint, database, cosmosclient.Options{}, cosmosratelimit.NewUnlimitedTokenBucket("pin-e2e-fleet"))
				Expect(err).NotTo(HaveOccurred(), "failed to open fleet reader")
				resources, err = corecosmosstorage.NewResourcesDBClient(endpoint, database, cosmosclient.Options{}, cosmosratelimit.NewUnlimitedTokenBucket("pin-e2e-resources"))
				Expect(err).NotTo(HaveOccurred(), "failed to open cluster resource reader")
				best, err := readBest(ctx)
				Expect(err).NotTo(HaveOccurred(), "failed to read regional rollout %s", channel)
				Expect(best.Major == targetVersion.Major && best.Minor == targetVersion.Minor && len(best.Build)+len(best.Pre) == 0 && best.GTE(targetVersion)).To(BeTrue(),
					"regional %s best %s must be a stable release at or above pin %s", channel, best, target)
				holdUntil = semver.Version{Major: best.Major, Minor: best.Minor, Patch: best.Patch + 100}
			} else {
				install, err := framework.SelectControlPlaneVersion(ctx, http.DefaultTransport.RoundTrip, nil, channel, clusterversion.GetZStreamOffset(channelGroup))
				Expect(err).NotTo(HaveOccurred(), "failed to select a baseline release from %s", channel)
				Expect(install).NotTo(BeNil(), "no baseline release found in %s", channel)
				Expect(install.Version).NotTo(Equal(target), "the stable baseline and pin target must differ")
				baselineVersion = install.Version
			}

			tc := framework.NewTestContext()
			if tc.UsePooledIdentities() {
				Expect(tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)).To(Succeed(), "failed to assign identity containers")
			}
			clusterName := "sre-pin-build-" + rand.String(6)
			resourceGroup, err := tc.NewResourceGroup(ctx, "admin-api-pin-build", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for %s", clusterName)
			params := framework.NewDefaultClusterParams20261001()
			params.ClusterName = clusterName
			params.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			params.OpenshiftVersionId = baselineVersion
			params.ChannelGroup = channelGroup
			params, err = tc.CreateClusterCustomerResources20261001(ctx, resourceGroup, params, map[string]any{
				"customerNsgName":        "sre-pin-build-nsg",
				"customerVnetName":       "sre-pin-build-vnet",
				"customerVnetSubnetName": "sre-pin-build-subnet",
			}, TestArtifactsFS, framework.RBACScopeResourceGroup)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for %s", clusterName)

			By(fmt.Sprintf("creating %s at %s and reading the running kube-apiserver build", clusterName, baselineVersion))
			Expect(tc.CreateHCPClusterFromParam20261001(ctx, GinkgoLogr, *resourceGroup.Name, params, nil, framework.ClusterCreationTimeout)).To(Succeed(), "failed to create %s", clusterName)
			hcpClient := tc.Get20261001ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
			config, err := tc.GetAdminRESTConfigForHCPCluster20261001(ctx, hcpClient, *resourceGroup.Name, clusterName, framework.GetAdminRESTConfigTimeout)
			Expect(err).NotTo(HaveOccurred(), "failed to get guest access for %s", clusterName)
			configClient, err := configv1client.NewForConfig(config)
			Expect(err).NotTo(HaveOccurred(), "failed to create guest ClusterVersion client")
			guestRelease := func(ctx context.Context) (string, error) {
				cv, err := configClient.ClusterVersions().Get(ctx, "version", metav1.GetOptions{})
				if err != nil {
					return "", err
				}
				return cv.Status.Desired.Version, nil
			}
			Eventually(ctx, guestRelease, 5*time.Minute, 15*time.Second).Should(Equal(baselineVersion),
				"guest did not reach baseline version %s", baselineVersion)
			guest, err := discovery.NewDiscoveryClientForConfig(config)
			Expect(err).NotTo(HaveOccurred(), "failed to create discovery client for %s", clusterName)
			runningCommit := func(ctx context.Context) (string, error) {
				info, err := guest.ServerVersionWithContext(ctx)
				if err != nil {
					return "", err
				}
				return info.GitCommit, nil
			}
			baselineCommit, err := runningCommit(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to read kube-apiserver /version before pinning %s", clusterName)
			Expect(baselineCommit).NotTo(BeEmpty(), "baseline kube-apiserver did not report a Git commit")
			Expect(baselineCommit).NotTo(Equal(expectedCommit), "baseline kube-apiserver already serves the target build")

			By(fmt.Sprintf("pinning %s to %s", clusterName, target))
			httpClient, adminAPIAddress, err := tc.NewAdminAPIHTTPClient(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to create admin API HTTP client")
			pinURL := fmt.Sprintf("%s/admin/v1/hcp%s/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/%s/controlplaneversionpin", adminAPIAddress, *resourceGroup.ID, clusterName)
			request := controlPlaneVersionPinResponse{ExactVersion: target}
			if autoRelease {
				request.UntilExactVersion = holdUntil.String()
			}
			postPin := func(request controlPlaneVersionPinResponse) {
				GinkgoHelper()
				body, err := json.Marshal(request)
				Expect(err).NotTo(HaveOccurred(), "failed to encode pin request")
				pin, err := framework.DoAdminAPIRequest[controlPlaneVersionPinResponse](ctx, httpClient, http.MethodPost, pinURL, http.StatusOK, bytes.NewReader(body))
				Expect(err).NotTo(HaveOccurred(), "failed to pin %s to %s until %s", clusterName, request.ExactVersion, request.UntilExactVersion)
				Expect(pin).To(BeComparableTo(request), "admin API returned an unexpected pin or release threshold")
			}
			postPin(request)

			// A single response from the target build proves at least one API server
			// rolled. The guest ClusterVersion can stay at the baseline during this update.
			By("waiting for the running kube-apiserver to serve the pinned build")
			Eventually(ctx, runningCommit, framework.HCPClusterVersionUpgradeTimeout, 30*time.Second).Should(Equal(expectedCommit),
				"cluster %s did not start serving pin %s (baseline commit %s)", clusterName, target, baselineCommit)
			if !autoRelease {
				Eventually(ctx, guestRelease, 5*time.Minute, 15*time.Second).Should(Equal(baselineVersion),
					"guest ClusterVersion did not report the baseline after the control-plane pin")
				return
			}
			subscription, err := tc.SubscriptionID(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to identify the cluster subscription")
			provider := resources.ServiceProviderClusters(subscription, *resourceGroup.Name, clusterName)
			readProvider := func(ctx context.Context) (*coreapi.ServiceProviderCluster, error) {
				return provider.Get(ctx, coreapi.ServiceProviderClusterResourceName)
			}
			minorVersion := fmt.Sprintf("%d.%d", targetVersion.Major, targetVersion.Minor)
			removeCreationVersionOverride(ctx, hcpClient, *resourceGroup.Name, clusterName, minorVersion, channelGroup)

			By("checking the prerelease remains assigned after removing the creation override")
			Eventually(ctx, func(g Gomega, ctx context.Context) {
				sp, err := readProvider(ctx)
				g.Expect(err).NotTo(HaveOccurred(), "failed to read provider cluster %s", clusterName)
				g.Expect(sp.Spec.PinnedVersion.ExactVersion).To(BeComparableTo(&targetVersion), "prerelease pin must still be set")
				g.Expect(sp.Spec.PinnedVersion.UntilExactVersion).To(BeComparableTo(&holdUntil), "initial threshold must still be set")
				g.Expect(sp.Spec.ControlPlaneVersion.DesiredVersion).To(BeComparableTo(&targetVersion), "prerelease must still be assigned")
			}, 5*time.Minute, 15*time.Second).Should(Succeed())
			best, err := readBest(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to read regional rollout %s before lowering the threshold", channel)
			Expect(best.GTE(targetVersion) && best.LT(holdUntil) && best.Major == targetVersion.Major && best.Minor == targetVersion.Minor &&
				len(best.Pre)+len(best.Build) == 0).To(BeTrue(), "fleet best %s must be stable and between pin %s and original threshold %s", best, target, holdUntil)
			By(fmt.Sprintf("lowering only untilExactVersion to the current fleet best %s", best))
			request.UntilExactVersion = best.String()
			postPin(request)
			verifyPrereleasePinRelease(ctx, clusterName, *best, readBest, readProvider)
		},
		Entry("starts applying an unreleased control plane version pinned through the admin api", false),
		Entry("releases a prerelease control plane pin after lowering its threshold to the stable fleet best", true),
	)
})

func removeCreationVersionOverride(ctx context.Context, hcpClient *hcpsdk20261001.HcpOpenShiftClustersClient, resourceGroup, clusterName, minorVersion, channelGroup string) {
	GinkgoHelper()
	// ARM replaces the tag set on PATCH. Preserve tags added during provisioning.
	By("removing the creation-time exact override")
	current, err := hcpClient.Get(ctx, resourceGroup, clusterName, nil)
	Expect(err).NotTo(HaveOccurred(), "failed to read current tags for cluster %q", clusterName)
	versionLine, overrideTags := framework.ControlPlaneExactVersionPatchTags(minorVersion)
	_, err = framework.UpdateHCPCluster20261001(ctx, hcpClient, resourceGroup, clusterName,
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
}

func verifyPrereleasePinRelease(ctx context.Context, clusterName string, threshold semver.Version,
	readBest func(context.Context) (*semver.Version, error), readProvider func(context.Context) (*coreapi.ServiceProviderCluster, error)) {
	GinkgoHelper()
	By("verifying the backend cleared the pin and the control plane started applying the stable release")
	Eventually(ctx, func(g Gomega, ctx context.Context) {
		sp, err := readProvider(ctx)
		g.Expect(err).NotTo(HaveOccurred(), "failed to read provider cluster %s", clusterName)
		g.Expect(sp.Spec.PinnedVersion.ExactVersion).To(BeNil(), "the prerelease pin is still set")
		g.Expect(sp.Spec.PinnedVersion.UntilExactVersion).To(BeNil(), "the release threshold is still set")
		desired := sp.Spec.ControlPlaneVersion.DesiredVersion
		g.Expect(desired).NotTo(BeNil(), "control plane has no assigned version")
		g.Expect(desired.Major == threshold.Major && desired.Minor == threshold.Minor && len(desired.Pre) == 0 && desired.GTE(threshold)).To(BeTrue(),
			"expected a stable assignment on the same line at or above %s, got %s", threshold, desired)
		// The rollout can select a newer stable release between the second POST and reconciliation.
		if !desired.EQ(threshold) {
			best, err := readBest(ctx)
			g.Expect(err).NotTo(HaveOccurred(), "failed to read the current regional fleet best")
			g.Expect(desired.String()).To(Equal(best.String()), "stable assignment must match the fleet best")
		}
		history := sp.Status.ControlPlaneVersionHistory
		g.Expect(history).NotTo(BeNil(), "control-plane update history has not been observed")
		g.Expect(*history).NotTo(BeEmpty(), "control-plane update history is empty")
		g.Expect((*history)[0].Version).To(Equal(desired.String()), "control plane has not started applying the stable assignment")
	}, framework.HCPClusterVersionUpgradeTimeout, time.Minute).Should(Succeed())
}
