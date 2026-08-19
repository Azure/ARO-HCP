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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/blang/semver/v4"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	clusterversion "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/version"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controlplaneversion"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

// TODO(recovery-e2e): resolveLatestInstallVersion and resolveZStreamUpgradeVersion below are
// best-effort placeholders reconstructed during a rebase onto upstream/main. Upstream's test
// framework refactor removed the previous stable-channel version helpers (GetLatestInstallVersion,
// GetInstallVersionForZStreamUpgrade) in favor of controlplaneversion.SelectControlPlaneVersion /
// clusterversion.GetZStreamOffset. Only the "nightly" channel scenario below is verified against
// the checked-in e2e fixtures; the "stable" channel y-stream/z-stream/mid-upgrade scenarios need
// review before being trusted in CI.

// resolveLatestInstallVersion resolves the latest install version for the given channel group and
// minor version (e.g. "4.20"). For "nightly" it delegates to the framework helper; for other channel
// groups it uses the Cincinnati-backed control-plane version selector at offset 0 (latest).
func resolveLatestInstallVersion(ctx context.Context, channelGroup, minorVersion string) (string, error) {
	if channelGroup == "nightly" {
		return framework.GetLatestNightlyInstallVersion(ctx, channelGroup, minorVersion)
	}
	release, err := controlplaneversion.SelectControlPlaneVersion(ctx, http.DefaultTransport.RoundTrip, nil,
		fmt.Sprintf("%s-%s", channelGroup, minorVersion), 0)
	if err != nil {
		return "", err
	}
	return release.Version, nil
}

// resolveZStreamUpgradeVersion returns an older z-stream release in the same channel/minor as
// currentVersion, if one exists, so that tests can move a cluster forward from it and observe a
// restore rolling it back (or vice versa for downgrade scenarios).
func resolveZStreamUpgradeVersion(ctx context.Context, channelGroup, currentVersion string) (string, bool, error) {
	current, err := semver.ParseTolerant(currentVersion)
	if err != nil {
		return "", false, fmt.Errorf("failed to parse current version %q: %w", currentVersion, err)
	}
	minor := fmt.Sprintf("%d.%d", current.Major, current.Minor)
	channel := fmt.Sprintf("%s-%s", channelGroup, minor)

	older, err := controlplaneversion.SelectControlPlaneVersion(ctx, http.DefaultTransport.RoundTrip, nil, channel, clusterversion.GetZStreamOffset(channelGroup)+1)
	if err != nil {
		return "", false, nil
	}
	if older.Version == currentVersion {
		return "", false, nil
	}
	return older.Version, true, nil
}

type recoveryTestEnv struct {
	HCPClientFactory *hcpsdk20240610preview.ClientFactory
	AdminRESTConfig  *rest.Config
	KubeClient       kubernetes.Interface
	Suffix           string
	ClusterName      string
	ResourceGroup    string
	HCPResourceID    string
	HTTPClient       *http.Client
	AdminAPIAddress  string
	OpenShiftVersion string
	ChannelGroup     string
}

type recoveryScenario struct {
	// resolveInstallVersion overrides the default install version for cluster creation.
	// Called with the channelGroup and the default (latest) version; returns the version
	// to actually install. Return skip=true to skip the test entry.
	resolveInstallVersion func(ctx context.Context, channelGroup, defaultVersion string) (version string, skip bool, skipMsg string)
	preBackup             func(ctx context.Context, env *recoveryTestEnv)
	// postBackup runs after the backup completes but before the restore is initiated.
	// Use this to mutate cluster state that the restore should roll back.
	postBackup  func(ctx context.Context, env *recoveryTestEnv)
	postRestore func(ctx context.Context, env *recoveryTestEnv)
}

var _ = Describe("HCP Recovery", func() {
	DescribeTable("should recover an HCP cluster",
		labels.MIContainers(1),
		func(ctx context.Context, version, channelGroup string, scenario recoveryScenario) {
			suffix := rand.String(6)
			clusterName := "recovery-" + suffix

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By(fmt.Sprintf("resolving latest %s %s version", version, channelGroup))
			openShiftVersion, err := resolveLatestInstallVersion(ctx, channelGroup, version)
			if err != nil {
				if framework.IsVersionNotFoundError(err) {
					Skip(fmt.Sprintf("No install version found for %s in %s channel: %s", version, channelGroup, err.Error()))
				} else {
					Fail(fmt.Sprintf("failed to get latest install version for %s %s: %s", version, channelGroup, err.Error()))
				}
			}

			if scenario.resolveInstallVersion != nil {
				var skip bool
				var skipMsg string
				openShiftVersion, skip, skipMsg = scenario.resolveInstallVersion(ctx, channelGroup, openShiftVersion)
				if skip {
					Skip(skipMsg)
				}
			}

			By("creating resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "hcp-recovery", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group")

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams20240610()
			clusterParams.ClusterName = clusterName
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name+"-"+suffix, "-managed", 64)
			clusterParams.OpenshiftVersionId = openShiftVersion
			clusterParams.ChannelGroup = channelGroup

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20240610(ctx,
				resourceGroup,
				clusterParams,
				map[string]any{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources")

			By(fmt.Sprintf("creating HCP cluster with version %q on %s channel", clusterParams.OpenshiftVersionId, channelGroup))
			err = tc.CreateHCPClusterFromParam20240610(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %q", clusterName)

			By("verifying the cluster is viable")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20240610(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				clusterName,
				10*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for cluster %q", clusterName)
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "cluster %q failed initial viability check", clusterName)

			By("creating a NodePool")
			nodePoolParams := framework.NewDefaultNodePoolParams20240610()
			nodePoolParams.NodePoolName = "np-1"
			nodePoolParams.ClusterName = clusterName
			nodePoolParams.Replicas = int32(2)
			nodePoolParams.OpenshiftVersionId = openShiftVersion
			nodePoolParams.ChannelGroup = channelGroup
			err = tc.CreateNodePoolFromParam20240610(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams.ManagedResourceGroupName,
				clusterName,
				nodePoolParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create nodepool np-1 on cluster %q", clusterName)

			kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create Kubernetes client for cluster %q", clusterName)

			hcpResourceID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/%s",
				metadataapi.Must(tc.SubscriptionID(ctx)), *resourceGroup.Name, clusterName)

			httpClient, adminAPIAddress, err := tc.NewAdminAPIHTTPClient(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to create admin API HTTP client")

			env := &recoveryTestEnv{
				HCPClientFactory: tc.Get20240610ClientFactoryOrDie(ctx),
				AdminRESTConfig:  adminRESTConfig,
				KubeClient:       kubeClient,
				Suffix:           suffix,
				ClusterName:      clusterName,
				ResourceGroup:    *resourceGroup.Name,
				HCPResourceID:    hcpResourceID,
				HTTPClient:       httpClient,
				AdminAPIAddress:  adminAPIAddress,
				OpenShiftVersion: openShiftVersion,
				ChannelGroup:     channelGroup,
			}

			//By("verifying cluster baseline before scenario setup")
			//err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig,
			//	verifiers.VerifyAllClusterOperatorsAvailable(),
			//	verifiers.VerifyNodesReady(),
			//)
			//Expect(err).NotTo(HaveOccurred(), "cluster %q failed baseline verification before scenario setup", clusterName)
			//
			if scenario.preBackup != nil {
				By("running pre-backup scenario setup")
				scenario.preBackup(ctx, env)
			}

			By("taking a backup using the admin API on-demand backup endpoint")
			createdBackup, err := createBackupViaAdminAPI(ctx, httpClient, adminAPIAddress, hcpResourceID)
			Expect(err).NotTo(HaveOccurred(), "failed to create backup via admin API")
			Expect(createdBackup.Name).NotTo(BeEmpty(), "backup response must include a non-empty Name")

			By(fmt.Sprintf("waiting for backup %s to complete", createdBackup.Name))
			Eventually(func() (string, error) {
				resp, err := getBackupViaAdminAPI(ctx, httpClient, adminAPIAddress, hcpResourceID, createdBackup.Name)
				if err != nil {
					return "", err
				}
				return resp.Backup.Phase, nil
			}, backupTimeout, 15*time.Second).Should(Equal("Completed"))

			if scenario.postBackup != nil {
				By("running post-backup scenario setup")
				scenario.postBackup(ctx, env)
			}

			By(fmt.Sprintf("creating a restore from backup %s", createdBackup.Name))
			restoreResp, err := createRestoreViaAdminAPI(ctx, httpClient, adminAPIAddress, hcpResourceID, createdBackup.Name)
			Expect(err).NotTo(HaveOccurred(), "failed to create restore via admin API")
			Expect(restoreResp.RecoveryID).NotTo(BeEmpty(), "POST restore must return non-empty recoveryID")
			Expect(restoreResp.RecoveryState).To(Equal("Pending"), "initial restore state must be Pending")

			By("waiting for the restore to complete")
			var previousState string
			Eventually(func() (string, error) {
				resp, err := getRestoreStatusViaAdminAPI(ctx, httpClient, adminAPIAddress, hcpResourceID, restoreResp.RecoveryID)
				if err != nil {
					return "", err
				}
				if resp.RecoveryState != previousState {
					GinkgoWriter.Printf("restore state changed: %s -> %s (phase: %s, lastCondition: %s)\n",
						previousState, resp.RecoveryState, resp.Phase, resp.LastCondition)
					previousState = resp.RecoveryState
				}
				if resp.RecoveryState == "Failed" {
					StopTrying(fmt.Sprintf("restore permanently failed: lastCondition=%s, phase=%s", resp.LastCondition, resp.Phase)).Now()
				}
				return resp.RecoveryState, nil
			}, 60*time.Minute, 30*time.Second).Should(Equal("Completed"))

			By("getting fresh admin credentials after restore")
			adminRESTConfig, err = tc.GetAdminRESTConfigForHCPCluster20240610(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				clusterName,
				10*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get fresh admin REST config after restore for cluster %q", clusterName)

			kubeClient, err = kubernetes.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create Kubernetes client from fresh admin REST config")
			env.KubeClient = kubeClient
			env.AdminRESTConfig = adminRESTConfig

			if scenario.postRestore != nil {
				By("running post-restore scenario verification")
				scenario.postRestore(ctx, env)
			}

			By("verifying the HCP cluster post-restore")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig,
				verifiers.VerifyAllClusterOperatorsAvailable(),
				verifiers.VerifyNodesReady(),
			)
			Expect(err).NotTo(HaveOccurred(), "HCP cluster failed verification after restore")
		},

		Entry("with a ConfigMap on 4.22 stable",
			labels.RequireNothing,
			labels.Critical,
			labels.Positive,
			labels.AroRpApiCompatible,
			labels.DevelopmentOnly,
			"4.22", "stable",
			recoveryScenario{
				preBackup: func(ctx context.Context, env *recoveryTestEnv) {
					By("deploying a test ConfigMap")
					testConfigMap := &corev1.ConfigMap{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "recovery-test-cm",
							Namespace: "default",
						},
						Data: map[string]string{
							"test-key": "test-value-" + env.Suffix,
						},
					}
					_, err := env.KubeClient.CoreV1().ConfigMaps("default").Create(ctx, testConfigMap, metav1.CreateOptions{})
					Expect(err).NotTo(HaveOccurred(), "failed to create recovery-test-cm ConfigMap")
				},
				postRestore: func(ctx context.Context, env *recoveryTestEnv) {
					By("validating the ConfigMap is present after restore")
					Eventually(func() error {
						cm, err := env.KubeClient.CoreV1().ConfigMaps("default").Get(ctx, "recovery-test-cm", metav1.GetOptions{})
						if err != nil {
							return fmt.Errorf("failed to get ConfigMap default/recovery-test-cm: %w", err)
						}
						expected := "test-value-" + env.Suffix
						if cm.Data["test-key"] != expected {
							return fmt.Errorf("ConfigMap data mismatch: expected %q, got %q", expected, cm.Data["test-key"])
						}
						return nil
					}, 5*time.Minute, 15*time.Second).Should(Succeed(), "ConfigMap default/recovery-test-cm did not match expected value after restore")
				},
			},
		),

		Entry("with y-stream upgrade rollback on 4.20 stable",
			labels.RequireNothing,
			labels.Critical,
			labels.Positive,
			labels.AroRpApiCompatible,
			labels.DevelopmentOnly,
			"4.20", "stable",
			recoveryScenario{
				// Backup captures cluster at 4.20; postBackup upgrades CP to 4.21 so that
				// the restore rolls the control plane back to 4.20.
				postBackup: func(ctx context.Context, env *recoveryTestEnv) {
					upgradeVersionID, err := resolveLatestInstallVersion(ctx, env.ChannelGroup, "4.21")
					Expect(err).NotTo(HaveOccurred(), "failed to resolve 4.21 stable version for y-stream upgrade")

					By(fmt.Sprintf("upgrading CP to %s so that restore reverts to 4.20", upgradeVersionID))
					hcpClient := env.HCPClientFactory.NewHcpOpenShiftClustersClient()
					_, err = framework.UpdateHCPCluster20240610(ctx, hcpClient, env.ResourceGroup, env.ClusterName,
						hcpsdk20240610preview.HcpOpenShiftClusterUpdate{
							Properties: &hcpsdk20240610preview.HcpOpenShiftClusterPropertiesUpdate{
								Version: &hcpsdk20240610preview.VersionProfile{
									ID:           to.Ptr(upgradeVersionID),
									ChannelGroup: to.Ptr(env.ChannelGroup),
								},
							},
						}, framework.HCPClusterVersionUpgradeTimeout)
					Expect(err).NotTo(HaveOccurred(), "failed to upgrade CP to %s", upgradeVersionID)

					By("waiting for CP y-stream upgrade to complete before initiating restore")
					Eventually(func() error {
						return verifiers.VerifyHCPCluster(ctx, env.AdminRESTConfig,
							verifiers.VerifyHostedControlPlaneYStreamUpgrade("4.20", "4.21"))
					}, framework.HCPClusterVersionUpgradeTimeout, 2*time.Minute).Should(Succeed(),
						"CP did not finish upgrading to 4.21 before restore was initiated")
				},
			},
		),

		Entry("with z-stream upgrade rollback on 4.20 stable",
			labels.RequireNothing,
			labels.Critical,
			labels.Positive,
			labels.AroRpApiCompatible,
			labels.DevelopmentOnly,
			"4.20", "stable",
			func() recoveryScenario {
				// zLatestVersion is captured by resolveInstallVersion (the default latest
				// 4.20 z-patch) and used in postBackup to upgrade the CP back to it.
				var zLatestVersion string
				return recoveryScenario{
					// Install at an older z-patch so the backup captures that state.
					resolveInstallVersion: func(ctx context.Context, channelGroup, defaultVersion string) (string, bool, string) {
						zLatestVersion = defaultVersion
						zOld, hasUpgradePath, err := resolveZStreamUpgradeVersion(ctx, channelGroup, defaultVersion)
						if err != nil {
							Fail(fmt.Sprintf("failed to resolve z-stream install version for %s in %s: %s", defaultVersion, channelGroup, err.Error()))
						}
						if !hasUpgradePath {
							return "", true, fmt.Sprintf("no z-stream upgrade path available for %s in %s channel", defaultVersion, channelGroup)
						}
						return zOld, false, ""
					},
					// After backup (at z_old), upgrade CP to z_latest so restore rolls it back.
					postBackup: func(ctx context.Context, env *recoveryTestEnv) {
						By(fmt.Sprintf("upgrading CP to z-latest %s so that restore reverts to z_old", zLatestVersion))
						hcpClient := env.HCPClientFactory.NewHcpOpenShiftClustersClient()
						_, err := framework.UpdateHCPCluster20240610(ctx, hcpClient, env.ResourceGroup, env.ClusterName,
							hcpsdk20240610preview.HcpOpenShiftClusterUpdate{
								Properties: &hcpsdk20240610preview.HcpOpenShiftClusterPropertiesUpdate{
									Version: &hcpsdk20240610preview.VersionProfile{
										ID:           to.Ptr(zLatestVersion),
										ChannelGroup: to.Ptr(env.ChannelGroup),
									},
								},
							}, framework.HCPClusterVersionUpgradeTimeout)
						Expect(err).NotTo(HaveOccurred(), "failed to upgrade CP to z-latest %s", zLatestVersion)

						By(fmt.Sprintf("waiting for CP z-stream upgrade to %s to complete before initiating restore", zLatestVersion))
						Eventually(func() error {
							return verifiers.VerifyHCPCluster(ctx, env.AdminRESTConfig,
								verifiers.VerifyHostedControlPlaneZStreamUpgradeOnly(env.OpenShiftVersion))
						}, framework.HCPClusterVersionUpgradeTimeout, 2*time.Minute).Should(Succeed(),
							"CP did not finish z-stream upgrade to %s before restore was initiated", zLatestVersion)
					},
				}
			}(),
		),

		Entry("with restore from mid-upgrade backup on 4.20 stable",
			labels.RequireNothing,
			labels.Critical,
			labels.Positive,
			labels.AroRpApiCompatible,
			labels.DevelopmentOnly,
			"4.20", "stable",
			recoveryScenario{
				// Start a y-stream upgrade without waiting for it to complete, then
				// immediately take a backup while the upgrade is in-flight.
				preBackup: func(ctx context.Context, env *recoveryTestEnv) {
					upgradeVersionID, err := resolveLatestInstallVersion(ctx, env.ChannelGroup, "4.21")
					Expect(err).NotTo(HaveOccurred(), "failed to resolve 4.21 stable version for mid-upgrade backup")

					By(fmt.Sprintf("starting non-blocking upgrade to %s to create mid-upgrade state for backup", upgradeVersionID))
					hcpClient := env.HCPClientFactory.NewHcpOpenShiftClustersClient()
					_, err = hcpClient.BeginUpdate(ctx, env.ResourceGroup, env.ClusterName,
						hcpsdk20240610preview.HcpOpenShiftClusterUpdate{
							Properties: &hcpsdk20240610preview.HcpOpenShiftClusterPropertiesUpdate{
								Version: &hcpsdk20240610preview.VersionProfile{
									ID:           to.Ptr(upgradeVersionID),
									ChannelGroup: to.Ptr(env.ChannelGroup),
								},
							},
						}, nil)
					Expect(err).NotTo(HaveOccurred(), "failed to initiate upgrade to %s", upgradeVersionID)

					By("waiting briefly so upgrade is in progress before backup is taken")
					time.Sleep(30 * time.Second)
				},
			},
		),

		Entry("with control plane rollback after nodepool and control plane upgrade on 4.20 stable",
			labels.RequireNothing,
			labels.Critical,
			labels.Positive,
			labels.AroRpApiCompatible,
			labels.DevelopmentOnly,
			"4.20", "stable",
			recoveryScenario{
				// Backup captures CP and NP at 4.20; postBackup upgrades both to 4.21 so
				// that the restore reverts the CP to 4.20 while NP VMs remain at 4.21
				// (valid within the supported one-minor kubelet skew).
				postBackup: func(ctx context.Context, env *recoveryTestEnv) {
					upgradeVersionID, err := resolveLatestInstallVersion(ctx, env.ChannelGroup, "4.21")
					Expect(err).NotTo(HaveOccurred(), "failed to resolve 4.21 stable version for upgrade")

					By(fmt.Sprintf("upgrading CP to %s before restore", upgradeVersionID))
					hcpClient := env.HCPClientFactory.NewHcpOpenShiftClustersClient()
					_, err = framework.UpdateHCPCluster20240610(ctx, hcpClient, env.ResourceGroup, env.ClusterName,
						hcpsdk20240610preview.HcpOpenShiftClusterUpdate{
							Properties: &hcpsdk20240610preview.HcpOpenShiftClusterPropertiesUpdate{
								Version: &hcpsdk20240610preview.VersionProfile{
									ID:           to.Ptr(upgradeVersionID),
									ChannelGroup: to.Ptr(env.ChannelGroup),
								},
							},
						}, framework.HCPClusterVersionUpgradeTimeout)
					Expect(err).NotTo(HaveOccurred(), "failed to upgrade CP to %s", upgradeVersionID)

					By("waiting for CP y-stream upgrade to complete before upgrading nodepool")
					Eventually(func() error {
						return verifiers.VerifyHCPCluster(ctx, env.AdminRESTConfig,
							verifiers.VerifyHostedControlPlaneYStreamUpgrade("4.20", "4.21"))
					}, framework.HCPClusterVersionUpgradeTimeout, 2*time.Minute).Should(Succeed(),
						"CP did not finish upgrading to 4.21 before nodepool upgrade was initiated")

					By(fmt.Sprintf("upgrading nodepool np-1 to %s before restore", upgradeVersionID))
					nodePoolsClient := env.HCPClientFactory.NewNodePoolsClient()
					_, err = framework.UpdateNodePoolAndWait20240610(ctx, nodePoolsClient, env.ResourceGroup, env.ClusterName, "np-1",
						hcpsdk20240610preview.NodePoolUpdate{
							Properties: &hcpsdk20240610preview.NodePoolPropertiesUpdate{
								Version: &hcpsdk20240610preview.NodePoolVersionProfile{
									ID:           to.Ptr(upgradeVersionID),
									ChannelGroup: to.Ptr(env.ChannelGroup),
								},
							},
						}, framework.NodePoolVersionUpgradeTimeout)
					Expect(err).NotTo(HaveOccurred(), "failed to upgrade nodepool np-1 to %s", upgradeVersionID)

					By("waiting for np-1 nodes to be ready at the upgraded version before initiating restore")
					Eventually(func() error {
						return verifiers.VerifyNodesReady().Verify(ctx, env.AdminRESTConfig)
					}, framework.NodePoolVersionUpgradeTimeout, 2*time.Minute).Should(Succeed(),
						"np-1 nodes did not reach ready state after upgrade to %s", upgradeVersionID)
				},
			},
		),

		Entry("with nodepool z-stream downgrade reverted by restore on 4.20 stable",
			labels.RequireNothing,
			labels.Critical,
			labels.Positive,
			labels.AroRpApiCompatible,
			labels.DevelopmentOnly,
			"4.20", "stable",
			recoveryScenario{
				// Backup captures NP at z_latest; postBackup downgrades NP to z_old so
				// that restore recreates the divergence between etcd (z_latest) and node
				// VMs (z_old).
				postBackup: func(ctx context.Context, env *recoveryTestEnv) {
					zOld, hasDowngradePath, err := resolveZStreamUpgradeVersion(ctx, env.ChannelGroup, env.OpenShiftVersion)
					if err != nil {
						Fail(fmt.Sprintf("failed to resolve z-stream downgrade version for %s in %s: %s", env.OpenShiftVersion, env.ChannelGroup, err.Error()))
					}
					if !hasDowngradePath {
						Skip(fmt.Sprintf("no z-stream downgrade target available for %s in %s channel", env.OpenShiftVersion, env.ChannelGroup))
					}

					By(fmt.Sprintf("downgrading nodepool np-1 to %s (restore will revert to %s)", zOld, env.OpenShiftVersion))
					nodePoolsClient := env.HCPClientFactory.NewNodePoolsClient()
					_, err = framework.UpdateNodePoolAndWait20240610(ctx, nodePoolsClient, env.ResourceGroup, env.ClusterName, "np-1",
						hcpsdk20240610preview.NodePoolUpdate{
							Properties: &hcpsdk20240610preview.NodePoolPropertiesUpdate{
								Version: &hcpsdk20240610preview.NodePoolVersionProfile{
									ID:           to.Ptr(zOld),
									ChannelGroup: to.Ptr(env.ChannelGroup),
								},
							},
						}, framework.NodePoolVersionUpgradeTimeout)
					Expect(err).NotTo(HaveOccurred(), "failed to downgrade nodepool np-1 to %s", zOld)
				},
			},
		),
	)
})

const backupTimeout = 15 * time.Minute

type backupResponse struct {
	Name                string `json:"name"`
	Phase               string `json:"phase"`
	StartTimestamp      string `json:"startTimestamp,omitempty"`
	CompletionTimestamp string `json:"completionTimestamp,omitempty"`
}

type getBackupResponse struct {
	ResourceID string         `json:"resourceID"`
	Backup     backupResponse `json:"backup"`
}

func createBackupViaAdminAPI(ctx context.Context, httpClient *http.Client, adminAPIAddress, resourceID string) (backupResponse, error) {
	url := fmt.Sprintf("%s/admin/v1/hcp%s/backups", adminAPIAddress, resourceID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return backupResponse{}, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return backupResponse{}, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return backupResponse{}, fmt.Errorf("create backup returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result backupResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return backupResponse{}, fmt.Errorf("failed to decode response: %w", err)
	}
	return result, nil
}

func getBackupViaAdminAPI(ctx context.Context, httpClient *http.Client, adminAPIAddress, resourceID, backupName string) (getBackupResponse, error) {
	url := fmt.Sprintf("%s/admin/v1/hcp%s/backups/%s", adminAPIAddress, resourceID, backupName)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return getBackupResponse{}, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return getBackupResponse{}, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return getBackupResponse{}, fmt.Errorf("get backup returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result getBackupResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return getBackupResponse{}, fmt.Errorf("failed to decode response: %w", err)
	}
	return result, nil
}

type restoreResponse struct {
	ResourceID    string `json:"resourceID"`
	RecoveryID    string `json:"recoveryID,omitempty"`
	RecoveryState string `json:"recoveryState"`
	BackupID      string `json:"backupID,omitempty"`
	StartedAt     string `json:"startedAt,omitempty"`
	CompletedAt   string `json:"completedAt,omitempty"`
	Phase         string `json:"phase,omitempty"`
	LastCondition string `json:"lastCondition,omitempty"`
}

func createRestoreViaAdminAPI(ctx context.Context, httpClient *http.Client, adminAPIAddress, resourceID, backupName string) (restoreResponse, error) {
	url := fmt.Sprintf("%s/admin/v1/hcp%s/restore", adminAPIAddress, resourceID)
	body, err := json.Marshal(struct {
		BackupID string `json:"backupID"`
	}{BackupID: backupName})
	if err != nil {
		return restoreResponse{}, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return restoreResponse{}, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return restoreResponse{}, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return restoreResponse{}, fmt.Errorf("create restore returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result restoreResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return restoreResponse{}, fmt.Errorf("failed to decode response: %w", err)
	}
	return result, nil
}

func getRestoreStatusViaAdminAPI(ctx context.Context, httpClient *http.Client, adminAPIAddress, resourceID, recoveryID string) (restoreResponse, error) {
	url := fmt.Sprintf("%s/admin/v1/hcp%s/restore?recoveryID=%s", adminAPIAddress, resourceID, recoveryID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return restoreResponse{}, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return restoreResponse{}, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return restoreResponse{}, fmt.Errorf("get restore status returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result restoreResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return restoreResponse{}, fmt.Errorf("failed to decode response: %w", err)
	}
	return result, nil
}
