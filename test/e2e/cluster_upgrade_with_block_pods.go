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
	"sort"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/blang/semver/v4"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	configv1 "github.com/openshift/api/config/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"

	hcpsdk20261001 "github.com/Azure/ARO-HCP/test/sdk/v20261001/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

const (
	ystreamNodePoolName = "ystream-np"
)

var _ = Describe("Customers", func() {
	// Problem: a customer cluster may carry admission controls that deny pod creation across
	// namespaces (here a deny-all-pods ValidatingAdmissionPolicy). This is a valid, RP-accepted
	// configuration. When a y-stream control-plane upgrade runs on such a cluster, the Hypershift
	// upgrade gate waits for worker-scheduled cluster operators to converge. But they cannot roll
	// new pods while pod CREATE is denied, so the upgrade can stall. The RP then still sees the
	// control plane as upgrading and rejects node pool creation at latest version, leaving the
	// customer unable to add worker capacity at the new version. The failure is in Hypershift's
	// upgrade gate, not in the RP, which accepted the configuration correctly.
	//
	// This test reproduces the workflow: it blocks pod creation, y-stream upgrades the control plane,
	// and asserts the upgrade completes and a node pool can still be created at the new version. It is
	// marked ExpectFailure while the upstream Hypershift fix is in progress.
	//
	// TODO: replace the ARO-123 placeholder in ExpectFailure below with the real Jira ticket.
	//
	// The target minor is the latest line published for the channel group (framework.LatestMinorLine, which
	// reads the update-service graph data so it sees the candidate channel, not just the RP's default); the
	// install minor is the one below it. This avoids a hand-maintained matrix. Exact z-streams are then
	// resolved via the OpenShift update service (resolveNodePoolTestVersion).
	It("can create a nodepool in a latest version after cluster upgraded to that version with blocked pods",
		labels.RequireNothing,
		labels.High,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.Slow,
		labels.ExpectFailure("ARO-123"),
		labels.MIContainers(1),
		func(ctx context.Context) {
			channelGroup := framework.DefaultOpenshiftChannelGroup()

			// Target the latest published minor (discovered from the cincinnati graph data, and install one minor
			// below it. The 5.0 case can't be reached by decrementing the minor, so it maps to 4.22, matching
			// the sibling control-plane upgrade tests.
			cpMinor, err := framework.LatestMinorLine(ctx, channelGroup)
			Expect(err).NotTo(HaveOccurred(), "failed to discover the latest OpenShift minor for channel group %s", channelGroup)
			cpVersion, err := semver.ParseTolerant(cpMinor)
			Expect(err).NotTo(HaveOccurred(), "failed to parse latest control plane minor %q", cpMinor)

			var installVersion semver.Version
			if cpVersion.Major == 5 && cpVersion.Minor == 0 {
				installVersion = semver.Version{Major: 4, Minor: 22}
			} else {
				installVersion = semver.Version{Major: cpVersion.Major, Minor: cpVersion.Minor - 1}
			}
			installMinorLine := fmt.Sprintf("%d.%d", installVersion.Major, installVersion.Minor)

			tc := framework.NewTestContext()

			versionLabel := strings.ReplaceAll(cpMinor, ".", "-")
			customerClusterName := "cp-y-upgrade-block-pod-" + versionLabel

			// Install at <installMinor>.latest (candidate channel, offset=0 is the latest z-stream).
			installVersionId, err := resolveNodePoolTestVersion(ctx, channelGroup, installMinorLine, 0)
			if err != nil {
				Skip(fmt.Sprintf("failed to resolve install version (%s.latest) for %s-%s: %v", installMinorLine, channelGroup, installMinorLine, err))
			}
			if installVersionId == "" {
				Skip(fmt.Sprintf("no install version (%s.latest) resolved for %s-%s", installMinorLine, channelGroup, installMinorLine))
			}

			// Y-stream upgrade target: <cpMinor>.latest-1 (offset=1 is the second-latest z-stream on cpMinor).
			targetVersionId, err := resolveNodePoolTestVersion(ctx, channelGroup, cpMinor, 1)
			if err != nil {
				Skip(fmt.Sprintf("failed to resolve y-stream upgrade version (%s.latest-1) for %s-%s: %v", cpMinor, channelGroup, cpMinor, err))
			}
			if targetVersionId == "" {
				Skip(fmt.Sprintf("no y-stream upgrade version (%s.latest-1) resolved for %s-%s", cpMinor, channelGroup, cpMinor))
			}

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "cp-y-upgrade-block-"+versionLabel, tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group cp-y-upgrade-block-%s", versionLabel)

			By(fmt.Sprintf("creating cluster parameters at install version %s", installVersionId))
			clusterParams := framework.NewDefaultClusterParams20261001()
			clusterParams.ClusterName = customerClusterName
			clusterParams.OpenshiftVersionId = installVersionId
			clusterParams.ChannelGroup = channelGroup
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20261001(ctx,
				resourceGroup,
				clusterParams,
				map[string]any{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create cluster customer resources")

			By(fmt.Sprintf("creating the HCP cluster at version %s", installVersionId))
			err = tc.CreateHCPClusterFromParam20261001(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %s at version %s", customerClusterName, installVersionId)

			// The 20261001 cluster client doubles as the activeVersions source: activeVersions (the
			// control-plane completion signal) is derived from HostedCluster.status.controlPlaneVersion,
			// which Hypershift documents as management-side only and therefore should settle to a single
			// minor once the control-plane rollout completes, independent of guest data-plane/operator
			// health. When that field stays Partial (activeVersions keeps reporting the previous minor
			// alongside the target) the control plane never "levels", which is the ARO-123 wedge this
			// test characterizes.
			hcpClient := tc.Get20261001ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()

			By("getting admin credentials for the cluster")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20261001(
				ctx,
				hcpClient,
				*resourceGroup.Name,
				customerClusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for cluster %s", customerClusterName)

			// diagLast holds the last diagnostics snapshot that was logged so repeated captures
			// (polled every 2m during the up-to-90m upgrade wait) only emit a line when something
			// actually changed, instead of flooding the log with identical state.
			var diagLast string

			// Always capture the control-plane completion signal and guest-side state, no matter
			// how the test ends (success, assertion failure, or timeout), so a failing run carries
			// the evidence that distinguishes this wedge from a merely-slow upgrade. The full
			// per-component ControlPlaneComponent conditions live in the hcpctl must-gather
			// collected during cleanup; this logs what the in-test clients can reach. A fresh
			// dedup key forces one authoritative final snapshot even if it matches the last poll.
			DeferCleanup(func(ctx context.Context) {
				logControlPlaneUpgradeDiagnostics(ctx, hcpClient, adminRESTConfig, *resourceGroup.Name, customerClusterName, new(string))
			})

			By("verifying the cluster is viable before upgrade")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to verify cluster %q is viable before upgrade", customerClusterName)

			kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create Kubernetes client for cluster %q", customerClusterName)

			By(fmt.Sprintf("creating a 2-replica nodepool at version %s", installVersionId))
			nodePoolParams := framework.NewDefaultNodePoolParams20261001()
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = ystreamDenyNodePoolName
			nodePoolParams.Replicas = int32(ystreamDenyExpectedNodes)
			nodePoolParams.OpenshiftVersionId = installVersionId
			nodePoolParams.ChannelGroup = channelGroup
			nodePoolParams.AutoRepair = true
			smallVMSize, err := tc.SelectVMSize(ctx, framework.SmallWorkerVMSizeSelector())
			Expect(err).NotTo(HaveOccurred(), "failed to resolve a small worker VM size; check VM SKU restrictions/quota for the test subscription in %s", tc.Location())
			nodePoolParams.VMSize = smallVMSize
			err = tc.CreateNodePoolFromParam20261001(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				managedResourceGroupName,
				customerClusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create node pool %q for cluster %q", ystreamDenyNodePoolName, customerClusterName)

			By("waiting for guest nodes to be ready")
			Eventually(func() error {
				if err := verifiers.VerifyNodeCount(customerClusterName, ystreamDenyExpectedNodes).Verify(ctx, adminRESTConfig); err != nil {
					return err
				}
				return verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)
			}).WithContext(ctx).WithTimeout(10*time.Minute).WithPolling(framework.StandardPollInterval).Should(Succeed(), "guest nodes were not ready after creating node pool %q on cluster %q", ystreamDenyNodePoolName, customerClusterName)

			By("applying a deny-pod ValidatingAdmissionPolicy")
			Expect(applyPodDenyAdmissionPolicy(ctx, kubeClient)).To(Succeed(), "failed to apply deny-pod ValidatingAdmissionPolicy on cluster %q", customerClusterName)
			Eventually(func() error {
				return verifiers.VerifyProbePodDeniedByAdmissionPolicy(ystreamDenyPolicyName, ystreamDenyPolicyMessage).Verify(ctx, adminRESTConfig)
			}).WithContext(ctx).WithTimeout(2*time.Minute).WithPolling(framework.StandardPollInterval).Should(Succeed(), "probe pod CREATE was not denied by ValidatingAdmissionPolicy on cluster %q", customerClusterName)

			Expect(ctx.Err()).NotTo(HaveOccurred(), "test context expired before triggering upgrade for cluster %q", customerClusterName)
			Expect(err).NotTo(HaveOccurred(), "failed to get pre-upgrade kube-apiserver version for cluster %q", customerClusterName)

			By(fmt.Sprintf("upgrading the control plane from %s to %s", installVersionId, targetVersionId))
			// Encode the upgrade target through the control-plane pin tag. On nightly runs,
			// targetVersionId is a full build string, but Version.ID must be MAJOR.MINOR.
			// ControlPlaneExactVersionPatchTags returns the release line and clears any
			// existing exact-version pin so the old install version does not block the upgrade.
			yVersionLine, yUpgradeTags := framework.ControlPlaneExactVersionPatchTags(targetVersionId)
			yStreamUpdate := hcpsdk20261001.HcpOpenShiftCluster{
				Tags: framework.TagsForPatch(clusterParams.Tags, yUpgradeTags),
				Properties: &hcpsdk20261001.HcpOpenShiftClusterProperties{
					Version: &hcpsdk20261001.VersionProfile{
						ID:           to.Ptr(yVersionLine),
						ChannelGroup: to.Ptr(channelGroup),
					},
				},
			}
			_, err = framework.UpdateHCPCluster20261001(ctx, hcpClient, *resourceGroup.Name, customerClusterName, yStreamUpdate, framework.HCPClusterVersionUpgradeTimeout)
			Expect(err).NotTo(HaveOccurred(), "y-stream upgrade of cluster %s to %s did not complete (ARM LRO failed or hung)", customerClusterName, targetVersionId)

			By(fmt.Sprintf("verifying the control plane advanced to the version %s", targetVersionId))
			Eventually(func(ctx context.Context) error {
				// Capture the control-plane completion signal and guest-side state on each poll
				// (deduped, so only changes are logged) to record the full progression and stall.
				logControlPlaneUpgradeDiagnostics(ctx, hcpClient, adminRESTConfig, *resourceGroup.Name, customerClusterName, &diagLast)

				// VerifyClusterActiveVersionsSettledAtMinor is the one that confirms the upgrade actually
				// *completed*: it reads HostedCluster.status.controlPlaneVersion (management-side only) and
				// requires it to have settled to the single target minor. In the ARO-123 wedge it never does
				// controlPlaneVersion stays Partial because blocked guest pods keep stale operands on
				// the old version, so this is the assertion that fails and characterizes the bug.
				return errors.Join(
					verifiers.VerifyClusterActiveVersionsSettledAtMinor(ctx, hcpClient, *resourceGroup.Name, customerClusterName, cpMinor),
				)
			}).WithContext(ctx).WithTimeout(2*framework.HCPClusterVersionUpgradeTimeout).WithPolling(2*time.Minute).
				Should(Succeed(), "control plane did not y-stream upgrade from minor %s to %s on cluster %s (HostedCluster.status.controlPlaneVersion never settled at the target minor -- the ARO-123 decoupling wedge)", installMinorLine, cpMinor, customerClusterName)

			By("verifying cluster API remains reachable after control plane upgrade")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to verify cluster %q API after y-stream upgrade to %s", customerClusterName, targetVersionId)

			By("asserting probe pod CREATE is still denied by the ValidatingAdmissionPolicy")
			Expect(verifiers.VerifyProbePodDeniedByAdmissionPolicy(ystreamDenyPolicyName, ystreamDenyPolicyMessage).Verify(ctx, adminRESTConfig)).To(Succeed(), "probe pod CREATE was no longer denied after upgrade on cluster %q", customerClusterName)

			By(fmt.Sprintf("creating a nodepool at the upgraded version %s", targetVersionId))
			// Headline assertion: once the control plane has settled at the target, a customer must
			// be able to add worker capacity at that version. While ARO-123 is open this is reached
			// only if the upgrade-completion check above passes; the "node pool version must not
			// exceed control plane version" guard otherwise rejects this create because the recorded
			// control-plane version never advanced -- the customer-visible blast radius of the wedge.
			nodePoolParams = framework.NewDefaultNodePoolParams20261001()
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = ystreamNodePoolName
			nodePoolParams.Replicas = int32(ystreamDenyExpectedNodes)
			nodePoolParams.OpenshiftVersionId = targetVersionId
			nodePoolParams.ChannelGroup = channelGroup
			nodePoolParams.AutoRepair = true
			nodePoolParams.VMSize = smallVMSize
			err = tc.CreateNodePoolFromParam20261001(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				managedResourceGroupName,
				customerClusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			if err != nil {
				// Capture the wedge evidence at the moment the create is rejected, and surface the
				// ARM status/message so a failing run shows it is the control-plane-version guard
				// (HTTP 400) rather than some unrelated node pool error.
				logControlPlaneUpgradeDiagnostics(ctx, hcpClient, adminRESTConfig, *resourceGroup.Name, customerClusterName, new(string))
				var respErr *azcore.ResponseError
				if errors.As(err, &respErr) {
					GinkgoLogr.Info("node pool create at upgraded version was rejected",
						"nodePool", ystreamNodePoolName, "version", targetVersionId, "status", respErr.StatusCode, "error", err.Error())
				}
			}
			Expect(err).NotTo(HaveOccurred(), "node pool %q could not be created at the upgraded version %s on cluster %q: the control-plane upgrade did not settle (activeVersions still reports the previous minor), so the node-pool-version-must-not-exceed-control-plane guard rejects it -- the ARO-123 wedge. A control plane decoupled from the data plane would have completed the upgrade regardless of the blocked guest pods", ystreamNodePoolName, targetVersionId, customerClusterName)

			By("waiting for guest nodes to be ready")
			Eventually(func() error {
				if err := verifiers.VerifyNodeCount(customerClusterName, ystreamDenyExpectedNodes).Verify(ctx, adminRESTConfig); err != nil {
					return err
				}
				return verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)
			}).WithContext(ctx).WithTimeout(10*time.Minute).WithPolling(framework.StandardPollInterval).Should(Succeed(), "guest nodes were not ready after creating node pool %q on cluster %q", ystreamNodePoolName, customerClusterName)

		})
})

// logControlPlaneUpgradeDiagnostics best-effort records the signals that distinguish the ARO-123
// control-plane/data-plane decoupling wedge from a healthy-but-slow upgrade:
//
//   - the ARM-reported control-plane activeVersions, derived from
//     HostedCluster.status.controlPlaneVersion (management-side only per Hypershift's contract, so it
//     should settle to a single minor once the control-plane rollout completes regardless of guest
//     operator health). More than one minor means controlPlaneVersion is still Partial.
//   - the guest ClusterVersion rollout (history states + Progressing message), which shows the guest
//     CVO stalling with operators unavailable.
//   - which ClusterOperators are not Available (and the version they report), i.e. the stale operands
//     that cannot roll because guest pod CREATE is blocked.
//
// It never fails the test, and it only emits a line when the snapshot differs from *last (the caller
// passes a stable pointer for deduped polling, or a fresh one to force a print). The exhaustive
// per-component ControlPlaneComponent conditions are not reachable from the in-test clients; they are
// in the hcpctl must-gather collected at cleanup.
func logControlPlaneUpgradeDiagnostics(
	ctx context.Context,
	hcpClusterClient *hcpsdk20261001.HcpOpenShiftClustersClient,
	adminRESTConfig *rest.Config,
	resourceGroupName, clusterName string,
	last *string,
) {
	activeVersions := "<error>"
	if resp, err := hcpClusterClient.Get(ctx, resourceGroupName, clusterName, nil); err != nil {
		activeVersions = fmt.Sprintf("<get failed: %v>", err)
	} else if resp.Properties != nil && resp.Properties.Status != nil {
		versions := make([]string, 0, len(resp.Properties.Status.ActiveVersions))
		for _, av := range resp.Properties.Status.ActiveVersions {
			if av != nil && av.Version != nil {
				versions = append(versions, *av.Version)
			}
		}
		activeVersions = fmt.Sprintf("%v", versions)
	} else {
		activeVersions = "<none>"
	}

	clusterVersion := "<error>"
	unavailableOperators := "<error>"
	if configClient, err := configv1client.NewForConfig(adminRESTConfig); err != nil {
		clusterVersion = fmt.Sprintf("<config client: %v>", err)
		unavailableOperators = clusterVersion
	} else {
		if cv, err := configClient.ClusterVersions().Get(ctx, "version", metav1.GetOptions{}); err != nil {
			clusterVersion = fmt.Sprintf("<get failed: %v>", err)
		} else {
			history := make([]string, 0, len(cv.Status.History))
			for _, h := range cv.Status.History {
				history = append(history, fmt.Sprintf("%s=%s", h.Version, h.State))
			}
			progressing := ""
			for _, c := range cv.Status.Conditions {
				if c.Type == configv1.OperatorProgressing {
					progressing = fmt.Sprintf("; Progressing=%s: %s", c.Status, c.Message)
					break
				}
			}
			clusterVersion = strings.Join(history, ", ") + progressing
		}

		if cos, err := configClient.ClusterOperators().List(ctx, metav1.ListOptions{}); err != nil {
			unavailableOperators = fmt.Sprintf("<list failed: %v>", err)
		} else {
			unavailable := []string{}
			for _, co := range cos.Items {
				available := false
				for _, cond := range co.Status.Conditions {
					if cond.Type == configv1.OperatorAvailable {
						available = cond.Status == configv1.ConditionTrue
						break
					}
				}
				if available {
					continue
				}
				operatorVersion := ""
				for _, v := range co.Status.Versions {
					if v.Name == "operator" {
						operatorVersion = "@" + v.Version
						break
					}
				}
				unavailable = append(unavailable, co.Name+operatorVersion)
			}
			sort.Strings(unavailable)
			unavailableOperators = fmt.Sprintf("%v", unavailable)
		}
	}

	summary := fmt.Sprintf("activeVersions=%s | clusterVersion=%s | unavailableOperators=%s",
		activeVersions, clusterVersion, unavailableOperators)
	if last != nil && summary == *last {
		return
	}
	if last != nil {
		*last = summary
	}
	GinkgoLogr.Info("control-plane upgrade diagnostics", "cluster", clusterName, "summary", summary)
}
