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

	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/metadataapi"
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

			By("selecting the stable release from Cincinnati for the target minor")
			channelGroup := string(metadataapi.ChannelGroupStable)
			channel := fmt.Sprintf("%s-%d.%d", channelGroup, targetVersion.Major, targetVersion.Minor)
			selected, err := framework.SelectControlPlaneVersion(ctx, http.DefaultTransport.RoundTrip, nil, channel, clusterversion.GetZStreamOffset(channelGroup))
			Expect(err).NotTo(HaveOccurred(), "failed to select a stable release from %s", channel)
			Expect(selected).NotTo(BeNil(), "no stable release found in %s", channel)
			best, err := semver.Parse(selected.Version)
			Expect(err).NotTo(HaveOccurred(), "invalid selected version %q from %s", selected.Version, channel)
			Expect(len(best.Pre)+len(best.Build)).To(BeZero(), "expected a stable X.Y.Z release from %s, got %s", channel, best)
			if autoRelease {
				baseline, err := semver.Parse(baselineVersion)
				Expect(err).NotTo(HaveOccurred(), "invalid exact baseline version %q", baselineVersion)
				Expect(len(baseline.Pre)+len(baseline.Build)).To(BeZero(), "baseline must be a stable X.Y.Z release")
				Expect(targetVersion.Pre).NotTo(BeEmpty(), "the pin must name a prerelease")
				Expect(targetVersion.Build).To(BeEmpty(), "the pin cannot contain build metadata")
				Expect(baseline.Major == targetVersion.Major && baseline.Minor == targetVersion.Minor && baseline.LT(targetVersion)).To(BeTrue(),
					"baseline %s must precede prerelease %s on the same minor line", baseline, target)
				if best.LT(targetVersion) {
					Skip(fmt.Sprintf("%s selected %s, which is older than prerelease %s", channel, best, target))
				}
			} else {
				Expect(selected.Version).NotTo(Equal(target), "the stable baseline and pin target must differ")
				baselineVersion = selected.Version
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
				holdUntil := semver.Version{Major: best.Major, Minor: best.Minor, Patch: best.Patch + 100}
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
			minorVersion := fmt.Sprintf("%d.%d", targetVersion.Major, targetVersion.Minor)
			removeCreationVersionOverride(ctx, hcpClient, *resourceGroup.Name, clusterName, minorVersion, channelGroup)

			By("checking the guest still serves the prerelease after removing the creation override")
			Eventually(ctx, runningCommit, 5*time.Minute, 15*time.Second).Should(Equal(expectedCommit),
				"guest no longer serves the pinned build on %s", clusterName)
			latestGuestUpdate := func(ctx context.Context) (string, error) {
				cv, err := configClient.ClusterVersions().Get(ctx, "version", metav1.GetOptions{})
				if err != nil {
					return "", err
				}
				if len(cv.Status.History) == 0 {
					return "", fmt.Errorf("guest ClusterVersion history is empty on %s", clusterName)
				}
				return cv.Status.History[0].Version, nil
			}
			Eventually(ctx, latestGuestUpdate, 5*time.Minute, 15*time.Second).Should(Or(Equal(baselineVersion), Equal(target)),
				"expected the guest update history to show the baseline or prerelease before lowering the threshold on %s", clusterName)

			By(fmt.Sprintf("lowering only untilExactVersion to the Cincinnati-selected stable release %s", best))
			request.UntilExactVersion = best.String()
			postPin(request)

			By("waiting for guest ClusterVersion history to show the stable upgrade starting")
			Eventually(ctx, func(g Gomega, ctx context.Context) {
				latest, err := latestGuestUpdate(ctx)
				g.Expect(err).NotTo(HaveOccurred(), "failed to read guest update history on %s", clusterName)
				version, err := semver.Parse(latest)
				g.Expect(err).NotTo(HaveOccurred(), "invalid version %q in guest update history", latest)
				g.Expect(version.Major == best.Major && version.Minor == best.Minor && len(version.Pre)+len(version.Build) == 0 && version.GTE(best)).To(BeTrue(),
					"expected a stable update on %s at or above %s, got %s", channel, best, version)
			}, framework.HCPClusterVersionUpgradeTimeout, time.Minute).Should(Succeed())
		},
		Entry("starts applying an unreleased control plane version pinned through the admin api", false),
		Entry("releases a prerelease control plane pin after lowering its threshold to the selected stable release", true),
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
