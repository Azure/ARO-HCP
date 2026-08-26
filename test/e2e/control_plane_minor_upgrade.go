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
	"github.com/google/uuid"

	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/kubernetes"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	clusterversion "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/version"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controlplaneversion"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/cincinnati"
	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Customer", func() {
	DescribeTable("should be able to successfully upgrade control plane minor version",
		labels.MIContainers(1),
		func(ctx context.Context, targetMinor string) {
			// The 4.22 -> 5.0 minor upgrade is not yet supported by Cluster Service, so skip it
			// until CS gains support. This entry is the only one whose target minor is "5.0".
			if targetMinor == "5.0" {
				Skip(`cluster service doesn't support this yet: VerifyHostedControlPlaneYStreamUpgrade(previousMinor=4.22, targetMinor=5.0) failed: clusterversion status.history has no version in target minor "5.0"`)
			}

			channelGroup := framework.DefaultOpenshiftChannelGroup()
			upgradeVersion := metadataapi.Must(semver.ParseTolerant(targetMinor))

			var installVersion semver.Version
			if targetMinor == "5.0" {
				installVersion = semver.Version{Major: 4, Minor: 22}
			} else {
				installVersion = semver.Version{Major: upgradeVersion.Major, Minor: upgradeVersion.Minor - 1}
			}

			// Resolve the install (previous minor) and upgrade (target minor) version strings up front
			// so a missing version skips before we burn resources on cluster creation. For nightly,
			// each resolves to its exact build tag (the RP cannot resolve major.minor to a nightly
			// build). For other channel groups, each is the bare major.minor string, verified
			// resolvable via the OpenShift update service at the channel's z-stream offset.
			installVersionId := fmt.Sprintf("%d.%d", installVersion.Major, installVersion.Minor)
			upgradeVersionId := fmt.Sprintf("%d.%d", upgradeVersion.Major, upgradeVersion.Minor)
			var resolvedInstallVersion string
			if channelGroup == "nightly" {
				resolvedInstall, err := framework.GetLatestNightlyInstallVersion(ctx, channelGroup, installVersionId)
				if framework.IsVersionNotFoundError(err) {
					Skip(fmt.Sprintf("no nightly version for %s: %v", installVersionId, err))
				}
				Expect(err).NotTo(HaveOccurred(), "failed to resolve nightly install version for %s", installVersionId)
				installVersionId = resolvedInstall
				resolvedInstallVersion = installVersionId

				resolvedUpgrade, err := framework.GetLatestNightlyInstallVersion(ctx, channelGroup, upgradeVersionId)
				if framework.IsVersionNotFoundError(err) {
					Skip(fmt.Sprintf("no nightly version for %s: %v", upgradeVersionId, err))
				}
				Expect(err).NotTo(HaveOccurred(), "failed to resolve nightly upgrade version for %s", upgradeVersionId)
				upgradeVersionId = resolvedUpgrade
			} else {
				for _, minorLine := range []string{installVersionId, upgradeVersionId} {
					desiredVersion, err := controlplaneversion.SelectControlPlaneVersion(ctx, http.DefaultTransport.RoundTrip, nil, fmt.Sprintf("%s-%s", channelGroup, minorLine), clusterversion.GetZStreamOffset(channelGroup))
					if err != nil {
						Skip(fmt.Sprintf("failed to resolve a version for channel %s-%s: %v", channelGroup, minorLine, err))
					}
					if desiredVersion == nil {
						Skip(fmt.Sprintf("no version resolved for channel %s-%s; skipping y-stream upgrade %s -> %s",
							channelGroup, minorLine, installVersionId, upgradeVersionId))
					}
					if minorLine == installVersionId {
						resolvedInstallVersion = desiredVersion.Version
					}
				}
			}

			tc := framework.NewTestContext()
			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			versionLabel := strings.ReplaceAll(targetMinor, ".", "-")
			suffix := rand.String(6)
			clusterName := "cp-ystream-upgrade-" + versionLabel + "-" + suffix

			By("creating resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "rg-cp-ystream-upgrade-"+versionLabel, tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for y-stream upgrade to %s", targetMinor)

			By("creating cluster parameters at install (previous minor) version")
			clusterParams := framework.NewDefaultClusterParams20240610()
			clusterParams.ClusterName = clusterName
			clusterParams.OpenshiftVersionId = installVersionId
			clusterParams.ChannelGroup = channelGroup
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name+"-cp-ystream-"+suffix, "-managed", 64)

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20240610(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{
					"customerNsgName":        "customer-nsg-cp-ystream-" + suffix,
					"customerVnetName":       "customer-vnet-cp-ystream-" + suffix,
					"customerVnetSubnetName": "customer-vnet-subnet-cp-ystream-" + suffix,
				},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for y-stream upgrade cluster %q", clusterName)

			By(fmt.Sprintf("creating the HCP cluster at install version %s (previous minor)", installVersionId))
			err = tc.CreateHCPClusterFromParam20240610(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %q at install version %s", clusterName, installVersionId)

			By("getting admin credentials")
			hcpClient := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20240610(
				ctx,
				hcpClient,
				*resourceGroup.Name,
				clusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for cluster %q", clusterName)

			By("verifying the cluster is viable before upgrade")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to verify cluster %q is viable before upgrade", clusterName)

			Expect(ctx.Err()).NotTo(HaveOccurred(), "test context expired before triggering upgrade for cluster %q", clusterName)
			kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create Kubernetes client for cluster %q", clusterName)
			preUpgradeKubeAPIServerVersion, err := kubeClient.Discovery().ServerVersion()
			Expect(err).NotTo(HaveOccurred(), "failed to get pre-upgrade kube-apiserver version for cluster %q", clusterName)

			By(fmt.Sprintf("triggering control plane y-stream upgrade to %s (target minor %s)", upgradeVersionId,
				upgradeVersion.String()))
			update := hcpsdk20240610preview.HcpOpenShiftClusterUpdate{
				Properties: &hcpsdk20240610preview.HcpOpenShiftClusterPropertiesUpdate{
					Version: &hcpsdk20240610preview.VersionProfile{
						ID:           to.Ptr(upgradeVersionId),
						ChannelGroup: to.Ptr(channelGroup),
					},
				},
			}
			_, err = framework.UpdateHCPCluster20240610(ctx, hcpClient, *resourceGroup.Name, clusterName, update, framework.HCPClusterVersionUpgradeTimeout)
			// Reactive: when the upgrade is rejected with "no upgrade path to update
			// channel …", confirm via Cincinnati that the resolved install z-stream
			// truly has no outgoing edges to the target minor. If so, the failure is
			// a transient upstream graph-data gap — skip the test instead of failing.
			// The timebomb (2026-09-30) ensures this grace window doesn't mask a real
			// regression indefinitely.
			if err != nil && channelGroup != "nightly" && strings.Contains(err.Error(), "no upgrade path to update channel") &&
				time.Now().Before(time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)) {
				installSemver, parseErr := semver.ParseTolerant(resolvedInstallVersion)
				if parseErr == nil {
					upgradeChannel := fmt.Sprintf("%s-%s", channelGroup, upgradeVersionId)
					cincinnatiURI, uriErr := cincinnati.GetCincinnatiURI(channelGroup)
					if uriErr == nil {
						cincinnatiClient := cincinnati.NewClientCache().GetOrCreateClient(uuid.Nil)
						_, updates, _, edgeErr := cincinnatiClient.GetUpdates(ctx, cincinnatiURI, "multi", "multi", upgradeChannel, installSemver)
						noEdges := cincinnati.IsCincinnatiVersionNotFoundError(edgeErr)
						if edgeErr == nil {
							noEdges = true
							for _, u := range updates {
								v, vErr := semver.ParseTolerant(u.Version)
								if vErr != nil {
									continue
								}
								if v.Major == upgradeVersion.Major && v.Minor == upgradeVersion.Minor {
									noEdges = false
									break
								}
							}
						}
						if noEdges {
							Skip(fmt.Sprintf("reactive: upgrade of cluster %q failed with 'no upgrade path' and Cincinnati confirms no outgoing edges from %s to %s.z in channel %s — upstream graph data likely not yet published",
								clusterName, resolvedInstallVersion, upgradeVersionId, upgradeChannel))
						}
					}
				}
			}
			Expect(err).NotTo(HaveOccurred(), "failed to trigger y-stream upgrade of cluster %q to %s", clusterName, upgradeVersionId)

			By("verifying control plane reached desired version and cluster remains viable")
			Eventually(func() error {
				return verifiers.VerifyHCPCluster(ctx, adminRESTConfig,
					verifiers.VerifyKubeAPIServerServerVersionUpgraded(preUpgradeKubeAPIServerVersion),
					verifiers.VerifyHostedControlPlaneYStreamUpgrade(
						installVersionId,
						upgradeVersionId))
			}, framework.HCPClusterVersionUpgradeTimeout, 2*time.Minute).Should(Succeed())
		},
		Entry("from 4.20 minor to 4.21 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "4.21"),
		Entry("from 4.21 minor to 4.22 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "4.22"),
		Entry("from 4.22 minor to 4.23 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "4.23"),
		Entry("from 4.22 minor to 5.0 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.0"),
		Entry("from 5.0 minor to 5.1 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.1"),
		Entry("from 5.1 minor to 5.2 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.2"),
		Entry("from 5.2 minor to 5.3 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.3"),
		Entry("from 5.3 minor to 5.4 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.4"),
		Entry("from 5.4 minor to 5.5 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.5"),
		Entry("from 5.5 minor to 5.6 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.6"),
		Entry("from 5.6 minor to 5.7 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.7"),
		Entry("from 5.7 minor to 5.8 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.8"),
		Entry("from 5.8 minor to 5.9 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.9"),
		Entry("from 5.9 minor to 5.10 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.10"),
		Entry("from 5.10 minor to 5.11 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.11"),
		Entry("from 5.11 minor to 5.12 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, "5.12"),
	)
})
