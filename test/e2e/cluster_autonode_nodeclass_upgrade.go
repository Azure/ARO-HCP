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
	"slices"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	"github.com/blang/semver/v4"

	hcpsdk20270201 "github.com/Azure/ARO-HCP/test/sdk/v20270201/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Customer", func() {
	DescribeTable("should replace autonode nodes when OpenShiftAzureNodeClass spec.version changes",
		labels.MIContainers(1),
		func(ctx context.Context, nodeClassMinor, targetMinor string) {
			// Each entry needs its own cluster name. The entries run in parallel.
			clusterName := fmt.Sprintf("autonode-upgrade-%s-%s",
				strings.ReplaceAll(nodeClassMinor, ".", ""), strings.ReplaceAll(targetMinor, ".", ""))
			tc := framework.NewTestContext()

			By("checking that the v20270201 API is deployed before creating Azure resources")
			apiDeployed, probeErr := framework.IsAPI20270201Deployed(ctx, tc.Get20270201ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient())
			Expect(probeErr).NotTo(HaveOccurred(), "failed to probe the v20270201 API")
			if !apiDeployed {
				if time.Now().Before(framework.V20270201DeploymentDeadline) {
					Skip(fmt.Sprintf("v20270201 API not yet deployed; skipping until %s", framework.V20270201DeploymentDeadline.Format(time.RFC3339)))
				}
				Fail(fmt.Sprintf("v20270201 API still not deployed as of %s deadline", framework.V20270201DeploymentDeadline.Format(time.RFC3339)))
			}

			By("reading the OpenShift versions that the service offers before creating Azure resources")
			// The service list is authoritative. The spec does not guess a version
			// that the region cannot install. Pre-release builds are ignored, because
			// a customer cannot upgrade to them.
			//
			// The control plane runs at the target version. The CRD requires a
			// NodeClass version that is not newer than the control plane.
			baselineVersion, parseErr := semver.ParseTolerant(nodeClassMinor)
			Expect(parseErr).NotTo(HaveOccurred(), "failed to parse the initial NodeClass minor %q", nodeClassMinor)
			if baselineVersion.LT(semver.Version{Major: 4, Minor: 22}) {
				Fail(fmt.Sprintf("AutoNode needs OpenShift 4.22 or later, because the autonode data plane operator identity exists only from that version; got %q", nodeClassMinor))
			}
			versionsClient := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftVersionsClient()
			versionsPager := versionsClient.NewListPager(tc.Location(), nil)
			var offeredVersions []semver.Version
			for versionsPager.More() {
				page, pageErr := versionsPager.NextPage(ctx)
				Expect(pageErr).NotTo(HaveOccurred(), "failed to list the OpenShift versions that the service offers in %s", tc.Location())
				for _, offered := range page.Value {
					if offered.Name == nil {
						continue
					}
					parsed, versionErr := semver.ParseTolerant(*offered.Name)
					if versionErr != nil {
						continue
					}
					if len(parsed.Pre) > 0 {
						continue
					}
					offeredVersions = append(offeredVersions, parsed)
				}
			}
			Expect(offeredVersions).NotTo(BeEmpty(), "the service must offer at least one OpenShift version in %s", tc.Location())

			var initialVersion, targetVersion string
			if nodeClassMinor == targetMinor {
				// z-stream change: start one release behind the target release.
				zStreams := versionsInMinor(offeredVersions, targetMinor)
				if len(zStreams) < 2 {
					Skip(fmt.Sprintf("a z-stream change needs two releases in %s, and the service offers %d in %s", targetMinor, len(zStreams), tc.Location()))
				}
				initialVersion = zStreams[len(zStreams)-2].String()
				targetVersion = zStreams[len(zStreams)-1].String()
			} else {
				// y-stream change: start on the tip of the older minor.
				initialReleases := versionsInMinor(offeredVersions, nodeClassMinor)
				if len(initialReleases) == 0 {
					Skip(fmt.Sprintf("the service offers no release in %s for %s; offered versions: %v", nodeClassMinor, tc.Location(), offeredVersions))
				}
				targetReleases := versionsInMinor(offeredVersions, targetMinor)
				if len(targetReleases) == 0 {
					Skip(fmt.Sprintf("the service offers no release in %s for %s; offered versions: %v", targetMinor, tc.Location(), offeredVersions))
				}
				initialVersion = initialReleases[len(initialReleases)-1].String()
				targetVersion = targetReleases[len(targetReleases)-1].String()
			}
			GinkgoLogr.Info("Selected the NodeClass upgrade pair", "initial", initialVersion, "target", targetVersion)
			channelGroup := framework.DefaultOpenshiftChannelGroup()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "autonode-upgrade", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create the resource group for the NodeClass upgrade")

			// Create the control plane at the target. Only Karpenter nodes upgrade.
			By("creating cluster parameters with AutoNode enabled at the target release")
			clusterParams := framework.NewDefaultClusterParams20270201()
			clusterParams.ClusterName = clusterName
			clusterParams.OpenshiftVersionId = targetVersion
			clusterParams.ChannelGroup = channelGroup
			clusterParams.AutoNodeMode = to.Ptr(hcpsdk20270201.AutoNodeModeEnabled)
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20270201(ctx,
				resourceGroup, clusterParams, map[string]any{}, TestArtifactsFS, framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for the NodeClass upgrade")
			validateAutoNodeIdentity20270201(clusterParams.UserAssignedIdentitiesProfile)

			By("creating the HCP cluster at the target release with AutoNode enabled")
			err = tc.CreateHCPClusterFromParam20270201(ctx, GinkgoLogr, *resourceGroup.Name, clusterParams, nil, framework.ClusterCreationTimeout)
			if framework.IsAPINotDeployedError(err) {
				if time.Now().Before(framework.V20270201DeploymentDeadline) {
					Skip(fmt.Sprintf("v20270201 API not yet deployed; skipping until %s", framework.V20270201DeploymentDeadline.Format(time.RFC3339)))
				}
				Fail(fmt.Sprintf("v20270201 API still not deployed as of %s deadline", framework.V20270201DeploymentDeadline.Format(time.RFC3339)))
			}
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %q at target release %s", clusterName, targetVersion)

			By("verifying the typed AutoNode field on the created cluster")
			validateAutoNodeEnabled20270201(ctx, tc.Get20270201ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(), *resourceGroup.Name, clusterName, tc.SubscriptionID)

			By("getting admin REST config")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20270201(ctx, tc.Get20270201ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(), *resourceGroup.Name, clusterName, framework.GetAdminRESTConfigTimeout)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for HCP cluster %q", clusterName)
			adminRESTConfig.QPS = 50
			adminRESTConfig.Burst = 100
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to verify that cluster %q is available at the target release", clusterName)

			By("creating a regular worker for cluster services during node replacement")
			workerVersion := resolveAutoNodeWorkerVersion(ctx, adminRESTConfig, clusterName)
			Expect(workerVersion).To(Equal(targetVersion), "expected the control plane to complete installation at %s before the NodeClass upgrade", targetVersion)
			nodePoolParams := framework.NewDefaultNodePoolParams20270201()
			nodePoolParams.ClusterName = clusterName
			nodePoolParams.NodePoolName = "workers"
			nodePoolParams.OpenshiftVersionId = workerVersion
			nodePoolParams.ChannelGroup = channelGroup
			nodePoolParams.Replicas = 1
			err = tc.CreateNodePoolFromParam20270201(ctx, GinkgoLogr, *resourceGroup.Name, managedResourceGroupName, clusterName, nodePoolParams, framework.NodePoolCreationTimeout)
			Expect(err).NotTo(HaveOccurred(), "failed to create a regular worker for cluster services during replacement")
			err = verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to verify the regular worker is Ready before Karpenter provisioning")

			run := provisionKarpenterNode(ctx, adminRESTConfig, clusterName, karpenterRunOptions{
				nodeClassOptions: []framework.OpenShiftAzureNodeClassOption{framework.WithNodeClassVersion(initialVersion)},
			})

			By("verifying the initial node reports the older kubelet version")
			err = verifiers.VerifyKarpenterNodeClassVersion(run.resourceName, initialVersion, verifiers.NodeClassUpgradeEvidence{}, 5*time.Minute).Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to verify Karpenter nodes start on OpenShift %s", initialVersion)
			kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create the Kubernetes client for the NodeClass upgrade")
			oldNodes, err := kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{LabelSelector: framework.KarpenterNodePoolLabel + "=" + run.resourceName})
			Expect(err).NotTo(HaveOccurred(), "failed to capture Karpenter node names before the version change")
			Expect(oldNodes.Items).To(HaveLen(1), "expected one Karpenter node before the version change")
			oldNodeNames := []string{oldNodes.Items[0].Name}
			Expect(oldNodeNames).To(Equal(run.nodeNames), "expected the initial workload to run on the captured Karpenter node")
			GinkgoLogr.Info("Captured the initial Karpenter node", "name", oldNodes.Items[0].Name, "uid", oldNodes.Items[0].UID,
				"kubeletVersion", oldNodes.Items[0].Status.NodeInfo.KubeletVersion, "providerID", oldNodes.Items[0].Spec.ProviderID)

			By("capturing the NodeClass drift state before the version change")
			// Karpenter replaces a node when the NodeClaim hash differs from the
			// AKSNodeClass hash. Capture both, so the check after the change can
			// prove that the drift source moved.
			upgradeState, err := verifiers.CaptureNodeClassUpgradeState(ctx, adminRESTConfig, run.resourceName)
			Expect(err).NotTo(HaveOccurred(), "failed to capture the NodeClass drift state before the version change")
			Expect(upgradeState.NodeClassHash).NotTo(BeEmpty(), "the AKSNodeClass must carry the %s annotation before the version change", "karpenter.azure.com/aksnodeclass-hash")
			Expect(upgradeState.NodeClaimNames).NotTo(BeEmpty(), "expected at least one NodeClaim before the version change")

			// Replacement nodes need the same CSR workaround as the initial node.
			csrApproverCtx, stopCSRApprover := context.WithCancel(ctx)
			csrApproverDone := make(chan struct{})
			var csrApproverErr error
			go func() {
				defer close(csrApproverDone)
				_, csrApproverErr = framework.RunKarpenterNodeCSRApprover(csrApproverCtx, adminRESTConfig, 10*time.Second)
			}()
			DeferCleanup(func() {
				stopCSRApprover()
				<-csrApproverDone
			})

			By(fmt.Sprintf("changing only NodeClass spec.version from %s to %s", initialVersion, targetVersion))
			err = framework.UpdateOpenShiftAzureNodeClassVersion(ctx, adminRESTConfig, run.resourceName, targetVersion)
			Expect(err).NotTo(HaveOccurred(), "failed to set NodeClass %q spec.version to %s", run.resourceName, targetVersion)

			By("waiting for the old node to disappear and replacement nodes to report the target kubelet version")
			// Allow 25 minutes for provisioning and 10 for drain and deletion.
			// The shared 45-minute upgrade budget also allows time for drift detection.
			evidence := verifiers.NodeClassUpgradeEvidence{
				PreviousNodeNames:      oldNodeNames,
				PreviousNodeClaimNames: upgradeState.NodeClaimNames,
				PreviousNodeClassHash:  upgradeState.NodeClassHash,
			}
			upgradeErr := verifiers.VerifyKarpenterNodeClassVersion(run.resourceName, targetVersion, evidence, framework.NodePoolVersionUpgradeTimeout).Verify(ctx, adminRESTConfig)
			stopCSRApprover()
			<-csrApproverDone
			Expect(csrApproverErr).NotTo(HaveOccurred(), "failed to approve replacement node CSRs")
			Expect(upgradeErr).NotTo(HaveOccurred(), "failed to replace old Karpenter nodes and reach OpenShift %s after the NodeClass version change", targetVersion)

			By("verifying the existing workload runs on a replacement node")
			Eventually(ctx, func(g Gomega, ctx context.Context) {
				pods, err := kubeClient.CoreV1().Pods(run.workloadNamespace).List(ctx, metav1.ListOptions{LabelSelector: "app=" + run.workloadName})
				g.Expect(err).NotTo(HaveOccurred(), "failed to list workload pods after the NodeClass upgrade")
				g.Expect(pods.Items).To(HaveLen(1), "expected one workload pod after node replacement")
				pod := &pods.Items[0]
				g.Expect(pod.Status.Phase).To(Equal(corev1.PodRunning), "expected workload pod %q to run after node replacement", pod.Name)
				g.Expect(pod.Spec.NodeName).NotTo(BeEmpty(), "expected workload pod %q to have a replacement node", pod.Name)
				g.Expect(oldNodeNames).NotTo(ContainElement(pod.Spec.NodeName), "expected workload pod %q to leave the old node", pod.Name)
				node, err := kubeClient.CoreV1().Nodes().Get(ctx, pod.Spec.NodeName, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred(), "failed to get replacement node %q for the workload", pod.Spec.NodeName)
				g.Expect(node.Labels[framework.KarpenterNodePoolLabel]).To(Equal(run.resourceName), "expected the workload to stay on the test Karpenter NodePool")
			}, 5*time.Minute, verifiers.DefaultPollInterval).Should(Succeed(), "the workload did not run on a replacement Karpenter node")

			By("verifying a web app runs on the upgraded Karpenter nodes")
			err = verifiers.VerifySimpleWebApp(run.nodeSelector).Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to run a web app on Karpenter nodes after the NodeClass upgrade")
			// Do not wait for the Karpenter node to drain. Cleanup deletes the
			// NodePool and the cluster, and that removes the node.
		},
		Entry("z-stream within the AutoNode baseline",
			labels.RequireNothing, labels.Medium, labels.Slow, labels.Positive, labels.AroRpApiCompatible, labels.CreateCluster,
			"4.22", "4.22"),
		Entry("next y-stream above the AutoNode baseline",
			labels.RequireNothing, labels.Medium, labels.Slow, labels.Positive, labels.AroRpApiCompatible, labels.CreateCluster,
			"4.22", "4.23"),
	)
})

// versionsInMinor returns the releases that belong to one y-stream, in ascending
// order.
func versionsInMinor(offered []semver.Version, minor string) []semver.Version {
	wanted, err := semver.ParseTolerant(minor)
	if err != nil {
		return nil
	}
	var releases []semver.Version
	for _, version := range offered {
		if version.Major == wanted.Major && version.Minor == wanted.Minor {
			releases = append(releases, version)
		}
	}
	slices.SortFunc(releases, func(a, b semver.Version) int { return a.Compare(b) })
	return releases
}
