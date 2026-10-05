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
	"sort"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/blang/semver/v4"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	configv1 "github.com/openshift/api/config/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"

	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	hcpsdk20270330preview "github.com/Azure/ARO-HCP/test/sdk/v20270330preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

// autoNodeTestOpenshiftVersion and autoNodeTestMarketplaceImage must be kept
// in lockstep: the marketplace image is an RHCOS build tied to a specific
// OpenShift minor version, and there is no API-level way to derive one from
// the other. If autoNodeTestOpenshiftVersion changes, this image reference
// must be re-verified (e.g. `az vm image list --all --publisher
// azureopenshift --offer aro4 --sku aro_422-v2 -o table`) and updated too.
const autoNodeTestOpenshiftVersion = "4.22"

var autoNodeTestMarketplaceImage = framework.KarpenterMarketplaceImage{
	Publisher: "azureopenshift",
	Offer:     "aro4",
	SKU:       "aro_422-v2",
	Version:   "9.8.20260428",
}

// This test exercises: the enablement signal (properties.autoNode.mode,
// translated into the same AFEC-gated experimental tag and projected onto
// ExperimentalFeatures.AutoNode by admit_cluster.go's mutateClusterExperimentalFeatures
// — see the v20270330preview version package's normalizeAutoNode) plus the
// "autonode" data-plane operator identity per ClusterOperatorIdentifierAutoNode
// in internal/azure/cluster_scoped_identities_config.go; automatic delivery of
// spec.autoNode onto the cluster's HostedCluster by the backend's
// AutoNodeEnabler controller; and that Karpenter actually provisions a real
// Azure node once scheduling pressure exists, and deprovisions it again once
// that pressure is gone.
//
// Known gap this test works around rather than fixes: there is no CSR
// auto-approver for Karpenter-provisioned nodes on Azure (see
// framework.RunKarpenterNodeCSRApprover's doc comment) - a passing run here is
// not evidence that gap is closed.
var _ = Describe("Customer", func() {
	It("should be able to enable AutoNode via the typed properties.autoNode.mode field for a cluster with version >= 4.22 and provision a real node via Karpenter",
		labels.RequireNothing, labels.Medium, labels.Slow, labels.Positive, labels.AroRpApiCompatible, labels.CreateCluster,
		labels.MIContainers(0),
		func(ctx context.Context) {
			const clusterName = "autonode-enable-422"

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 0, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "autonode-enable", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for AutoNode enablement test")

			By("creating cluster parameters with version 4.22 and the typed AutoNode field set to Enabled")
			clusterParams := framework.NewDefaultClusterParams20270330()
			clusterParams.ClusterName = clusterName
			clusterParams.OpenshiftVersionId = autoNodeTestOpenshiftVersion
			// NOTE: The E2E subscription must have the ExperimentalReleaseFeatures AFEC
			// registered for this to be honored (see NewDefaultClusterParams20270330,
			// whose default tags rely on the same AFEC). Without it, admission silently
			// zeroes ExperimentalFeatures instead of rejecting the request
			// (mutateClusterExperimentalFeatures in internal/admission/admit_cluster.go),
			// so a missing AFEC registration would surface here as a failed round-trip
			// assertion below, not a clean create-time error.
			clusterParams.AutoNodeMode = to.Ptr(hcpsdk20270330preview.AutoNodeModeEnabled)

			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20270330(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
				true, // enableAutoNode
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for AutoNode enablement cluster")

			By("verifying the autonode data-plane operator identity was provisioned")
			Expect(clusterParams.UserAssignedIdentitiesProfile).NotTo(BeNil(), "UserAssignedIdentitiesProfile should be populated after creating customer resources")
			autoNodeIdentityID := clusterParams.UserAssignedIdentitiesProfile.DataPlaneOperators["autonode"]
			Expect(autoNodeIdentityID).NotTo(BeNil(), "DataPlaneOperators should contain an \"autonode\" identity")
			Expect(*autoNodeIdentityID).NotTo(BeEmpty(), "autonode data-plane identity resource ID should not be empty")

			By("creating the HCP cluster with the typed AutoNode field set")
			err = tc.CreateHCPClusterFromParam20270330(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil, // imageDigestMirrors
				framework.ClusterCreationTimeout,
			)
			if framework.IsAPINotDeployedError(err) {
				if time.Now().Before(framework.V20270330PreviewDeploymentDeadline) {
					Skip(fmt.Sprintf("v20270330preview API not yet deployed; skipping until %s", framework.V20270330PreviewDeploymentDeadline.Format(time.RFC3339)))
				}
				Fail(fmt.Sprintf("v20270330preview API still not deployed as of %s deadline", framework.V20270330PreviewDeploymentDeadline.Format(time.RFC3339)))
			}
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster with the typed AutoNode field set")

			By("verifying the typed AutoNode field round-trips on the created cluster")
			actualHCPCluster, err := tc.Get20270330ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient().Get(ctx, *resourceGroup.Name, clusterName, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to get HCP cluster %s", clusterName)
			if actualHCPCluster.Properties.AutoNode == nil || actualHCPCluster.Properties.AutoNode.Mode == nil || *actualHCPCluster.Properties.AutoNode.Mode != hcpsdk20270330preview.AutoNodeModeEnabled {
				subscriptionID, subErr := tc.SubscriptionID(ctx)
				if subErr != nil {
					subscriptionID = fmt.Sprintf("<unknown, failed to resolve: %s>", subErr)
				}
				var gotMode any
				if actualHCPCluster.Properties.AutoNode != nil {
					gotMode = actualHCPCluster.Properties.AutoNode.Mode
				}
				Fail(fmt.Sprintf(
					"\n"+
						"=================================================================\n"+
						"AUTONODE FIELD DID NOT ROUND-TRIP (got mode: %v)\n"+
						"This is almost certainly NOT an AutoNode bug: it means the %q AFEC\n"+
						"flag is not registered on this test subscription (%s), so\n"+
						"mutateClusterExperimentalFeatures (internal/admission/admit_cluster.go)\n"+
						"silently zeroed ExperimentalFeatures instead of honoring the request.\n"+
						"Fix by registering the flag on this subscription and re-running:\n"+
						"  az feature register --namespace Microsoft.RedHatOpenShift \\\n"+
						"    --name ExperimentalReleaseFeatures --subscription %s\n"+
						"=================================================================\n",
					gotMode, metadataapi.FeatureExperimentalReleaseFeatures, subscriptionID, subscriptionID,
				))
			}

			By("getting admin REST config")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20260901(
				ctx,
				tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				clusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for HCP cluster %s", clusterName)

			By("verifying the cluster is healthy with AutoNode enabled")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to verify HCP cluster %s is healthy", clusterName)

			By("creating a regular worker node pool so cluster-infra pods have somewhere to run besides the Karpenter node")
			// Without a regular node pool, every non-DaemonSet infra pod (router,
			// monitoring, image-registry, etc.) has nowhere to schedule except the
			// single Karpenter-provisioned node, since that would otherwise be the
			// only Ready node in the cluster. That starved Karpenter's own node
			// drain during teardown: deleting the NodePool tries to evict those
			// pods, but they have nowhere else to go, so the drain hangs (observed
			// as "Failed to drain node, N pods are waiting to be evicted" events
			// and VerifyKarpenterDeprovisioned timing out). Mirrors the regular
			// worker node pool created by other multi-node e2e tests (e.g.
			// complete_cluster_create_multiversion.go); the workload pod that
			// actually exercises Karpenter is pinned away from this node pool via
			// nodeSelector below.
			const workerNodePoolName = "workers"
			nodePoolParams := framework.NewDefaultNodePoolParams20260630()
			nodePoolParams.ClusterName = clusterName
			nodePoolParams.NodePoolName = workerNodePoolName
			nodePoolParams.Replicas = 1

			// The node pool API requires a concrete Major.Minor.Patch version (unlike
			// the control plane, which resolves a bare "4.22"), so resolve one from
			// the cluster's own ClusterVersion history rather than guessing.
			configClient, err := configv1client.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create OpenShift config client for cluster %s", clusterName)
			clusterVersion, err := configClient.ClusterVersions().Get(ctx, "version", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to get ClusterVersion for cluster %s", clusterName)
			var parseableVersions []string
			for _, h := range clusterVersion.Status.History {
				if _, err := semver.ParseTolerant(h.Version); err != nil {
					continue
				}
				parseableVersions = append(parseableVersions, h.Version)
				if h.State == configv1.CompletedUpdate {
					break
				}
			}
			sort.Slice(parseableVersions, func(i, j int) bool {
				vi, _ := semver.ParseTolerant(parseableVersions[i])
				vj, _ := semver.ParseTolerant(parseableVersions[j])
				return vi.LT(vj)
			})
			Expect(parseableVersions).NotTo(BeEmpty(), "no parseable node pool install version found in ClusterVersion history for cluster %s", clusterName)
			nodePoolParams.OpenshiftVersionId = parseableVersions[0]

			err = tc.CreateNodePoolFromParam20260630(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				managedResourceGroupName,
				clusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create worker node pool %q for cluster %s", workerNodePoolName, clusterName)

			By("verifying the guest Karpenter CRDs are installed")
			// Hard precondition, not a workaround target: the standalone
			// karpenter-operator installs these unconditionally on startup, so
			// their absence means the container itself is broken, not a normal
			// timing state to build a fallback around.
			err = verifiers.VerifyKarpenterCRDsInstalled(5*time.Minute).Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "guest Karpenter CRDs are not installed on cluster %s", clusterName)

			const karpenterResourceName = "default"
			karpenterInstanceTypes := []string{"Standard_D4s_v3", "Standard_D4s_v5"}

			By("applying an AKSNodeClass and NodePool for Karpenter")
			err = framework.ApplyAKSNodeClassAndNodePool(ctx, adminRESTConfig, karpenterResourceName, autoNodeTestMarketplaceImage, karpenterInstanceTypes)
			Expect(err).NotTo(HaveOccurred(), "failed to apply AKSNodeClass and NodePool %q", karpenterResourceName)
			DeferCleanup(func(ctx context.Context) {
				_ = framework.DeleteAKSNodeClassAndNodePool(ctx, adminRESTConfig, karpenterResourceName)
			})

			By("waiting for the AKSNodeClass to become ready")
			err = verifiers.VerifyAKSNodeClassReady(karpenterResourceName, 5*time.Minute).Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "AKSNodeClass %q did not become ready", karpenterResourceName)

			nodeSelector := map[string]string{
				"autonode":                       "true",
				framework.KarpenterNodePoolLabel: karpenterResourceName,
			}
			const workloadNamespace = "autonode-e2e-workload"
			const workloadName = "autonode-e2e-pending"

			By("deploying a workload matched to the Karpenter NodePool, initially at 0 replicas")
			err = framework.DeployPendingWorkload(ctx, adminRESTConfig, workloadNamespace, workloadName, nodeSelector)
			Expect(err).NotTo(HaveOccurred(), "failed to deploy pending workload %q", workloadName)
			DeferCleanup(func(ctx context.Context) {
				_ = framework.DeleteNamespace(ctx, adminRESTConfig, workloadNamespace)
			})

			kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create kubernetes client for cluster %s", clusterName)

			By("scaling the workload to 1 replica to create real scheduling pressure")
			err = framework.ScaleDeployment(ctx, adminRESTConfig, workloadNamespace, workloadName, 1)
			Expect(err).NotTo(HaveOccurred(), "failed to scale workload %q to 1 replica", workloadName)

			By("verifying the workload's pod is Pending, proving the scheduling pressure is real")
			Eventually(func(g Gomega) {
				pods, err := kubeClient.CoreV1().Pods(workloadNamespace).List(ctx, metav1.ListOptions{LabelSelector: "app=" + workloadName})
				g.Expect(err).NotTo(HaveOccurred(), "failed to list pods for workload %q", workloadName)
				g.Expect(pods.Items).To(HaveLen(1), "expected exactly one pod for workload %q", workloadName)
				g.Expect(pods.Items[0].Status.Phase).To(Equal(corev1.PodPending), "expected workload pod to be Pending: no node satisfying the NodePool's requirements should exist yet")
			}, time.Minute, 5*time.Second).Should(Succeed(), "workload pod never reached Pending phase")

			By("waiting for Karpenter to provision a real Azure node, approving its CSRs as a workaround for the missing Azure auto-approver")
			csrApproverCtx, stopCSRApprover := context.WithCancel(ctx)
			var approvedCSRCount int
			var csrApproverErr error
			csrApproverDone := make(chan struct{})
			go func() {
				defer close(csrApproverDone)
				approvedCSRCount, csrApproverErr = framework.RunKarpenterNodeCSRApprover(csrApproverCtx, adminRESTConfig, 10*time.Second)
			}()

			// Demo notes ~5 minutes for a Karpenter-provisioned Azure VM to
			// initialize and start trying to register, on top of normal
			// cluster-create time already spent above; budget generously.
			err = verifiers.VerifyKarpenterNodeProvisioned(karpenterResourceName, 25*time.Minute).Verify(ctx, adminRESTConfig)
			stopCSRApprover()
			<-csrApproverDone
			GinkgoLogr.Info("Karpenter node CSR approver finished", "approved", approvedCSRCount)
			Expect(csrApproverErr).NotTo(HaveOccurred(), "background Karpenter node CSR approver failed")
			Expect(err).NotTo(HaveOccurred(), "Karpenter never provisioned a Ready, schedulable, fully-linked node for NodePool %q", karpenterResourceName)

			By("verifying the workload's pod reaches Running on the Karpenter-provisioned node")
			Eventually(func(g Gomega) {
				pods, err := kubeClient.CoreV1().Pods(workloadNamespace).List(ctx, metav1.ListOptions{LabelSelector: "app=" + workloadName})
				g.Expect(err).NotTo(HaveOccurred(), "failed to list pods for workload %q", workloadName)
				g.Expect(pods.Items).To(HaveLen(1), "expected exactly one pod for workload %q", workloadName)
				g.Expect(pods.Items[0].Status.Phase).To(Equal(corev1.PodRunning), "expected workload pod to reach Running once Karpenter provisioned a node")
			}, 5*time.Minute, 10*time.Second).Should(Succeed(), "workload pod never reached Running phase on the Karpenter-provisioned node")

			By("scaling the workload back down and deleting the NodePool/AKSNodeClass, verifying Karpenter actually deprovisions the node")
			err = framework.ScaleDeployment(ctx, adminRESTConfig, workloadNamespace, workloadName, 0)
			Expect(err).NotTo(HaveOccurred(), "failed to scale workload %q back to 0 replicas", workloadName)
			err = framework.DeleteAKSNodeClassAndNodePool(ctx, adminRESTConfig, karpenterResourceName)
			Expect(err).NotTo(HaveOccurred(), "failed to delete AKSNodeClass/NodePool %q", karpenterResourceName)
			// 10m: an observed real run drained and deprovisioned in ~1m15s once
			// cluster-infra pods had a regular worker node pool to live on instead
			// of the Karpenter node (see the worker node pool step above), so 10m
			// leaves a comfortable margin without masking a genuine hang.
			// Consolidation itself is not the bottleneck - Karpenter's default
			// consolidateAfter is 0s when the NodePool doesn't set spec.disruption
			// (confirmed against karpenter.sh_nodepools.yaml).
			err = verifiers.VerifyKarpenterDeprovisioned(karpenterResourceName, 10*time.Minute).Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "Karpenter did not deprovision the Node/NodeClaim for NodePool %q; a VM may have leaked", karpenterResourceName)

			By("deleting the workload namespace")
			err = framework.DeleteNamespace(ctx, adminRESTConfig, workloadNamespace)
			Expect(err).NotTo(HaveOccurred(), "failed to delete workload namespace %q", workloadNamespace)
		})
})
