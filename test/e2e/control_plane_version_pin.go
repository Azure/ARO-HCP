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
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
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
	"github.com/Azure/ARO-HCP/internal/cpoutofband"
	hcpsdk20261001 "github.com/Azure/ARO-HCP/test/sdk/v20261001/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var kubeBuildCommit = regexp.MustCompile("^[0-9a-f]{40}$")

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
			if os.Getenv("OPENSHIFT_CI") != "true" {
				Skip("control-plane version pin scenarios run only in ARO-HCP CI")
			}
			By("selecting a prerelease from the out-of-band catalog for the configured control-plane minor")
			configuredVersion := framework.DefaultOpenshiftControlPlaneVersionId()
			minor, err := semver.ParseTolerant(configuredVersion)
			Expect(err).NotTo(HaveOccurred(), "invalid configured control-plane version %q", configuredVersion)
			targetVersion, image, found := cpoutofband.LatestPrereleaseForMinor(minor.Major, minor.Minor)
			if !found {
				Skip(fmt.Sprintf("no out-of-band prerelease for %d.%d", minor.Major, minor.Minor))
			}
			target := targetVersion.String()

			By("selecting the stable release from Cincinnati for the target minor")
			channelGroup := string(metadataapi.ChannelGroupStable)
			channel := fmt.Sprintf("%s-%d.%d", channelGroup, targetVersion.Major, targetVersion.Minor)
			selected, err := framework.SelectControlPlaneVersion(ctx, http.DefaultTransport.RoundTrip, nil, channel, clusterversion.GetZStreamOffset(channelGroup))
			Expect(err).NotTo(HaveOccurred(), "failed to select a stable release from %s", channel)
			Expect(selected).NotTo(BeNil(), "no stable release found in %s", channel)
			best, err := semver.Parse(selected.Version)
			Expect(err).NotTo(HaveOccurred(), "invalid selected version %q from %s", selected.Version, channel)
			Expect(len(best.Pre)+len(best.Build)).To(BeZero(), "expected a stable X.Y.Z release from %s, got %s", channel, best)
			const releaseController = "https://multi.ocp.releases.ci.openshift.org"
			if autoRelease && best.LT(targetVersion) {
				Skip(fmt.Sprintf("%s selected %s, which is older than prerelease %s", channel, best, target))
			}
			By("reading the expected kube-apiserver commit from the pinned release image")
			expectedCommit, err := privateReleaseKubeCommit(ctx, targetVersion, image)
			Expect(err).NotTo(HaveOccurred(), "failed to read prerelease metadata for %s", target)
			tc := framework.NewTestContext()
			baselineVersion := selected.Version
			var selectedCommit string
			if autoRelease {
				Eventually(ctx, func(ctx context.Context) error {
					var err error
					selectedCommit, err = releaseKubeCommit(ctx, releaseController, best)
					return err
				}, time.Minute, 5*time.Second).Should(Succeed(), "failed to read multi-arch release metadata for %s", best)
				By("selecting an enabled stable baseline in the region with a distinguishable kube-apiserver build")
				versionsClient := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftVersionsClient()
				pager := versionsClient.NewListPager(tc.Location(), nil)
				var candidates []semver.Version
				for pager.More() {
					page, err := pager.NextPage(ctx)
					Expect(err).NotTo(HaveOccurred(), "failed to list available cluster versions in %s", tc.Location())
					for _, item := range page.Value {
						if item == nil || item.Name == nil || item.Properties == nil || item.Properties.Enabled == nil ||
							!*item.Properties.Enabled || item.Properties.ChannelGroup == nil || *item.Properties.ChannelGroup != channelGroup {
							continue
						}
						v, err := semver.Parse(*item.Name)
						if err == nil && v.Major == targetVersion.Major && v.Minor == targetVersion.Minor &&
							len(v.Pre)+len(v.Build) == 0 && v.LT(targetVersion) {
							candidates = append(candidates, v)
						}
					}
				}
				slices.SortFunc(candidates, func(a, b semver.Version) int { return b.Compare(a) })
				baselineVersion = ""
				for _, v := range candidates {
					var sha string
					Eventually(ctx, func(ctx context.Context) error {
						var err error
						sha, err = releaseKubeCommit(ctx, releaseController, v)
						return err
					}, time.Minute, 5*time.Second).Should(Succeed(), "failed to read stable baseline metadata for %s", v)
					if sha != expectedCommit && sha != selectedCommit {
						baselineVersion = v.String()
						break
					}
				}
				if baselineVersion == "" {
					Skip(fmt.Sprintf("no earlier enabled stable baseline with a distinguishable kube-apiserver commit for %s in %s", target, tc.Location()))
				}
			} else {
				Expect(selected.Version).NotTo(Equal(target), "the stable baseline and pin target must differ")
			}

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
			pinnedBuild := Eventually(ctx, runningCommit, framework.HCPClusterVersionUpgradeTimeout, 30*time.Second)
			if autoRelease {
				// Allow the replicas behind the guest endpoint to settle before starting the hold check.
				pinnedBuild = pinnedBuild.MustPassRepeatedly(3)
			}
			pinnedBuild.Should(Equal(expectedCommit),
				"cluster %s did not start serving pin %s (baseline commit %s)", clusterName, target, baselineCommit)
			if !autoRelease {
				Eventually(ctx, guestRelease, 5*time.Minute, 15*time.Second).Should(Equal(baselineVersion),
					"guest ClusterVersion did not report the baseline after the control-plane pin")
				return
			}
			minorVersion := fmt.Sprintf("%d.%d", targetVersion.Major, targetVersion.Minor)
			removeCreationVersionOverride(ctx, hcpClient, *resourceGroup.Name, clusterName, minorVersion, channelGroup)

			By("observing the pinned kube-apiserver build across several one-minute forced-assignment resyncs")
			Consistently(ctx, runningCommit, 5*time.Minute, 15*time.Second).Should(Equal(expectedCommit),
				"guest stopped serving the pinned build while the high threshold was in place on %s", clusterName)

			By(fmt.Sprintf("lowering only untilExactVersion to the Cincinnati-selected stable release %s", best))
			request.UntilExactVersion = best.String()
			postPin(request)

			By("waiting for a guest kube-apiserver to serve a known stable build")
			knownCommits := map[string]string{best.String(): selectedCommit}
			Eventually(ctx, func(ctx context.Context) error {
				observed, err := runningCommit(ctx)
				if err != nil {
					return err
				}
				if observed == baselineCommit || observed == expectedCommit {
					return fmt.Errorf("guest still serves the baseline or prerelease commit %s", observed)
				}
				for _, commit := range knownCommits {
					if commit == observed {
						return nil
					}
				}
				// A regional rollout may select a newer stable build while the test runs.
				// Walk back from the channel tip only when the observed commit is unfamiliar.
				for offset := uint(0); ; offset++ {
					release, err := framework.SelectControlPlaneVersion(ctx, http.DefaultTransport.RoundTrip, nil, channel, offset)
					if err != nil {
						return err
					}
					version, err := semver.Parse(release.Version)
					if err != nil {
						return err
					}
					if version.LTE(best) {
						return fmt.Errorf("guest commit %q did not match a stable build on %s at or above %s", observed, channel, best)
					}
					if len(version.Pre)+len(version.Build) != 0 {
						continue
					}
					if _, known := knownCommits[release.Version]; !known {
						commit, err := releaseKubeCommit(ctx, releaseController, version)
						if err != nil {
							return err
						}
						knownCommits[release.Version] = commit
					}
					if knownCommits[release.Version] == observed {
						return nil
					}
				}
			}, framework.HCPClusterVersionUpgradeTimeout, 30*time.Second).Should(Succeed(),
				"guest on %s did not serve an identifiable stable build on %s at or above %s", clusterName, channel, best)
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

func privateReleaseKubeCommit(ctx context.Context, target semver.Version, image string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "oc", "adm", "release", "info", image, "--filter-by-os=linux/amd64", "-o", "json")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("oc adm release info for %s: %w: %s", image, err, strings.TrimSpace(stderr.String()))
	}
	embeddedVersion, commit, err := decodeKubeBuild(bytes.NewReader(output))
	if err != nil {
		return "", fmt.Errorf("decode release metadata for %s: %w", image, err)
	}
	// The catalog key and embedded payload version may differ on the same minor line.
	embedded, err := semver.Parse(embeddedVersion)
	if err != nil || embedded.Major != target.Major || embedded.Minor != target.Minor {
		return "", fmt.Errorf("payload version %q does not match the minor line of pin %s", embeddedVersion, target)
	}
	return commit, nil
}

// HyperShift runs kube-apiserver from the hyperkube component of the release.
func releaseKubeCommit(ctx context.Context, releaseController string, version semver.Version) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseController+"/releasetag/"+version.String()+"/json", nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("release metadata for %s returned %s", version, resp.Status)
	}
	release, commit, err := decodeKubeBuild(resp.Body)
	if err != nil {
		return "", fmt.Errorf("decode release metadata for %s: %w", version, err)
	}
	if release != version.String() {
		return "", fmt.Errorf("requested release %s, got metadata for %q", version, release)
	}
	return commit, nil
}

func decodeKubeBuild(reader io.Reader) (string, string, error) {
	var info struct {
		Metadata   struct{ Version string }
		References struct {
			Spec struct {
				Tags []struct {
					Name        string
					Annotations map[string]string
				}
			}
		}
	}
	if err := json.NewDecoder(reader).Decode(&info); err != nil {
		return "", "", err
	}
	for _, tag := range info.References.Spec.Tags {
		if tag.Name == "hyperkube" {
			commit := tag.Annotations["io.openshift.build.commit.id"]
			if kubeBuildCommit.MatchString(commit) {
				return info.Metadata.Version, commit, nil
			}
			break
		}
	}
	return "", "", fmt.Errorf("release %q has no full hyperkube source commit", info.Metadata.Version)
}
