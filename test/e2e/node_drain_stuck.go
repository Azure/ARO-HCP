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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"

	hcpsdk20251223preview "github.com/Azure/ARO-HCP/test/sdk/v20251223preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

const (
	drainBlockNamespace = "e2e-drain-block"
	drainBlockApp       = "drain-blocker"
)

func deployDrainBlocker(ctx context.Context, kubeClient kubernetes.Interface, replicas int32) {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: drainBlockNamespace},
	}
	_, err := kubeClient.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	Expect(err).NotTo(HaveOccurred(), "failed to create namespace %s", drainBlockNamespace)

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      drainBlockApp,
			Namespace: drainBlockNamespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: to.Ptr(replicas),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": drainBlockApp},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": drainBlockApp},
				},
				Spec: corev1.PodSpec{
					Affinity: &corev1.Affinity{
						PodAntiAffinity: &corev1.PodAntiAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{{
								LabelSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{"app": drainBlockApp},
								},
								TopologyKey: "kubernetes.io/hostname",
							}},
						},
					},
					Containers: []corev1.Container{{
						Name:    "blocker",
						Image:   "registry.access.redhat.com/ubi9/ubi-minimal:latest",
						Command: []string{"sleep", "infinity"},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("10m"),
								corev1.ResourceMemory: resource.MustParse("16Mi"),
							},
						},
					}},
				},
			},
		},
	}
	_, err = kubeClient.AppsV1().Deployments(drainBlockNamespace).Create(ctx, deployment, metav1.CreateOptions{})
	Expect(err).NotTo(HaveOccurred(), "failed to create drain blocker deployment")

	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      drainBlockApp,
			Namespace: drainBlockNamespace,
		},
		Spec: policyv1.PodDisruptionBudgetSpec{
			MinAvailable: &intstr.IntOrString{Type: intstr.Int, IntVal: replicas},
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": drainBlockApp},
			},
		},
	}
	_, err = kubeClient.PolicyV1().PodDisruptionBudgets(drainBlockNamespace).Create(ctx, pdb, metav1.CreateOptions{})
	Expect(err).NotTo(HaveOccurred(), "failed to create drain blocker PDB")
}

func waitForDrainBlockerReady(ctx context.Context, kubeClient kubernetes.Interface, replicas int32, timeout time.Duration) {
	Eventually(func(g Gomega) {
		deploy, err := kubeClient.AppsV1().Deployments(drainBlockNamespace).Get(ctx, drainBlockApp, metav1.GetOptions{})
		g.Expect(err).NotTo(HaveOccurred(), "failed to get drain blocker deployment")
		g.Expect(deploy.Status.ReadyReplicas).To(Equal(replicas),
			"drain blocker deployment not fully ready: %d/%d", deploy.Status.ReadyReplicas, replicas)
	}).WithContext(ctx).WithTimeout(timeout).WithPolling(10 * time.Second).Should(Succeed(),
		"drain blocker deployment did not become ready",
	)
}

func deleteDrainBlockerPDB(ctx context.Context, kubeClient kubernetes.Interface) {
	err := kubeClient.PolicyV1().PodDisruptionBudgets(drainBlockNamespace).Delete(ctx, drainBlockApp, metav1.DeleteOptions{})
	Expect(err).NotTo(HaveOccurred(), "failed to delete drain blocker PDB")
}

func nodeHasCordoned(nodes []corev1.Node) bool {
	for i := range nodes {
		if nodes[i].Spec.Unschedulable {
			return true
		}
	}
	return false
}

var _ = Describe("Customer", func() {

	It("should validate node drain health signal lifecycle",
		labels.RequireNothing,
		labels.Medium,
		labels.Positive,
		labels.MIContainers(1),
		func(ctx context.Context) {
			const (
				nodePoolReplicas      = 2
				deployReadyTimeout    = 5 * time.Minute
				drainDetectTimeout    = 15 * time.Minute
				stuckDrainHoldDur     = 1 * time.Hour
				stuckDrainPollInt     = 5 * time.Minute
				heartbeatInterval     = 15 * time.Minute
				scaleDownTimeout      = 60 * time.Minute
				nodeRecoveryTimeout   = 30 * time.Minute
				recoverySoakDuration  = 30 * time.Minute
			)

			clusterName := "drain-lifecycle"
			nodePoolName := "drain-lc-np"
			rgPrefix := "rg-drain-lifecycle"

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, rgPrefix, tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group")
			rgName := *resourceGroup.Name

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams20260901()
			clusterParams.ClusterName = clusterName
			managedRGName := framework.SuffixName(rgName, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedRGName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20260901(ctx,
				resourceGroup,
				clusterParams,
				map[string]any{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create cluster customer resources")

			By("creating the HCP cluster")
			err = tc.CreateHCPClusterFromParam20260901(ctx,
				GinkgoLogr,
				rgName,
				clusterParams,
				nil,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %s", clusterName)

			By("getting admin credentials for the cluster")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20260901(
				ctx,
				tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				rgName,
				clusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config")

			By("creating the node pool with no drain timeout limit")
			npParams := framework.NewDefaultNodePoolParams20260901()
			npParams.NodePoolName = nodePoolName
			npParams.Replicas = int32(nodePoolReplicas)
			npParams.NodeDrainTimeoutMinutes = to.Ptr(int32(0))

			err = tc.CreateNodePoolFromParam20260901(ctx,
				GinkgoLogr,
				rgName,
				managedRGName,
				clusterName,
				npParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create node pool %s", nodePoolName)

			kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create Kubernetes client")

			DeferCleanup(func(cleanupCtx context.Context) {
				_ = kubeClient.PolicyV1().PodDisruptionBudgets(drainBlockNamespace).Delete(cleanupCtx, drainBlockApp, metav1.DeleteOptions{})
			})

			// Phase 1: Deploy drain blocker and trigger stuck drain
			By("phase 1: deploying drain blocker workload with PDB across both nodes")
			deployDrainBlocker(ctx, kubeClient, int32(nodePoolReplicas))

			By("phase 1: waiting for drain blocker pods to be ready on both nodes")
			waitForDrainBlockerReady(ctx, kubeClient, int32(nodePoolReplicas), deployReadyTimeout)

			By("phase 1: starting node pool scale-down from 2 to 1 to trigger drain")
			npClient := tc.Get20251223ClientFactoryOrDie(ctx).NewNodePoolsClient()
			update := hcpsdk20251223preview.NodePoolUpdate{
				Properties: &hcpsdk20251223preview.NodePoolPropertiesUpdate{
					Replicas: to.Ptr(int32(1)),
				},
			}

			scaleErr := make(chan error, 1)
			go func() {
				_, err := framework.UpdateNodePoolAndWait20251223(ctx,
					npClient, rgName, clusterName, nodePoolName, update, stuckDrainHoldDur+scaleDownTimeout,
				)
				scaleErr <- err
			}()

			By("phase 1: waiting for a node to be cordoned (drain started)")
			Eventually(func(g Gomega) {
				nodes, err := kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
				g.Expect(err).NotTo(HaveOccurred(), "failed to list nodes")
				g.Expect(nodeHasCordoned(nodes.Items)).To(BeTrue(), "no node is cordoned yet")
			}).WithContext(ctx).WithTimeout(drainDetectTimeout).WithPolling(30 * time.Second).Should(Succeed(),
				"no node was cordoned after initiating scale-down",
			)

			// Phase 2: Hold stuck drain for health signal accumulation
			By(fmt.Sprintf("phase 2: holding stuck drain for %s for health signal validation", stuckDrainHoldDur))
			holdStart := time.Now()
			lastHeartbeat := holdStart

			Consistently(func(g Gomega) {
				nodes, err := kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
				g.Expect(err).NotTo(HaveOccurred(), "failed to list nodes during hold")

				elapsed := time.Since(holdStart).Round(time.Minute)
				g.Expect(nodes.Items).To(HaveLen(nodePoolReplicas),
					"node was removed during hold after %s; drain should still be stuck", elapsed)
				g.Expect(nodeHasCordoned(nodes.Items)).To(BeTrue(),
					"no node is cordoned during hold after %s; drain may have been abandoned", elapsed)

				if time.Since(lastHeartbeat) >= heartbeatInterval {
					GinkgoLogr.Info("phase 2: stuck drain hold in progress",
						"elapsed", elapsed.String(),
						"remaining", (stuckDrainHoldDur - time.Since(holdStart)).Round(time.Minute).String())
					lastHeartbeat = time.Now()
				}
			}).WithContext(ctx).WithTimeout(stuckDrainHoldDur).WithPolling(stuckDrainPollInt).Should(Succeed(),
				"drain did not stay stuck for the full %s hold", stuckDrainHoldDur,
			)

			// Phase 3: Recovery — remove PDB and verify drain completes
			By("phase 3: removing PDB to unblock the drain")
			deleteDrainBlockerPDB(ctx, kubeClient)

			By("phase 3: waiting for scale-down to complete")
			select {
			case err := <-scaleErr:
				Expect(err).NotTo(HaveOccurred(), "node pool scale-down failed after PDB removal")
			case <-time.After(scaleDownTimeout):
				Fail("scale-down did not complete within timeout after PDB removal")
			}

			By("phase 3: verifying node count is 1 and node is Ready")
			Eventually(func(g Gomega) {
				nodes, err := kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
				g.Expect(err).NotTo(HaveOccurred(), "failed to list nodes after scale-down")

				readyCount := 0
				for i := range nodes.Items {
					for _, cond := range nodes.Items[i].Status.Conditions {
						if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
							readyCount++
						}
					}
				}
				g.Expect(readyCount).To(Equal(1), "expected 1 Ready node after scale-down")
			}).WithContext(ctx).WithTimeout(nodeRecoveryTimeout).WithPolling(30 * time.Second).Should(Succeed(),
				"did not converge to 1 Ready node after scale-down",
			)

			By(fmt.Sprintf("phase 3: holding for %s so the health function reports no stuck drains", recoverySoakDuration))
			soakStart := time.Now()
			Consistently(func(g Gomega) {
				nodes, err := kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
				g.Expect(err).NotTo(HaveOccurred(), "failed to list nodes during recovery soak")

				readyCount := 0
				for i := range nodes.Items {
					if !nodes.Items[i].Spec.Unschedulable {
						for _, cond := range nodes.Items[i].Status.Conditions {
							if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
								readyCount++
							}
						}
					}
				}
				g.Expect(readyCount).To(Equal(1),
					"expected 1 schedulable Ready node during recovery soak after %s",
					time.Since(soakStart).Round(time.Minute))
			}).WithContext(ctx).WithTimeout(recoverySoakDuration).WithPolling(stuckDrainPollInt).Should(Succeed(),
				"node state was not stable during recovery soak",
			)
		})
})
