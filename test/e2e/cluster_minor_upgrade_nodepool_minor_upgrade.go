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
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/blang/semver/v4"

	"k8s.io/client-go/kubernetes"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	clusterversion "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/version"
	hcpsdk20251223preview "github.com/Azure/ARO-HCP/test/sdk/v20251223preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

// This test reproduces ARO-30325: the RP blocks a node pool upgrade when the control
// plane upgrade has not fully completed, which can never happen while all worker capacity
// carries an untolerated NoSchedule taint (add-on operands cannot be scheduled, so their
// ClusterOperators report Available=False indefinitely).
//
// Flow:
//  1. Create a cluster at the framework default install version (from ARO_HCP_OPENSHIFT_CONTROLPLANE_VERSION
//     or ARO_HCP_OPENSHIFT_VERSION_ID env vars) with a single node pool whose every worker carries
//     a NoSchedule taint the standard add-on operands do not tolerate -> cluster-wide
//     untainted worker capacity is 0, so worker-scheduled ClusterOperators go
//     Available=False while the nodes themselves are Ready.
//  2. Upgrade the control plane and assert the operation succeeds and the control plane
//     actually reaches the target minor. The upgrade must not stall waiting for operators
//     that cannot schedule their operand pods.
//  3. Upgrade the tainted node pool and assert the RP accepts and completes it.
var _ = Describe("Customer", func() {
	It("should upgrade a cluster and a node pool upgrade when all worker capacity is zero (pods cannot be created)",
		labels.RequireNothing,
		labels.High,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.Slow,
		labels.ToBeFixed,
		labels.MIContainers(1),
		func(ctx context.Context) {
			const (
				customerClusterName = "np-taint-upgrade-hcp"
				taintedNodePoolName = "np-tainted"
				taintKey            = "dedicated"
				taintValue          = "workload"
			)

			channelGroup := framework.DefaultOpenshiftChannelGroup()
			offset := clusterversion.GetZStreamOffset(channelGroup)

			// Install version and node pool version come from framework defaults (reads
			// ARO_HCP_OPENSHIFT_CONTROLPLANE_VERSION / ARO_HCP_OPENSHIFT_VERSION_ID, and
			// applies CPO_IMAGE_OVERRIDE when set via NewDefaultClusterParams20251223).
			clusterParams := framework.NewDefaultClusterParams20251223()
			taintedParams := framework.NewDefaultNodePoolParams20251223()

			installVersionId := clusterParams.OpenshiftVersionId
			nodePoolInstallVersion := taintedParams.OpenshiftVersionId

			// Extract bare major.minor from the resolved install version for the YStream verifier.
			// For non-nightly channels installVersionId is already a bare major.minor (e.g. "4.22");
			// for nightly it is an exact build tag (e.g. "4.22.0-0.nightly-...").
			installMinor := installVersionId
			if parts := strings.SplitN(installVersionId, ".", 3); len(parts) >= 2 {
				installMinor = parts[0] + "." + parts[1]
			}

			// Compute the upgrade target minor: 4.22→5.0, 4.23→5.1, other major.minor→major.(minor+1).
			targetMinor, err := nextTestMinorVersion(installMinor)
			if err != nil {
				Fail(fmt.Sprintf("failed to compute upgrade target minor from install version %s: %v", installMinor, err))
			}

			// Resolve the upgrade target up front so a missing version skips before we burn
			// resources on cluster creation. For non-nightly channels the bare major.minor is a
			// valid version.id (verified resolvable here); nightly must be pinned to an exact build.
			upgradeVersionId := targetMinor
			if channelGroup == "nightly" {
				resolvedUpgrade, err := framework.GetLatestNightlyInstallVersion(ctx, channelGroup, targetMinor)
				if framework.IsVersionNotFoundError(err) || framework.IsIncompatibleNightlyVersionError(err) {
					Skip(fmt.Sprintf("no usable nightly version for %s: %v", targetMinor, err))
				}
				Expect(err).NotTo(HaveOccurred(), "failed to resolve nightly upgrade version for %s", targetMinor)
				upgradeVersionId = resolvedUpgrade
			} else {
				desiredVersion, err := framework.SelectControlPlaneVersion(ctx, http.DefaultTransport.RoundTrip, nil, fmt.Sprintf("%s-%s", channelGroup, targetMinor), offset)
				if err != nil {
					Skip(fmt.Sprintf("failed to resolve a version for channel %s-%s: %v", channelGroup, targetMinor, err))
				}
				if desiredVersion == nil {
					Skip(fmt.Sprintf("no version resolved for channel %s-%s; skipping upgrade %s -> %s",
						channelGroup, targetMinor, installMinor, targetMinor))
				}
			}

			nodePoolUpgradeVersion, err := resolveNodePoolTestVersion(ctx, channelGroup, targetMinor, offset)
			if err != nil {
				Skip(fmt.Sprintf("failed to resolve node pool upgrade version for %s-%s: %v", channelGroup, targetMinor, err))
			}
			if nodePoolUpgradeVersion == "" {
				Skip(fmt.Sprintf("no node pool upgrade version resolved for %s-%s", channelGroup, targetMinor))
			}

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "nodepool-taint-upgrade", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group nodepool-taint-upgrade")

			By(fmt.Sprintf("creating cluster parameters at install version %s", installVersionId))
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20251223(ctx,
				resourceGroup,
				clusterParams,
				map[string]any{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create cluster customer resources")

			By(fmt.Sprintf("creating the HCP cluster at version %s", installVersionId))
			err = tc.CreateHCPClusterFromParam20251223(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %s at version %s", customerClusterName, installVersionId)

			By("getting admin credentials for the cluster")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20240610(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for cluster %s", customerClusterName)

			// using a smaller VM size for faster provisioning
			smallVMSize, err := tc.SelectVMSize(ctx, framework.SmallWorkerVMSizeSelector())
			Expect(err).NotTo(HaveOccurred(), "failed to resolve a small worker VM size; check VM SKU restrictions/quota for the test subscription in %s", tc.Location())

			nodePoolsClient := tc.Get20251223ClientFactoryOrDie(ctx).NewNodePoolsClient()

			By(fmt.Sprintf("creating a node pool at version %s whose every worker is tainted NoSchedule", nodePoolInstallVersion))
			taintedParams.ClusterName = customerClusterName
			taintedParams.NodePoolName = taintedNodePoolName
			taintedParams.Replicas = int32(2)
			taintedParams.VMSize = smallVMSize
			taintedParams.ChannelGroup = channelGroup
			taintedParams.Taints = []*hcpsdk20251223preview.Taint{
				{
					Key:    to.Ptr(taintKey),
					Value:  to.Ptr(taintValue),
					Effect: to.Ptr(hcpsdk20251223preview.EffectNoSchedule),
				},
			}
			taintedNodePool := framework.BuildNodePoolFromParams20251223(taintedParams, tc.Location())
			_, err = framework.CreateNodePoolAndWait20251223(ctx,
				nodePoolsClient,
				*resourceGroup.Name,
				customerClusterName,
				taintedNodePoolName,
				taintedNodePool,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create tainted node pool %s", taintedNodePoolName)

			By("verifying the tainted nodes are present and Ready")
			Expect(verifiers.VerifyNodeCount(customerClusterName, int(taintedParams.Replicas)).Verify(ctx, adminRESTConfig)).To(Succeed(), "failed to verify node count of %d after creating tainted node pool", int(taintedParams.Replicas))
			Expect(verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)).To(Succeed(), "tainted nodes never became Ready")

			By("capturing the pre-upgrade kube-apiserver version")
			kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create Kubernetes client for cluster %s", customerClusterName)
			preUpgradeKubeAPIServerVersion, err := kubeClient.Discovery().ServerVersion()
			Expect(err).NotTo(HaveOccurred(), "failed to get pre-upgrade kube-apiserver version for cluster %s", customerClusterName)

			By(fmt.Sprintf("upgrading the control plane from %s to %s while worker capacity is fully tainted", installVersionId, upgradeVersionId))
			hcpClient := tc.Get20251223ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
			// Encode the upgrade target through the control-plane pin tag. On nightly runs,
			// upgradeVersionId is a full build string, but control-plane validation requires
			// version.id to be MAJOR.MINOR. Also, when the install version is exact, leaving
			// Tags nil makes UpdateHCPCluster20251223 preserve the old control-plane-exact-version
			// pin, which conflicts with the new release line. Use ControlPlaneExactVersionPatchTags
			// to replace or clear the pin, merge its changes into the current tags, and send the
			// returned release line as the ID.
			upgradeVersionLine, upgradeTags := framework.ControlPlaneExactVersionPatchTags(upgradeVersionId)
			clusterUpdate := hcpsdk20251223preview.HcpOpenShiftClusterUpdate{
				Tags: framework.TagsForPatch(clusterParams.Tags, upgradeTags),
				Properties: &hcpsdk20251223preview.HcpOpenShiftClusterPropertiesUpdate{
					Version: &hcpsdk20251223preview.VersionProfileUpdate{
						ChannelGroup: to.Ptr(channelGroup),
						ID:           to.Ptr(upgradeVersionLine),
					},
				},
			}
			_, err = framework.UpdateHCPCluster20251223(ctx, hcpClient, *resourceGroup.Name, customerClusterName, clusterUpdate, framework.HCPClusterVersionUpgradeTimeout)
			Expect(err).NotTo(HaveOccurred(), "control plane upgrade of degraded cluster %s to %s did not complete (update LRO failed or hung)", customerClusterName, upgradeVersionId)

			By("verifying the control plane actually reached the target minor despite being degraded")
			// VerifyHCPCluster is intentionally not used here: it asserts all cluster operators
			// are available, which they are not while worker capacity remains fully tainted. We
			// only assert the version moved -- the point of the test is that the degraded state
			// does not stall the upgrade.
			Eventually(func(ctx context.Context) error {
				return errors.Join(
					verifiers.VerifyHostedControlPlaneYStreamUpgrade(installMinor, targetMinor).Verify(ctx, adminRESTConfig),
					verifiers.VerifyKubeAPIServerServerVersionUpgraded(preUpgradeKubeAPIServerVersion).Verify(ctx, adminRESTConfig),
				)
			}).WithContext(ctx).WithTimeout(framework.HCPClusterVersionUpgradeTimeout).WithPolling(2*time.Minute).Should(Succeed(), "control plane did not reach %s on degraded cluster %s", targetMinor, customerClusterName)

			By("capturing node release images before upgrade")
			previousReleaseImages, err := framework.NodePoolReleaseImages(ctx, adminRESTConfig, taintedNodePoolName)
			Expect(err).NotTo(HaveOccurred(), "failed to capture node release images before upgrade")
			Expect(previousReleaseImages).NotTo(BeEmpty(), "expected node pool nodes to report at least one release image ref before upgrade")

			By(fmt.Sprintf("triggering nodepool upgrade to version %s", nodePoolUpgradeVersion))
			nodePoolUpdate := hcpsdk20251223preview.NodePoolUpdate{
				Properties: &hcpsdk20251223preview.NodePoolPropertiesUpdate{
					Version: &hcpsdk20251223preview.NodePoolVersionProfileUpdate{
						ID:           to.Ptr(nodePoolUpgradeVersion),
						ChannelGroup: to.Ptr(channelGroup),
					},
				},
			}
			_, err = framework.UpdateNodePoolAndWait20251223(ctx, nodePoolsClient, *resourceGroup.Name, customerClusterName, taintedNodePoolName, nodePoolUpdate, framework.NodePoolVersionUpgradeTimeout)
			Expect(err).ToNot(HaveOccurred(), "failed to upgrade node pool %s to version %s", taintedNodePoolName, nodePoolUpgradeVersion)

			By("verifying nodes are ready, updated to expected version, and release images differ from pre-upgrade")
			Eventually(func() error {
				return verifiers.VerifyNodePoolUpgrade(nodePoolUpgradeVersion, taintedNodePoolName, previousReleaseImages).Verify(ctx, adminRESTConfig)
			}, framework.NodePoolVersionUpgradeTimeout, 2*time.Minute).Should(Succeed(), "node pool %s did not finish upgrading to %s", taintedNodePoolName, nodePoolUpgradeVersion)
		})
})

// nextTestMinorVersion returns the next minor version after installMinor for the purpose
// of Y-stream upgrade tests. The OCP 5.x series was renumbered from 4.x:
//   - 4.22 → 5.0
//   - 4.23 → 5.1
//   - any other major.minor → major.(minor+1)
func nextTestMinorVersion(installMinor string) (string, error) {
	v, err := semver.ParseTolerant(installMinor)
	if err != nil {
		return "", fmt.Errorf("parse install minor %q: %w", installMinor, err)
	}
	switch {
	case v.Major == 4 && v.Minor == 22:
		return "5.0", nil
	case v.Major == 4 && v.Minor == 23:
		return "5.1", nil
	default:
		return fmt.Sprintf("%d.%d", v.Major, v.Minor+1), nil
	}
}
