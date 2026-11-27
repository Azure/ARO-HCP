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
			"ingress operator should stop being Degraded",
		)
		g.Expect(getConditionStatus(co, configv1.OperatorAvailable)).To(
			Equal(configv1.ConditionTrue),
			"ingress operator should be Available",
		)
	}).WithContext(ctx).WithTimeout(timeout).WithPolling(15*time.Second).Should(Succeed(),
		"ingress cluster operator did not recover",
	)
}

func setupClusterWithNodePool(ctx context.Context, clusterName, nodePoolName, rgPrefix string) (kubernetes.Interface, configv1client.ConfigV1Interface) {
	const nodePoolReplicas = 1

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

// Ingress health lifecycle test
//
// Timeline (conservative, 30s scrape interval, 15m health function timer):
//
//	Minute 0        – verify healthy baseline
//	Minute 10       – apply NetworkPolicy, ingress degrades within ~5 min
//	Minute 15       – degraded scrapes begin
//	Minute 50       – 60 degraded samples collected (~35 min + buffer)
//	Minute 55-70    – health function reports Error (next timer tick)
//	Minute 70       – remove NetworkPolicy, ingress recovers within ~5 min
//	Minute 75       – healthy scrapes begin, min_over_time ≠ 1 → Healthy
//	Minute 85-90    – health function reports Healthy (next timer tick)
//	Minute 110      – 60 healthy samples in window, stable Healthy
//	Minute 130      – end of recovery soak
//
// Total: ~2 hours 10 minutes
var _ = Describe("Customer", func() {

	It("should validate ingress health signal lifecycle",
		labels.RequireNothing,
		labels.Medium,
		labels.Positive,
		labels.MIContainers(1),
		func(ctx context.Context) {
			const (
				detectTimeout        = 10 * time.Minute
				pollInterval         = 5 * time.Minute
				heartbeatInterval    = 15 * time.Minute
				degradedHoldDuration = 1 * time.Hour
				recoverySoakDuration = 1 * time.Hour
			)

			kubeClient, configClient := setupClusterWithNodePool(ctx,
				"ingress-lifecycle", "ingress-lc-np", "rg-ingress-lifecycle",
			)

			// Phase 1: Healthy baseline
			By("phase 1: verifying ingress is healthy at baseline")
			waitForIngressHealthy(ctx, configClient, detectTimeout)
			GinkgoLogr.Info("phase 1 complete: ingress healthy at baseline")

			// Phase 2: Degrade ingress
			By("phase 2: applying NetworkPolicy to block ingress canary traffic")
			createIngressBlockingNetworkPolicy(ctx, kubeClient)

			By("phase 2: waiting for ingress operator to report Degraded")
			waitForIngressDegraded(ctx, configClient, detectTimeout)

			By(fmt.Sprintf("phase 2: holding degraded for %s (60 samples + health function ticks)", degradedHoldDuration))
			holdStart := time.Now()
			lastState := ""
			lastHeartbeat := holdStart

			Consistently(func(g Gomega) {
				co, err := configClient.ClusterOperators().Get(ctx, "ingress", metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred(), "failed to get ingress cluster operator during degraded hold")

				degraded := getConditionStatus(co, configv1.OperatorDegraded) == configv1.ConditionTrue
				available := getConditionStatus(co, configv1.OperatorAvailable) == configv1.ConditionTrue
				elapsed := time.Since(holdStart).Round(time.Minute)

				state := fmt.Sprintf("degraded=%t available=%t", degraded, available)
				switch {
				case state != lastState:
					GinkgoLogr.Info("phase 2: state changed",
						"state", state, "previous", lastState, "elapsed", elapsed.String())
					lastState = state
					lastHeartbeat = time.Now()
				case time.Since(lastHeartbeat) >= heartbeatInterval:
					GinkgoLogr.Info("phase 2: degraded hold in progress",
						"state", state, "elapsed", elapsed.String(),
						"remaining", (degradedHoldDuration - time.Since(holdStart)).Round(time.Minute).String())
					lastHeartbeat = time.Now()
				}

				g.Expect(degraded).To(BeTrue(),
					"ingress operator stopped being Degraded after %s", elapsed)
			}).WithContext(ctx).WithTimeout(degradedHoldDuration).WithPolling(pollInterval).Should(Succeed(),
				"ingress operator did not stay Degraded for the full hold",
			)
			GinkgoLogr.Info("phase 2 complete: ingress held degraded",
				"duration", time.Since(holdStart).Round(time.Minute).String())

			// Phase 3: Recovery
			By("phase 3: removing NetworkPolicy to restore ingress")
			deleteIngressBlockingNetworkPolicy(ctx, kubeClient)

			By("phase 3: waiting for ingress operator to recover")
			waitForIngressHealthy(ctx, configClient, detectTimeout)

			By(fmt.Sprintf("phase 3: holding healthy for %s (60 clean samples + health function ticks)", recoverySoakDuration))
			soakStart := time.Now()
			lastHeartbeat = time.Now()

			Consistently(func(g Gomega) {
				co, err := configClient.ClusterOperators().Get(ctx, "ingress", metav1.GetOptions{})
				g.Expect(err).NotTo(HaveOccurred(), "failed to get ingress cluster operator during recovery soak")

				elapsed := time.Since(soakStart).Round(time.Minute)
				if time.Since(lastHeartbeat) >= heartbeatInterval {
					GinkgoLogr.Info("phase 3: recovery soak in progress",
						"elapsed", elapsed.String(),
						"remaining", (recoverySoakDuration - time.Since(soakStart)).Round(time.Minute).String())
					lastHeartbeat = time.Now()
				}

				g.Expect(getConditionStatus(co, configv1.OperatorDegraded)).To(
					Equal(configv1.ConditionFalse),
					"ingress operator became Degraded during recovery soak after %s", elapsed,
				)
				g.Expect(getConditionStatus(co, configv1.OperatorAvailable)).To(
					Equal(configv1.ConditionTrue),
					"ingress operator stopped being Available during recovery soak after %s", elapsed,
				)
			}).WithContext(ctx).WithTimeout(recoverySoakDuration).WithPolling(pollInterval).Should(Succeed(),
				"ingress operator did not stay healthy for the full recovery soak",
			)
			GinkgoLogr.Info("phase 3 complete: ingress recovered and stable",
				"duration", time.Since(soakStart).Round(time.Minute).String())

			// Phase 4: Cleanup → cluster deletion removes metrics → health function sees 0 clusters → 0 reports
			GinkgoLogr.Info("lifecycle complete: cluster cleanup will validate 0-report behavior")
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
