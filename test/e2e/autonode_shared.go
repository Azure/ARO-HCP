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
	"sort"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/blang/semver/v4"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	configv1 "github.com/openshift/api/config/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"

	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	hcpsdk20270201 "github.com/Azure/ARO-HCP/test/sdk/v20270201/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

const autoNodeTestOpenshiftVersion = "4.22"

type karpenterRun struct {
	resourceName, workloadNamespace, workloadName string
	nodeSelector                                  map[string]string
	// nodeNames holds the distinct nodes that run the workload replicas, sorted.
	nodeNames []string
}

type karpenterRunOptions struct {
	startupTaints      []corev1.Taint
	provisioningChecks verifiers.KarpenterProvisioningChecks
	// nodeClassOptions configure the test-owned NodeClass before creation.
	nodeClassOptions []framework.OpenShiftAzureNodeClassOption
	// replicas sets the workload replica count. Karpenter provisions one node per
	// replica when the workload has a required anti-affinity rule. Zero means 1.
	replicas int32
}

func validateAutoNodeIdentity20270201(profile *hcpsdk20270201.UserAssignedIdentitiesProfile) {
	GinkgoHelper()
	Expect(profile).NotTo(BeNil(), "UserAssignedIdentitiesProfile should be populated after creating customer resources")
	autoNodeIdentityID := profile.DataPlaneOperators["autonode"]
	Expect(autoNodeIdentityID).NotTo(BeNil(), "DataPlaneOperators should contain an \"autonode\" identity")
	Expect(*autoNodeIdentityID).NotTo(BeEmpty(), "autonode data-plane identity resource ID should not be empty")
}

func validateAutoNodeEnabled20270201(ctx context.Context, client *hcpsdk20270201.HcpOpenShiftClustersClient, resourceGroupName, clusterName string, subscriptionID func(context.Context) (string, error)) {
	GinkgoHelper()
	actualHCPCluster, err := client.Get(ctx, resourceGroupName, clusterName, nil)
	Expect(err).NotTo(HaveOccurred(), "failed to get HCP cluster %s", clusterName)
	Expect(actualHCPCluster.Properties).NotTo(BeNil(), "AutoNode cluster response properties must be present")
	if actualHCPCluster.Properties.AutoNode == nil || actualHCPCluster.Properties.AutoNode.Mode == nil || *actualHCPCluster.Properties.AutoNode.Mode != hcpsdk20270201.AutoNodeModeEnabled {
		subscriptionID, subErr := subscriptionID(ctx)
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
}

func resolveAutoNodeWorkerVersion(ctx context.Context, adminRESTConfig *rest.Config, clusterName string) string {
	GinkgoHelper()
	configClient, err := configv1client.NewForConfig(adminRESTConfig)
	Expect(err).NotTo(HaveOccurred(), "failed to create OpenShift config client for cluster %s", clusterName)
	var parseableVersions []string
	Eventually(ctx, func(g Gomega, ctx context.Context) {
		clusterVersion, err := configClient.ClusterVersions().Get(ctx, "version", metav1.GetOptions{})
		g.Expect(err).NotTo(HaveOccurred(), "failed to get ClusterVersion for cluster %s", clusterName)
		parseableVersions = nil
		for _, h := range clusterVersion.Status.History {
			if _, err := semver.ParseTolerant(h.Version); err != nil {
				continue
			}
			parseableVersions = append(parseableVersions, h.Version)
			if h.State == configv1.CompletedUpdate {
				break
			}
		}
		g.Expect(parseableVersions).NotTo(BeEmpty(), "no parseable node pool install version found in ClusterVersion history for cluster %s", clusterName)
	}, time.Minute, verifiers.DefaultPollInterval).Should(Succeed(), "failed to resolve a node pool install version from ClusterVersion history for cluster %s", clusterName)
	sort.Slice(parseableVersions, func(i, j int) bool {
		vi, _ := semver.ParseTolerant(parseableVersions[i])
		vj, _ := semver.ParseTolerant(parseableVersions[j])
		return vi.LT(vj)
	})
	return parseableVersions[0]
}

func provisionKarpenterNode(ctx context.Context, adminRESTConfig *rest.Config, clusterName string, options karpenterRunOptions) karpenterRun {
	GinkgoHelper()
	By("verifying the guest Karpenter CRDs are installed")
	// Hard precondition. The standalone karpenter-operator installs these CRDs at
	// startup. A missing CRD shows that the container is broken. It is not a timing
	// state, and the test does not build a fallback.
	err := verifiers.VerifyKarpenterCRDsInstalled(5*time.Minute).Verify(ctx, adminRESTConfig)
	Expect(err).NotTo(HaveOccurred(), "guest Karpenter CRDs are not installed on cluster %s", clusterName)

	const karpenterResourceName = "autonode-e2e"
	karpenterInstanceTypes := []string{"Standard_D4s_v3", "Standard_D4s_v5"}
	err = framework.ValidateKarpenterInstanceTypes(karpenterInstanceTypes)
	Expect(err).NotTo(HaveOccurred(), "invalid instance types for Karpenter NodePool %q", karpenterResourceName)

	By("creating a test-owned OpenShiftAzureNodeClass for Karpenter")
	err = framework.ApplyOpenShiftAzureNodeClass(ctx, adminRESTConfig, karpenterResourceName, options.nodeClassOptions...)
	Expect(err).NotTo(HaveOccurred(), "failed to create OpenShiftAzureNodeClass %q", karpenterResourceName)
	DeferCleanup(func(ctx context.Context) {
		_ = framework.DeleteOpenShiftAzureNodeClassAndNodePool(ctx, adminRESTConfig, karpenterResourceName)
	})

	By("waiting for the OpenShiftAzureNodeClass to become ready")
	err = verifiers.VerifyOpenShiftAzureNodeClassReady(karpenterResourceName, 5*time.Minute).Verify(ctx, adminRESTConfig)
	Expect(err).NotTo(HaveOccurred(), "OpenShiftAzureNodeClass %q did not become ready", karpenterResourceName)

	By("creating a Karpenter NodePool that references the ready NodeClass")
	var nodePoolOptions []framework.KarpenterNodePoolOption
	if len(options.startupTaints) > 0 {
		nodePoolOptions = append(nodePoolOptions, framework.WithKarpenterStartupTaints(options.startupTaints...))
	}
	err = framework.ApplyKarpenterNodePool(ctx, adminRESTConfig, karpenterResourceName, karpenterInstanceTypes, nodePoolOptions...)
	Expect(err).NotTo(HaveOccurred(), "failed to create Karpenter NodePool %q", karpenterResourceName)

	// Deploy the workload.
	nodeSelector := map[string]string{
		"autonode":                       "true",
		framework.KarpenterNodePoolLabel: karpenterResourceName,
	}
	const workloadNamespace = "autonode-e2e-workload"
	const workloadName = "autonode-e2e-pending"
	run := karpenterRun{resourceName: karpenterResourceName, workloadNamespace: workloadNamespace, workloadName: workloadName, nodeSelector: nodeSelector}

	replicas := options.replicas
	if replicas == 0 {
		replicas = 1
	}
	workloadOptions := []framework.PendingWorkloadOption{}
	if replicas > 1 {
		// One replica per node. Karpenter then provisions one node per replica,
		// and the test gets several Karpenter nodes to compare.
		workloadOptions = append(workloadOptions, framework.WithPodAntiAffinityOnHostname())
	}

	By("deploying a workload matched to the Karpenter NodePool, initially at 0 replicas")
	err = framework.DeployPendingWorkload(ctx, adminRESTConfig, workloadNamespace, workloadName, nodeSelector, workloadOptions...)
	Expect(err).NotTo(HaveOccurred(), "failed to deploy pending workload %q", workloadName)
	DeferCleanup(func(ctx context.Context) {
		_ = framework.DeleteNamespace(ctx, adminRESTConfig, workloadNamespace)
	})

	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	Expect(err).NotTo(HaveOccurred(), "failed to create kubernetes client for cluster %s", clusterName)

	By("scaling the workload to create real scheduling pressure")
	err = framework.ScaleDeployment(ctx, adminRESTConfig, workloadNamespace, workloadName, replicas)
	Expect(err).NotTo(HaveOccurred(), "failed to scale workload %q to %d replicas", workloadName, replicas)

	By("verifying the workload's pod is Pending, proving the scheduling pressure is real")
	Eventually(func(g Gomega) {
		pods, err := kubeClient.CoreV1().Pods(workloadNamespace).List(ctx, metav1.ListOptions{LabelSelector: "app=" + workloadName})
		g.Expect(err).NotTo(HaveOccurred(), "failed to list pods for workload %q", workloadName)
		g.Expect(pods.Items).To(HaveLen(int(replicas)), "expected %d pods for workload %q", replicas, workloadName)
		for i := range pods.Items {
			g.Expect(pods.Items[i].Status.Phase).To(Equal(corev1.PodPending), "expected workload pod %q to be Pending: no node satisfying the NodePool's requirements should exist yet", pods.Items[i].Name)
		}
	}, time.Minute, 5*time.Second).Should(Succeed(), "workload pods never reached Pending phase")

	By("waiting for Karpenter to provision a real Azure node, approving its CSRs as a workaround for the missing Azure auto-approver")
	csrApproverCtx, stopCSRApprover := context.WithCancel(ctx)
	var approvedCSRCount int
	var csrApproverErr error
	csrApproverDone := make(chan struct{})
	go func() {
		defer close(csrApproverDone)
		approvedCSRCount, csrApproverErr = framework.RunKarpenterNodeCSRApprover(csrApproverCtx, adminRESTConfig, 10*time.Second)
	}()

	// A Karpenter-provisioned Azure VM needs about five minutes to initialize and
	// start registration. The cluster creation above adds more time. Budget
	// generously.
	err = verifiers.VerifyKarpenterNodeProvisioned(karpenterResourceName, 25*time.Minute, options.provisioningChecks).Verify(ctx, adminRESTConfig)
	stopCSRApprover()
	<-csrApproverDone
	GinkgoLogr.Info("Karpenter node CSR approver finished", "approved", approvedCSRCount)
	Expect(csrApproverErr).NotTo(HaveOccurred(), "background Karpenter node CSR approver failed")
	Expect(err).NotTo(HaveOccurred(), "Karpenter never provisioned a Ready, schedulable, fully-linked node for NodePool %q", karpenterResourceName)

	By("verifying every workload pod reaches Running on a Karpenter-provisioned node")
	// Karpenter plans all NodeClaims in one scheduling pass and launches them
	// concurrently, so the replicas finish at about the same time. The budget
	// grows with the replica count only as a safety margin.
	Eventually(func(g Gomega) {
		pods, err := kubeClient.CoreV1().Pods(workloadNamespace).List(ctx, metav1.ListOptions{LabelSelector: "app=" + workloadName})
		g.Expect(err).NotTo(HaveOccurred(), "failed to list pods for workload %q", workloadName)
		g.Expect(pods.Items).To(HaveLen(int(replicas)), "expected %d pods for workload %q", replicas, workloadName)
		nodeNames := make([]string, 0, len(pods.Items))
		for i := range pods.Items {
			g.Expect(pods.Items[i].Status.Phase).To(Equal(corev1.PodRunning), "expected workload pod %q to reach Running once Karpenter provisioned a node", pods.Items[i].Name)
			nodeNames = append(nodeNames, pods.Items[i].Spec.NodeName)
		}
		slices.Sort(nodeNames)
		run.nodeNames = slices.Compact(nodeNames)
		g.Expect(run.nodeNames).To(HaveLen(int(replicas)), "expected %d distinct nodes for %d replicas, observed %v", replicas, replicas, run.nodeNames)
	}, time.Duration(replicas)*5*time.Minute, 10*time.Second).Should(Succeed(), "workload pods never reached Running on %d distinct Karpenter-provisioned nodes", replicas)

	By("verifying a simple web app can run on the Karpenter-provisioned node")
	err = verifiers.VerifySimpleWebApp(nodeSelector).Verify(ctx, adminRESTConfig)
	Expect(err).NotTo(HaveOccurred(), "failed to verify simple web app runs on cluster %q", clusterName)
	return run
}

func deprovisionKarpenterNode(ctx context.Context, adminRESTConfig *rest.Config, run karpenterRun) {
	GinkgoHelper()
	By("scaling the workload back down and deleting the NodePool/OpenShiftAzureNodeClass, verifying Karpenter actually deprovisions the node")
	err := framework.ScaleDeployment(ctx, adminRESTConfig, run.workloadNamespace, run.workloadName, 0)
	Expect(err).NotTo(HaveOccurred(), "failed to scale workload %q back to 0 replicas", run.workloadName)
	err = framework.DeleteOpenShiftAzureNodeClassAndNodePool(ctx, adminRESTConfig, run.resourceName)
	Expect(err).NotTo(HaveOccurred(), "failed to delete OpenShiftAzureNodeClass/NodePool %q", run.resourceName)
	// 10m budget. In a real run, drain and deprovision took about 1m15s once the
	// cluster-infra pods had a regular worker pool to use (see the worker pool step
	// above). 10m gives a wide margin and still exposes a real hang.
	//
	// Consolidation is not the bottleneck. Karpenter sets consolidateAfter to 0s
	// when the NodePool does not set spec.disruption. This is confirmed against
	// karpenter.sh_nodepools.yaml.
	//
	// Without the regular worker pool, draining this node can block on the
	// router-default PodDisruptionBudget, because that is the only other place for
	// the pods to go. Observed events:
	//   Normal  DisruptionBlocked  4m57s  karpenter  Pdb prevents pod evictions (PodDisruptionBudget=[openshift-ingress/router-default])
	//   Normal  DisruptionBlocked  57s (x2 over 2m57s)  karpenter  Node is deleting or marked for deletion
	err = verifiers.VerifyKarpenterDeprovisioned(run.resourceName, 10*time.Minute).Verify(ctx, adminRESTConfig)
	Expect(err).NotTo(HaveOccurred(), "Karpenter did not remove Nodes, NodeClaims, and both NodeClasses for NodePool %q; resources may remain", run.resourceName)

	By("deleting the workload namespace")
	err = framework.DeleteNamespace(ctx, adminRESTConfig, run.workloadNamespace)
	Expect(err).NotTo(HaveOccurred(), "failed to delete workload namespace %q", run.workloadNamespace)
}
