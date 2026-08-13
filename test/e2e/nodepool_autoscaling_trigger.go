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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Customer", func() {
	BeforeEach(func() {
		// do nothing. per test initialization usually ages better than shared.
	})

	It("should trigger nodepool autoscaling when pods exhaust single-node resources",
		labels.RequireNothing,
		labels.Medium,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.MIContainers(1),
		func(ctx context.Context) {
			const (
				customerClusterName        = "autoscale-trigger"
				customerNodePoolName       = "stress-pool"
				autoscalingMin       int32 = 1
				autoscalingMax       int32 = 2
			)
			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "autoscale-trigger", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group")

			By("resolving OpenShift 4.20 to the latest z-stream version")
			clusterParams := framework.NewDefaultClusterParams20240610()
			const openshiftMinor = "4.20"
			resolvedVersion, err := framework.GetLatestInstallVersion(ctx, clusterParams.ChannelGroup, openshiftMinor)
			if err != nil {
				if errors.Is(err, framework.ErrNightlyReleaseStreamNotFound) || errors.Is(err, framework.ErrNoAcceptedNightlyTags) || errors.Is(err, framework.ErrVersionNotFound) {
					Skip(fmt.Sprintf("No install version found for %s in %s channel (%s)", openshiftMinor, clusterParams.ChannelGroup, err.Error()))
				} else {
					Fail(fmt.Sprintf("failed to get latest install version for %s in %s channel: %s", openshiftMinor, clusterParams.ChannelGroup, err.Error()))
				}
			}
			clusterParams.OpenshiftVersionId = resolvedVersion

			By("creating cluster parameters for OpenShift " + resolvedVersion)
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20240610(ctx,
				resourceGroup,
				clusterParams,
				map[string]any{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create cluster customer resources")

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
			kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create Kubernetes client from admin REST config")

			By("creating the nodepool with autoscaling min=1 max=2")
			nodePoolParams := framework.NewDefaultNodePoolParams20240610()
			nodePoolParams.OpenshiftVersionId = resolvedVersion
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.AutoScaling = &framework.NodePoolAutoScalingParams{
				Min: autoscalingMin,
				Max: autoscalingMax,
			}

			err = tc.CreateNodePoolFromParam20240610(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				managedResourceGroupName,
				customerClusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create nodepool %s with autoscaling", customerNodePoolName)

			By("verifying the nodepool has the correct autoscaling configuration")
			nodePoolsClient := tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient()
			npResp, err := framework.GetNodePool20240610(ctx,
				nodePoolsClient,
				*resourceGroup.Name,
				customerClusterName,
				customerNodePoolName)
			Expect(err).NotTo(HaveOccurred(), "failed to get nodepool %s", customerNodePoolName)
			Expect(npResp.Properties).NotTo(BeNil(), "nodepool response Properties was nil")
			Expect(npResp.Properties.AutoScaling).NotTo(BeNil(), "expected nodepool to have autoscaling configuration")
			Expect(npResp.Properties.AutoScaling.Min).To(Equal(to.Ptr(autoscalingMin)),
				"expected autoscaling min to be %d", autoscalingMin)
			Expect(npResp.Properties.AutoScaling.Max).To(Equal(to.Ptr(autoscalingMax)),
				"expected autoscaling max to be %d", autoscalingMax)

			By("verifying the nodepool starts with 1 node")
			nodes, err := kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to list nodes after nodepool creation")
			poolNodes, err := framework.SelectNodesBelongingToNodePool(nodes.Items, customerNodePoolName)
			Expect(err).NotTo(HaveOccurred(), "failed to select nodes for nodepool %s", customerNodePoolName)
			Expect(len(poolNodes)).To(Equal(int(autoscalingMin)),
				"expected nodepool %s to start with %d node(s)", customerNodePoolName, autoscalingMin)

			By("creating a namespace for stress workloads on the hosted cluster")
			stressNS, err := kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: "e2e-autoscale-stress"},
			}, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to create stress namespace")

			// Each pod requests 3 CPU and 8Gi memory. The default worker VM
			// (Standard_D8s_v3, 8 vCPU / 32 GiB) fits at most 2 such pods, so
			// deploying 4 replicas guarantees at least 2 pods are Pending and
			// triggers the cluster autoscaler to provision a second node.
			By("deploying pods with high resource requests to exhaust single-node capacity")
			stressReplicas := int32(4)
			stressDeploy := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "resource-stress",
					Namespace: stressNS.Name,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: to.Ptr(stressReplicas),
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"app": "resource-stress"},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{"app": "resource-stress"},
						},
						Spec: corev1.PodSpec{
							TerminationGracePeriodSeconds: to.Ptr(int64(0)),
							Containers: []corev1.Container{
								{
									Name:  "pause",
									Image: "registry.k8s.io/pause:3.9",
									Resources: corev1.ResourceRequirements{
										Requests: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("3"),
											corev1.ResourceMemory: resource.MustParse("8Gi"),
										},
									},
								},
							},
						},
					},
				},
			}
			_, err = kubeClient.AppsV1().Deployments(stressNS.Name).Create(ctx, stressDeploy, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to create stress deployment")

			By("waiting for the cluster autoscaler to scale the nodepool to 2 nodes")
			Eventually(func(g Gomega) {
				nodeList, err := kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
				g.Expect(err).NotTo(HaveOccurred(), "failed to list nodes during autoscaling poll")
				scaledPoolNodes, err := framework.SelectNodesBelongingToNodePool(nodeList.Items, customerNodePoolName)
				g.Expect(err).NotTo(HaveOccurred(), "failed to select nodes for nodepool %s", customerNodePoolName)
				readyCount := 0
				for i := range scaledPoolNodes {
					for _, c := range scaledPoolNodes[i].Status.Conditions {
						if c.Type == corev1.NodeReady && c.Status == corev1.ConditionTrue {
							readyCount++
						}
					}
				}
				g.Expect(readyCount).To(Equal(int(autoscalingMax)),
					"expected %d ready nodes in nodepool %s, got %d", autoscalingMax, customerNodePoolName, readyCount)
			}).WithTimeout(framework.NodePoolScalingTimeout).WithPolling(30*time.Second).Should(Succeed(),
				"autoscaler did not scale nodepool %s to %d nodes within %s", customerNodePoolName, autoscalingMax, framework.NodePoolScalingTimeout)

			By("verifying stress pods are running across both nodes")
			Eventually(func(g Gomega) {
				pods, err := kubeClient.CoreV1().Pods(stressNS.Name).List(ctx, metav1.ListOptions{
					LabelSelector: "app=resource-stress",
				})
				g.Expect(err).NotTo(HaveOccurred(), "failed to list stress pods")
				nodeSet := make(map[string]bool)
				for i := range pods.Items {
					if pods.Items[i].Status.Phase == corev1.PodRunning {
						nodeSet[pods.Items[i].Spec.NodeName] = true
					}
				}
				g.Expect(len(nodeSet)).To(BeNumerically(">=", 2),
					"expected pods running on at least 2 distinct nodes after autoscaling")
			}).WithTimeout(5*time.Minute).WithPolling(15*time.Second).Should(Succeed(),
				"stress pods did not spread across nodes after autoscaling")
		})
})
