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

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	configv1 "github.com/openshift/api/config/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

const (
	canaryNamespace   = "openshift-ingress-canary"
	networkPolicyName = "block-canary-traffic"
)

func createIngressBlockingNetworkPolicy(ctx context.Context, kubeClient kubernetes.Interface) {
	networkPolicy := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      networkPolicyName,
			Namespace: canaryNamespace,
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeIngress,
			},
		},
	}
	_, err := kubeClient.NetworkingV1().NetworkPolicies(canaryNamespace).Create(ctx, networkPolicy, metav1.CreateOptions{})
	Expect(err).NotTo(HaveOccurred(), "failed to create NetworkPolicy %s in namespace %s", networkPolicyName, canaryNamespace)
}

func deleteIngressBlockingNetworkPolicy(ctx context.Context, kubeClient kubernetes.Interface) {
	err := kubeClient.NetworkingV1().NetworkPolicies(canaryNamespace).Delete(ctx, networkPolicyName, metav1.DeleteOptions{})
	Expect(err).NotTo(HaveOccurred(), "failed to delete NetworkPolicy %s", networkPolicyName)
}

func waitForIngressDegraded(ctx context.Context, configClient configv1client.ConfigV1Interface, timeout time.Duration) {
	Eventually(func(g Gomega) {
		co, err := configClient.ClusterOperators().Get(ctx, "ingress", metav1.GetOptions{})
		g.Expect(err).NotTo(HaveOccurred(), "failed to get ingress cluster operator")
		g.Expect(getConditionStatus(co, configv1.OperatorDegraded)).To(
			Equal(configv1.ConditionTrue),
			"ingress operator should be Degraded after blocking canary traffic",
		)
	}).WithContext(ctx).WithTimeout(timeout).WithPolling(15*time.Second).Should(Succeed(),
		"ingress cluster operator did not report Degraded",
	)
}

func waitForIngressHealthy(ctx context.Context, configClient configv1client.ConfigV1Interface, timeout time.Duration) {
	Eventually(func(g Gomega) {
		co, err := configClient.ClusterOperators().Get(ctx, "ingress", metav1.GetOptions{})
		g.Expect(err).NotTo(HaveOccurred(), "failed to get ingress cluster operator")
		g.Expect(getConditionStatus(co, configv1.OperatorDegraded)).To(
			Equal(configv1.ConditionFalse),
			"ingress operator should stop being Degraded after removing NetworkPolicy",
		)
		g.Expect(getConditionStatus(co, configv1.OperatorAvailable)).To(
			Equal(configv1.ConditionTrue),
			"ingress operator should be Available after removing NetworkPolicy",
		)
	}).WithContext(ctx).WithTimeout(timeout).WithPolling(15*time.Second).Should(Succeed(),
		"ingress cluster operator did not recover",
	)
}

func setupClusterWithNodePool(ctx context.Context, clusterName, nodePoolName, rgPrefix string) (kubernetes.Interface, configv1client.ConfigV1Interface) {
	const nodePoolReplicas = 2

	tc := framework.NewTestContext()

	if tc.UsePooledIdentities() {
		err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
		Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
	}

	By("creating a resource group")
	resourceGroup, err := tc.NewResourceGroup(ctx, rgPrefix, tc.Location())
	Expect(err).NotTo(HaveOccurred(), "failed to create resource group for %s", rgPrefix)

	By("creating cluster parameters")
	clusterParams := framework.NewDefaultClusterParams20260901()
	clusterParams.ClusterName = clusterName
	managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
	clusterParams.ManagedResourceGroupName = managedResourceGroupName

	By("creating customer resources")
	clusterParams, err = tc.CreateClusterCustomerResources20260901(ctx,
		resourceGroup,
		clusterParams,
		map[string]any{},
		TestArtifactsFS,
		framework.RBACScopeResourceGroup,
	)
	Expect(err).NotTo(HaveOccurred(), "failed to create cluster customer resources for %s", clusterName)

	By("creating the HCP cluster")
	err = tc.CreateHCPClusterFromParam20260901(ctx,
		GinkgoLogr,
		*resourceGroup.Name,
		clusterParams,
		nil,
		framework.ClusterCreationTimeout,
	)
	Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %s", clusterName)

	By("getting admin credentials for the cluster")
	adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20260901(
		ctx,
		tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
		*resourceGroup.Name,
		clusterName,
		framework.GetAdminRESTConfigTimeout,
	)
	Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for cluster %s", clusterName)

	By("creating the node pool")
	npParams := framework.NewDefaultNodePoolParams20260901()
	npParams.NodePoolName = nodePoolName
	npParams.Replicas = int32(nodePoolReplicas)

	err = tc.CreateNodePoolFromParam20260901(ctx,
		GinkgoLogr,
		*resourceGroup.Name,
		managedResourceGroupName,
		clusterName,
		npParams,
		framework.NodePoolCreationTimeout,
	)
	Expect(err).NotTo(HaveOccurred(), "failed to create node pool %s", nodePoolName)

	By("creating Kubernetes and OpenShift clients")
	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	Expect(err).NotTo(HaveOccurred(), "failed to create Kubernetes client from admin REST config")

	configClient, err := configv1client.NewForConfig(adminRESTConfig)
	Expect(err).NotTo(HaveOccurred(), "failed to create OpenShift config client from admin REST config")

	return kubeClient, configClient
}

var _ = Describe("Customer", func() {

	It("should sustain ingress degradation for health signal validation",
		labels.RequireNothing,
		labels.Medium,
		labels.Positive,
		labels.MIContainers(1),
		func(ctx context.Context) {
			const (
				degradedDetectTimeout = 10 * time.Minute
				degradedHoldDuration  = 4 * time.Hour
				degradedPollInterval  = 5 * time.Minute
				heartbeatInterval     = 30 * time.Minute
				recoverySoakDuration  = 30 * time.Minute
			)

			kubeClient, configClient := setupClusterWithNodePool(ctx,
				"ingress-sustained", "ingress-sus-np", "rg-ingress-sustained",
			)

			By("verifying ingress cluster operator is available before applying break")
			waitForIngressHealthy(ctx, configClient, degradedDetectTimeout)

			By("applying NetworkPolicy to block ingress canary traffic")
			createIngressBlockingNetworkPolicy(ctx, kubeClient)

			By("waiting for ingress cluster operator to report Degraded")
			waitForIngressDegraded(ctx, configClient, degradedDetectTimeout)

			By(fmt.Sprintf("holding ingress degraded for %s for health signal validation", degradedHoldDuration))
			holdStart := time.Now()
			lastState := ""
			lastHeartbeat := holdStart

			Consistently(func(g Gomega) {
				co, err := configClient.ClusterOperators().Get(ctx, "ingress", metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred(), "failed to get ingress cluster operator during hold")

				degraded := getConditionStatus(co, configv1.OperatorDegraded) == configv1.ConditionTrue
				available := getConditionStatus(co, configv1.OperatorAvailable) == configv1.ConditionTrue
				elapsed := time.Since(holdStart).Round(time.Minute)

				state := fmt.Sprintf("degraded=%t available=%t", degraded, available)
				switch {
				case state != lastState:
					GinkgoLogr.Info("sustained hold state changed",
						"state", state, "previous", lastState, "elapsed", elapsed.String())
					lastState = state
					lastHeartbeat = time.Now()
				case time.Since(lastHeartbeat) >= heartbeatInterval:
					GinkgoLogr.Info("sustained hold in progress",
						"state", state, "elapsed", elapsed.String(),
						"remaining", (degradedHoldDuration - time.Since(holdStart)).Round(time.Minute).String())
					lastHeartbeat = time.Now()
				}

				g.Expect(degraded).To(BeTrue(),
					"ingress operator stopped being Degraded after %s; health signal no longer valid", elapsed)
			}).WithContext(ctx).WithTimeout(degradedHoldDuration).WithPolling(degradedPollInterval).Should(Succeed(),
				"ingress operator did not stay Degraded for the full %s hold", degradedHoldDuration,
			)

			By("removing NetworkPolicy to restore ingress")
			deleteIngressBlockingNetworkPolicy(ctx, kubeClient)

			By("verifying ingress cluster operator recovers")
			waitForIngressHealthy(ctx, configClient, degradedDetectTimeout)

			By(fmt.Sprintf("holding healthy state for %s so Prometheus scrapes the recovery", recoverySoakDuration))
			recoverySoakStart := time.Now()
			Consistently(func(g Gomega) {
				co, err := configClient.ClusterOperators().Get(ctx, "ingress", metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred(), "failed to get ingress cluster operator during recovery soak")
				g.Expect(getConditionStatus(co, configv1.OperatorDegraded)).To(
					Equal(configv1.ConditionFalse),
					"ingress operator became Degraded again during recovery soak after %s",
					time.Since(recoverySoakStart).Round(time.Minute),
				)
			}).WithContext(ctx).WithTimeout(recoverySoakDuration).WithPolling(degradedPollInterval).Should(Succeed(),
				"ingress operator did not stay healthy for the full recovery soak",
			)
		})

	It("should produce intermittent ingress degradation for health signal validation",
		labels.RequireNothing,
		labels.Medium,
		labels.Positive,
		labels.MIContainers(1),
		func(ctx context.Context) {
			const (
				degradedDetectTimeout = 10 * time.Minute
				recoveryTimeout       = 10 * time.Minute
				recoverySoakDuration  = 30 * time.Minute

				// Each cycle: apply NetworkPolicy, wait for degradation,
				// hold for 15 minutes (under the 30m sustained threshold),
				// then remove and let it recover before the next cycle.
				cycleHoldDuration = 15 * time.Minute
				cyclePollInterval = 2 * time.Minute
				cycleCount        = 4
				heartbeatInterval = 5 * time.Minute
			)

			kubeClient, configClient := setupClusterWithNodePool(ctx,
				"ingress-intermittent", "ingress-int-np", "rg-ingress-intermittent",
			)

			By("verifying ingress cluster operator is available before starting cycles")
			waitForIngressHealthy(ctx, configClient, degradedDetectTimeout)

			for cycle := 1; cycle <= cycleCount; cycle++ {
				By(fmt.Sprintf("cycle %d/%d: applying NetworkPolicy to degrade ingress", cycle, cycleCount))
				createIngressBlockingNetworkPolicy(ctx, kubeClient)

				By(fmt.Sprintf("cycle %d/%d: waiting for ingress to degrade", cycle, cycleCount))
				waitForIngressDegraded(ctx, configClient, degradedDetectTimeout)

				By(fmt.Sprintf("cycle %d/%d: holding degraded for %s", cycle, cycleCount, cycleHoldDuration))
				holdStart := time.Now()
				lastHeartbeat := holdStart

				Consistently(func(g Gomega) {
					co, err := configClient.ClusterOperators().Get(ctx, "ingress", metav1.GetOptions{})
					g.Expect(err).NotTo(HaveOccurred(), "failed to get ingress cluster operator during cycle %d hold", cycle)

					degraded := getConditionStatus(co, configv1.OperatorDegraded) == configv1.ConditionTrue
					elapsed := time.Since(holdStart).Round(time.Minute)

					if time.Since(lastHeartbeat) >= heartbeatInterval {
						GinkgoLogr.Info("intermittent hold in progress",
							"cycle", fmt.Sprintf("%d/%d", cycle, cycleCount),
							"degraded", degraded, "elapsed", elapsed.String())
						lastHeartbeat = time.Now()
					}

					g.Expect(degraded).To(BeTrue(),
						"ingress operator stopped being Degraded during cycle %d after %s", cycle, elapsed)
				}).WithContext(ctx).WithTimeout(cycleHoldDuration).WithPolling(cyclePollInterval).Should(Succeed(),
					"ingress operator did not stay Degraded for cycle %d hold", cycle,
				)

				By(fmt.Sprintf("cycle %d/%d: removing NetworkPolicy to let ingress recover", cycle, cycleCount))
				deleteIngressBlockingNetworkPolicy(ctx, kubeClient)

				By(fmt.Sprintf("cycle %d/%d: waiting for ingress to recover", cycle, cycleCount))
				waitForIngressHealthy(ctx, configClient, recoveryTimeout)

				GinkgoLogr.Info("intermittent cycle complete",
					"cycle", fmt.Sprintf("%d/%d", cycle, cycleCount))
			}

			By(fmt.Sprintf("holding healthy state for %s so Prometheus scrapes the recovery", recoverySoakDuration))
			recoverySoakStart := time.Now()
			Consistently(func(g Gomega) {
				co, err := configClient.ClusterOperators().Get(ctx, "ingress", metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred(), "failed to get ingress cluster operator during recovery soak")
				g.Expect(getConditionStatus(co, configv1.OperatorDegraded)).To(
					Equal(configv1.ConditionFalse),
					"ingress operator became Degraded again during recovery soak after %s",
					time.Since(recoverySoakStart).Round(time.Minute),
				)
			}).WithContext(ctx).WithTimeout(recoverySoakDuration).WithPolling(5*time.Minute).Should(Succeed(),
				"ingress operator did not stay healthy for the full recovery soak",
			)
		})
})

func getConditionStatus(co *configv1.ClusterOperator, conditionType configv1.ClusterStatusConditionType) configv1.ConditionStatus {
	for _, c := range co.Status.Conditions {
		if c.Type == conditionType {
			return c.Status
		}
	}
	return configv1.ConditionUnknown
}
