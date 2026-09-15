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

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Customer", func() {

	It("should be able to cordon, drain, and uncordon a node in an HCP cluster",
		labels.RequireNothing,
		labels.Medium,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.MIContainers(1),
		func(ctx context.Context) {
			const (
				customerClusterName  = "cordon-hcp-cluster"
				customerNodePoolName = "cordon-np"
				nodePoolReplicas     = 2

				cordonVerifyTimeout = 2 * time.Minute
				podScheduleTimeout  = 3 * time.Minute
				drainEvictTimeout   = 3 * time.Minute
			)

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "rg-cordon-test", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for node cordon test")

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams20240610()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20240610(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create cluster customer resources for cordon test")

			By("creating the HCP cluster")
			err = tc.CreateHCPClusterFromParam20240610(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %s", customerClusterName)

			By("getting admin credentials for the cluster")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20260901(
				ctx,
				tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for cluster %s", customerClusterName)

			By("creating the node pool")
			nodePoolParams := framework.NewDefaultNodePoolParams20240610()
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.Replicas = int32(nodePoolReplicas)

			err = tc.CreateNodePoolFromParam20240610(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				managedResourceGroupName,
				customerClusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create node pool %s", customerNodePoolName)

			By("verifying initial node count and readiness")
			Expect(verifiers.VerifyNodeCount(customerClusterName, nodePoolReplicas).Verify(ctx, adminRESTConfig)).To(
				Succeed(), "failed to verify initial node count of %d", nodePoolReplicas,
			)
			Expect(verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)).To(
				Succeed(), "failed to verify all nodes are ready after node pool creation",
			)

			By("creating a Kubernetes client")
			kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create Kubernetes client from admin REST config")

			// ── Pre-cordon ──

			By("selecting a node to cordon")
			nodes, err := kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to list nodes in the cluster")
			Expect(nodes.Items).To(HaveLen(nodePoolReplicas), "expected %d nodes but found %d", nodePoolReplicas, len(nodes.Items))

			targetNode := nodes.Items[0]
			otherNode := nodes.Items[1]
			GinkgoLogr.Info("Selected node to cordon", "targetNode", targetNode.Name, "otherNode", otherNode.Name)

			targetHostname, ok := targetNode.Labels[corev1.LabelHostname]
			Expect(ok).To(BeTrue(), "target node %s is missing %q label", targetNode.Name, corev1.LabelHostname)
			Expect(targetHostname).NotTo(BeEmpty(), "label %s on node %s must not be empty", corev1.LabelHostname, targetNode.Name)

			By("creating a test namespace for scheduling and drain verification")
			testNamespace := "cordon-test-ns"
			_, err = kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: testNamespace},
			}, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to create test namespace %s", testNamespace)

			By("creating a service account for test pods")
			testSA, err := kubeClient.CoreV1().ServiceAccounts(testNamespace).Create(ctx, &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{Name: "cordon-test-sa"},
			}, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to create service account in namespace %s", testNamespace)

			restrictedSecurityContext := &corev1.SecurityContext{
				AllowPrivilegeEscalation: ptr.To(false),
				RunAsNonRoot:             ptr.To(true),
				SeccompProfile: &corev1.SeccompProfile{
					Type: corev1.SeccompProfileTypeRuntimeDefault,
				},
				Capabilities: &corev1.Capabilities{
					Drop: []corev1.Capability{"ALL"},
				},
			}

			By(fmt.Sprintf("scheduling a pre-cordon workload pod on target node %s", targetNode.Name))
			preCordonPod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pre-cordon-workload",
					Namespace: testNamespace,
				},
				Spec: corev1.PodSpec{
					ServiceAccountName:           testSA.Name,
					AutomountServiceAccountToken: ptr.To(false),
					NodeSelector: map[string]string{
						corev1.LabelHostname: targetHostname,
					},
					Containers: []corev1.Container{
						{
							Name:            "pause",
							Image:           "registry.k8s.io/pause:3.9",
							Command:         []string{"/pause"},
							SecurityContext: restrictedSecurityContext,
						},
					},
				},
			}
			_, err = kubeClient.CoreV1().Pods(testNamespace).Create(ctx, preCordonPod, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to create pre-cordon workload pod on node %s", targetNode.Name)

			By("waiting for the pre-cordon workload pod to be Running")
			Eventually(func(g Gomega) {
				pod, err := kubeClient.CoreV1().Pods(testNamespace).Get(ctx, "pre-cordon-workload", metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred(), "failed to get pre-cordon workload pod")
				g.Expect(pod.Status.Phase).To(Equal(corev1.PodRunning),
					"expected pre-cordon workload pod to be Running but is %s", pod.Status.Phase,
				)
				g.Expect(pod.Spec.NodeName).To(Equal(targetNode.Name),
					"expected pre-cordon workload pod on node %s but found on %s", targetNode.Name, pod.Spec.NodeName,
				)
			}).WithContext(ctx).WithTimeout(podScheduleTimeout).WithPolling(5*time.Second).Should(Succeed(),
				"pre-cordon workload pod did not reach Running state on target node %s", targetNode.Name,
			)

			// ── Cordon ──

			By(fmt.Sprintf("cordoning node %s by setting spec.unschedulable=true", targetNode.Name))
			freshNode, err := kubeClient.CoreV1().Nodes().Get(ctx, targetNode.Name, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to get fresh copy of node %s before cordoning", targetNode.Name)
			freshNode.Spec.Unschedulable = true
			_, err = kubeClient.CoreV1().Nodes().Update(ctx, freshNode, metav1.UpdateOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to cordon node %s", targetNode.Name)

			By(fmt.Sprintf("verifying node %s is unschedulable", targetNode.Name))
			Eventually(func(g Gomega) {
				node, err := kubeClient.CoreV1().Nodes().Get(ctx, targetNode.Name, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred(), "failed to get node %s", targetNode.Name)
				g.Expect(node.Spec.Unschedulable).To(BeTrue(), "expected node %s to be unschedulable (cordoned)", targetNode.Name)
			}).WithContext(ctx).WithTimeout(cordonVerifyTimeout).WithPolling(5*time.Second).Should(Succeed(),
				"node %s was not marked as unschedulable after cordoning", targetNode.Name,
			)

			By("verifying both nodes remain in Ready condition after cordoning")
			Expect(verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)).To(
				Succeed(), "nodes should remain Ready after cordoning (cordon only affects scheduling, not readiness)",
			)

			By("verifying the pre-cordon workload pod is still Running (cordon does not evict existing pods)")
			preCordonPodAfter, err := kubeClient.CoreV1().Pods(testNamespace).Get(ctx, "pre-cordon-workload", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to get pre-cordon workload pod after cordoning")
			Expect(preCordonPodAfter.Status.Phase).To(Equal(corev1.PodRunning),
				"pre-cordon workload pod should still be Running after cordoning node %s", targetNode.Name,
			)
			Expect(preCordonPodAfter.Spec.NodeName).To(Equal(targetNode.Name),
				"pre-cordon workload pod should still be on cordoned node %s", targetNode.Name,
			)

			By("scheduling a new pod that targets the cordoned node via nodeSelector to verify it stays Pending")
			pendingPod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-on-cordoned-node",
					Namespace: testNamespace,
				},
				Spec: corev1.PodSpec{
					ServiceAccountName:           testSA.Name,
					AutomountServiceAccountToken: ptr.To(false),
					NodeSelector: map[string]string{
						corev1.LabelHostname: targetHostname,
					},
					Containers: []corev1.Container{
						{
							Name:            "pause",
							Image:           "registry.k8s.io/pause:3.9",
							Command:         []string{"/pause"},
							SecurityContext: restrictedSecurityContext,
						},
					},
				},
			}
			_, err = kubeClient.CoreV1().Pods(testNamespace).Create(ctx, pendingPod, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to create test pod targeting cordoned node %s", targetNode.Name)

			By("verifying the new pod targeting the cordoned node does not get scheduled")
			Consistently(func(g Gomega) {
				pod, err := kubeClient.CoreV1().Pods(testNamespace).Get(ctx, "pod-on-cordoned-node", metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred(), "failed to get pod pod-on-cordoned-node")
				g.Expect(pod.Spec.NodeName).To(BeEmpty(), "pod targeting cordoned node %s should not be assigned a node", targetNode.Name)
				g.Expect(pod.Status.Phase).To(Equal(corev1.PodPending),
					"pod targeting cordoned node %s should remain Pending but is in phase %s", targetNode.Name, pod.Status.Phase,
				)
			}).WithContext(ctx).WithTimeout(30*time.Second).WithPolling(5*time.Second).Should(Succeed(),
				"pod on cordoned node %s should remain Pending", targetNode.Name,
			)

			By("scheduling a pod without nodeSelector to verify it lands on the uncordoned node")
			freePod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-on-free-node",
					Namespace: testNamespace,
				},
				Spec: corev1.PodSpec{
					ServiceAccountName:           testSA.Name,
					AutomountServiceAccountToken: ptr.To(false),
					Containers: []corev1.Container{
						{
							Name:            "pause",
							Image:           "registry.k8s.io/pause:3.9",
							Command:         []string{"/pause"},
							SecurityContext: restrictedSecurityContext,
						},
					},
				},
			}
			_, err = kubeClient.CoreV1().Pods(testNamespace).Create(ctx, freePod, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to create test pod for scheduling on uncordoned node")

			By(fmt.Sprintf("verifying the free pod is scheduled on uncordoned node %s", otherNode.Name))
			Eventually(func(g Gomega) {
				pod, err := kubeClient.CoreV1().Pods(testNamespace).Get(ctx, "pod-on-free-node", metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred(), "failed to get pod pod-on-free-node")
				g.Expect(pod.Spec.NodeName).NotTo(BeEmpty(), "pod should be assigned to a node")
				g.Expect(pod.Spec.NodeName).To(Equal(otherNode.Name),
					"expected pod to be scheduled on uncordoned node %s but was scheduled on %s", otherNode.Name, pod.Spec.NodeName,
				)
			}).WithContext(ctx).WithTimeout(podScheduleTimeout).WithPolling(5*time.Second).Should(Succeed(),
				"free pod was not scheduled on the uncordoned node %s", otherNode.Name,
			)

			// ── Drain: evict the pre-cordon workload while the node is still cordoned ──

			By(fmt.Sprintf("draining node %s by evicting the pre-cordon workload pod", targetNode.Name))
			eviction := &policyv1.Eviction{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pre-cordon-workload",
					Namespace: testNamespace,
				},
				DeleteOptions: &metav1.DeleteOptions{
					GracePeriodSeconds: ptr.To(int64(0)),
				},
			}
			err = kubeClient.CoreV1().Pods(testNamespace).EvictV1(ctx, eviction)
			Expect(err).NotTo(HaveOccurred(), "failed to evict pre-cordon-workload from node %s during drain", targetNode.Name)

			By(fmt.Sprintf("verifying the pre-cordon workload pod is evicted from drained node %s", targetNode.Name))
			Eventually(func(g Gomega) {
				remainingPods, err := kubeClient.CoreV1().Pods(testNamespace).List(ctx, metav1.ListOptions{
					FieldSelector: "spec.nodeName=" + targetNode.Name,
				})
				g.Expect(err).NotTo(HaveOccurred(), "failed to list pods on node %s", targetNode.Name)
				g.Expect(remainingPods.Items).To(BeEmpty(),
					"expected no test namespace pods on drained node %s but found %d", targetNode.Name, len(remainingPods.Items),
				)
			}).WithContext(ctx).WithTimeout(drainEvictTimeout).WithPolling(5*time.Second).Should(Succeed(),
				"pre-cordon workload was not evicted from drained node %s", targetNode.Name,
			)

			// ── Uncordon and verify scheduling resumes ──

			By(fmt.Sprintf("uncordoning node %s by setting spec.unschedulable=false", targetNode.Name))
			freshNode, err = kubeClient.CoreV1().Nodes().Get(ctx, targetNode.Name, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to get fresh copy of node %s before uncordoning", targetNode.Name)
			freshNode.Spec.Unschedulable = false
			_, err = kubeClient.CoreV1().Nodes().Update(ctx, freshNode, metav1.UpdateOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to uncordon node %s", targetNode.Name)

			By(fmt.Sprintf("verifying node %s is schedulable again", targetNode.Name))
			Eventually(func(g Gomega) {
				node, err := kubeClient.CoreV1().Nodes().Get(ctx, targetNode.Name, metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred(), "failed to get node %s", targetNode.Name)
				g.Expect(node.Spec.Unschedulable).To(BeFalse(), "expected node %s to be schedulable (uncordoned)", targetNode.Name)
			}).WithContext(ctx).WithTimeout(cordonVerifyTimeout).WithPolling(5*time.Second).Should(Succeed(),
				"node %s was not marked as schedulable after uncordoning", targetNode.Name,
			)

			By(fmt.Sprintf("verifying the previously Pending pod-on-cordoned-node is now Running on %s after uncordoning", targetNode.Name))
			Eventually(func(g Gomega) {
				pod, err := kubeClient.CoreV1().Pods(testNamespace).Get(ctx, "pod-on-cordoned-node", metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred(), "failed to get pod pod-on-cordoned-node after uncordoning")
				g.Expect(pod.Spec.NodeName).To(Equal(targetNode.Name),
					"expected pod-on-cordoned-node to be scheduled on uncordoned node %s but was on %s", targetNode.Name, pod.Spec.NodeName,
				)
				g.Expect(pod.Status.Phase).To(Equal(corev1.PodRunning),
					"expected pod-on-cordoned-node to be Running after uncordoning but is %s", pod.Status.Phase,
				)
			}).WithContext(ctx).WithTimeout(podScheduleTimeout).WithPolling(5*time.Second).Should(Succeed(),
				"pod-on-cordoned-node was not scheduled on node %s after uncordoning", targetNode.Name,
			)

			By("verifying all nodes remain ready and schedulable after the cordon/drain/uncordon cycle")
			Expect(verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)).To(
				Succeed(), "all nodes should be ready after the cordon/drain/uncordon cycle",
			)
			Expect(verifiers.VerifyNodePoolReadyAndSchedulableNodeCount(customerNodePoolName, nodePoolReplicas).Verify(ctx, adminRESTConfig)).To(
				Succeed(), "expected %d ready and schedulable nodes after the cordon/drain/uncordon cycle", nodePoolReplicas,
			)
		})
})
