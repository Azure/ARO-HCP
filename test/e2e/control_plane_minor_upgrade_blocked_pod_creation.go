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

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/kubernetes"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	clusterversion "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/version"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	hcpsdk20260901preview "github.com/Azure/ARO-HCP/test/sdk/v20260901preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

const (
	ystreamTaintedExpectedNodes = 2
	ystreamTaintedNodePoolName  = "np-tainted"
	ystreamTaintKey             = "dedicated"
	ystreamTaintValue           = "workload"
)

var _ = Describe("Customer", func() {
	DescribeTable("should upgrade control plane minor version when all workers are tainted NoSchedule",
		labels.MIContainers(1),
		func(ctx context.Context, targetMinor string) {
			const (
				customerNetworkSecurityGroupName = "customer-nsg-ystream-tainted-"
				customerVnetName                 = "customer-vnet-ystream-tainted-"
				customerVnetSubnetName           = "customer-vnet-subnet-ystream-tainted-"
				customerClusterNamePrefix        = "cp-ystream-tainted-"
			)

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
			// Node pools require a concrete Major.Minor.Patch (the RP does not resolve a
			// bare major.minor for version.id the way it does for the control plane).
			nodePoolVersionId, err := resolveNodePoolTestVersion(ctx, channelGroup, installVersionId, clusterversion.GetZStreamOffset(channelGroup))
			if err != nil {
				Skip(fmt.Sprintf("failed to resolve node pool version for %s-%s: %v", channelGroup, installVersionId, err))
			}
			if nodePoolVersionId == "" {
				Skip(fmt.Sprintf("no node pool version resolved for %s-%s", channelGroup, installVersionId))
			}
			if channelGroup == "nightly" {
				resolvedInstall, err := framework.GetLatestNightlyInstallVersion(ctx, channelGroup, installVersionId)
				if framework.IsVersionNotFoundError(err) || framework.IsIncompatibleNightlyVersionError(err) {
					Skip(fmt.Sprintf("no usable nightly version for %s: %v", installVersionId, err))
				}
				Expect(err).NotTo(HaveOccurred(), "failed to resolve nightly install version for %s", installVersionId)
				installVersionId = resolvedInstall

				resolvedUpgrade, err := framework.GetLatestNightlyInstallVersion(ctx, channelGroup, upgradeVersionId)
				if framework.IsVersionNotFoundError(err) || framework.IsIncompatibleNightlyVersionError(err) {
					Skip(fmt.Sprintf("no usable nightly version for %s: %v", upgradeVersionId, err))
				}
				Expect(err).NotTo(HaveOccurred(), "failed to resolve nightly upgrade version for %s", upgradeVersionId)
				upgradeVersionId = resolvedUpgrade
			} else {
				for _, minorLine := range []string{installVersionId, upgradeVersionId} {
					desiredVersion, err := framework.SelectControlPlaneVersion(ctx, http.DefaultTransport.RoundTrip, nil, fmt.Sprintf("%s-%s", channelGroup, minorLine), clusterversion.GetZStreamOffset(channelGroup))
					if err != nil {
						Skip(fmt.Sprintf("failed to resolve a version for channel %s-%s: %v", channelGroup, minorLine, err))
					}
					if desiredVersion == nil {
						Skip(fmt.Sprintf("no version resolved for channel %s-%s; skipping y-stream upgrade %s -> %s",
							channelGroup, minorLine, installVersionId, upgradeVersionId))
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
			clusterName := customerClusterNamePrefix + versionLabel + "-" + suffix

			By("creating resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "rg-cp-ystream-tainted-"+versionLabel, tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for y-stream tainted-worker upgrade to %s", targetMinor)

			By("creating cluster parameters at install (previous minor) version")
			clusterParams := framework.NewDefaultClusterParams20260901()
			clusterParams.ClusterName = clusterName
			clusterParams.OpenshiftVersionId = installVersionId
			clusterParams.ChannelGroup = channelGroup
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name+"-cp-ystream-tainted-"+suffix, "-managed", 64)
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
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for y-stream tainted-worker upgrade cluster %q", clusterName)

			By(fmt.Sprintf("creating the HCP cluster at install version %s (previous minor)", installVersionId))
			err = tc.CreateHCPClusterFromParam20260901(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %q at install version %s", clusterName, installVersionId)

			By("getting admin credentials")
			hcpClient := tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20260901(
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

			kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create Kubernetes client for cluster %q", clusterName)

			By(fmt.Sprintf("creating a %d-replica node pool at version %s whose every worker is tainted NoSchedule", ystreamTaintedExpectedNodes, nodePoolVersionId))
			nodePoolParams := framework.NewDefaultNodePoolParams20260901()
			nodePoolParams.ClusterName = clusterName
			nodePoolParams.NodePoolName = ystreamTaintedNodePoolName
			nodePoolParams.Replicas = int32(ystreamTaintedExpectedNodes)
			nodePoolParams.OpenshiftVersionId = nodePoolVersionId
			nodePoolParams.ChannelGroup = channelGroup
			nodePoolParams.AutoRepair = true
			smallVMSize, err := tc.SelectVMSize(ctx, framework.SmallWorkerVMSizeSelector())
			Expect(err).NotTo(HaveOccurred(), "failed to resolve a small worker VM size; check VM SKU restrictions/quota for the test subscription in %s", tc.Location())
			nodePoolParams.VMSize = smallVMSize
			// Taint in the create body so kubelet registers the nodes with NoSchedule.
			// A taint applied to nodes that are already running does not keep add-on operands off them.
			nodePool := framework.BuildNodePoolFromParams20260901(nodePoolParams, tc.Location())
			nodePool.Properties.Taints = []*hcpsdk20260901preview.Taint{
				{
					Key:    to.Ptr(ystreamTaintKey),
					Value:  to.Ptr(ystreamTaintValue),
					Effect: to.Ptr(hcpsdk20260901preview.EffectNoSchedule),
				},
			}
			_, err = framework.CreateNodePoolAndWait20260901(
				ctx,
				tc.Get20260901ClientFactoryOrDie(ctx).NewNodePoolsClient(),
				*resourceGroup.Name,
				clusterName,
				ystreamTaintedNodePoolName,
				nodePool,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create tainted node pool %q for cluster %q", ystreamTaintedNodePoolName, clusterName)

			By("waiting for tainted guest nodes to be ready")
			Eventually(func() error {
				if err := verifiers.VerifyNodeCount(clusterName, ystreamTaintedExpectedNodes).Verify(ctx, adminRESTConfig); err != nil {
					return err
				}
				return verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)
			}).WithContext(ctx).WithTimeout(10*time.Minute).WithPolling(framework.StandardPollInterval).Should(Succeed(), "tainted nodes were not ready after creating node pool %q on cluster %q", ystreamTaintedNodePoolName, clusterName)

			By("verifying every worker is tainted NoSchedule")
			expectWorkersTaintedNoSchedule(ctx, kubeClient, clusterName)

			Expect(ctx.Err()).NotTo(HaveOccurred(), "test context expired before triggering upgrade for cluster %q", clusterName)
			preUpgradeKubeAPIServerVersion, err := kubeClient.Discovery().ServerVersion()
			Expect(err).NotTo(HaveOccurred(), "failed to get pre-upgrade kube-apiserver version for cluster %q", clusterName)

			By(fmt.Sprintf("triggering control plane y-stream upgrade to %s (target minor %s)", upgradeVersionId,
				upgradeVersion.String()))
			current, err := hcpClient.Get(ctx, *resourceGroup.Name, clusterName, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to get current tags before upgrading cluster %q", clusterName)
			upgradeVersionLine, upgradeTags := framework.ControlPlaneExactVersionPatchTags(upgradeVersionId)
			update := hcpsdk20260901preview.HcpOpenShiftClusterUpdate{
				Tags: framework.TagsForPatch(current.Tags, upgradeTags),
				Properties: &hcpsdk20260901preview.HcpOpenShiftClusterPropertiesUpdate{
					Version: &hcpsdk20260901preview.VersionProfileUpdate{
						ID:           to.Ptr(upgradeVersionLine),
						ChannelGroup: to.Ptr(channelGroup),
					},
				},
			}
			_, err = framework.UpdateHCPCluster20260901(ctx, hcpClient, *resourceGroup.Name, clusterName, update, framework.HCPClusterVersionUpgradeTimeout)
			Expect(err).NotTo(HaveOccurred(), "failed to trigger y-stream upgrade of cluster %q to %s", clusterName, upgradeVersionId)

			activeVersionsClient := tc.Get20261001ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()

			By("verifying control plane reached the target minor")
			Eventually(func() error {
				// Every worker carries an untolerated NoSchedule taint, so add-on operands
				// cannot schedule and the guest CVO may never report the upgrade as complete.
				// The activeVersions check reads HostedCluster controlPlaneVersion by way of
				// the backend, and so reports control-plane completion regardless. All three must pass.
				return errors.Join(
					verifiers.VerifyKubeAPIServerServerVersionUpgraded(preUpgradeKubeAPIServerVersion).Verify(ctx, adminRESTConfig),
					verifiers.VerifyHostedControlPlaneYStreamUpgrade(installVersionId, upgradeVersionId).Verify(ctx, adminRESTConfig),
					verifiers.VerifyClusterActiveVersionsSettledAtMinor(
						ctx,
						activeVersionsClient,
						*resourceGroup.Name,
						clusterName,
						upgradeVersionId),
				)
			}).WithContext(ctx).WithTimeout(framework.HCPClusterVersionUpgradeTimeout).WithPolling(2*time.Minute).Should(Succeed(), "control plane did not reach %s on cluster %q while all workers are tainted NoSchedule", upgradeVersionId, clusterName)

			By("verifying cluster API remains reachable after control plane upgrade")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to verify cluster %q API after y-stream upgrade to %s", clusterName, upgradeVersionId)

			By("asserting workers are still tainted NoSchedule after the control plane upgrade")
			expectWorkersTaintedNoSchedule(ctx, kubeClient, clusterName)
		},
		Entry("from 4.20 minor to 4.21 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "4.21"),
		Entry("from 4.21 minor to 4.22 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "4.22"),
		Entry("from 4.22 minor to 4.23 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "4.23"),
		Entry("from 4.22 minor to 5.0 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.0"),
		Entry("from 5.0 minor to 5.1 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.1"),
		Entry("from 5.1 minor to 5.2 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.2"),
		Entry("from 5.2 minor to 5.3 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.3"),
		Entry("from 5.3 minor to 5.4 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.4"),
		Entry("from 5.4 minor to 5.5 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.5"),
		Entry("from 5.5 minor to 5.6 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.6"),
		Entry("from 5.6 minor to 5.7 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.7"),
		Entry("from 5.7 minor to 5.8 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.8"),
		Entry("from 5.8 minor to 5.9 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.9"),
		Entry("from 5.9 minor to 5.10 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.10"),
		Entry("from 5.10 minor to 5.11 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.11"),
		Entry("from 5.11 minor to 5.12 minor", labels.RequireNothing, labels.Critical, labels.Positive, labels.AroRpApiCompatible, labels.Slow, "5.12"),
	)
})

func expectWorkersTaintedNoSchedule(ctx context.Context, kubeClient kubernetes.Interface, clusterName string) {
	nodes, err := kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	Expect(err).NotTo(HaveOccurred(), "failed to list nodes on cluster %q", clusterName)
	Expect(framework.HasNodeTaint(nodes.Items, ystreamTaintKey, ystreamTaintValue, corev1.TaintEffectNoSchedule, ystreamTaintedExpectedNodes)).To(BeTrue(),
		"expected all %d workers on cluster %q to have taint %s=%s:NoSchedule",
		ystreamTaintedExpectedNodes, clusterName, ystreamTaintKey, ystreamTaintValue)
}
