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
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	hcpsdk20251223preview "github.com/Azure/ARO-HCP/test/sdk/v20251223preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

// This test creates a default (Public visibility) cluster and verifies both
// KAS access paths that define the PublicAndPrivate topology:
//   - Public path: KAS is reachable from outside the VNet via shared ingress
//   - Private/Swift path: worker nodes reach KAS via Swift networking, proven
//     by nodes reporting Ready and the API server successfully fetching pod logs
var _ = Describe("Customer", func() {
	It("should create a default cluster and verify KAS is accessible via both public shared ingress and private Swift networking from worker nodes",
		labels.RequireNothing,
		labels.High,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.CreateCluster,
		labels.MIContainers(1),
		func(ctx context.Context) {
			const (
				customerClusterName  = "pub-priv-kas"
				customerNodePoolName = "np-1"
			)

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "pub-priv-kas", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for PublicAndPrivate KAS test")

			By("creating cluster parameters with default (Public) API visibility")
			clusterParams := framework.NewDefaultClusterParams20251223()
			clusterParams.ClusterName = customerClusterName
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.DisableSwift = false

			// Swift networking (worker→KAS private path) requires OCP >= 4.22
			openshiftVersionID, err := framework.PickAtLeastOpenshiftVersionId(clusterParams.OpenshiftVersionId, "4.22")
			// If the default is nightly which isn't >= 4.22, skip this test with an explanation.
			// Log the reason before calling Skip: Skip() stores its message internally and the
			// Ginkgo reporter only emits file:line in verbose mode, never the message text itself.
			if framework.IsIncompatibleNightlyVersionError(err) {
				skipMsg := fmt.Sprintf("Swift networking requires OCP >= 4.22, but default version %q does not satisfy it: %v", clusterParams.OpenshiftVersionId, err)
				GinkgoLogr.Info(skipMsg)
				Skip(skipMsg)
			}
			Expect(err).NotTo(HaveOccurred(), "failed to select OpenShift version >= 4.22 for public-and-private KAS test (default version: %q)", clusterParams.OpenshiftVersionId)
			clusterParams.OpenshiftVersionId = openshiftVersionID

			By("creating customer resources (infrastructure and managed identities)")
			clusterParams, err = tc.CreateClusterCustomerResources20251223(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for PublicAndPrivate KAS cluster")

			By("creating the HCP cluster with default (Public) visibility")
			err = tc.CreateHCPClusterFromParam20251223(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %q", customerClusterName)

			By("verifying cluster API visibility is Public via ARM GET")
			hcpClient := tc.Get20251223ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
			cluster, err := hcpClient.Get(ctx, *resourceGroup.Name, customerClusterName, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to get cluster %q to verify API visibility", customerClusterName)
			Expect(cluster.Properties).ToNot(BeNil(), "cluster %q Properties was nil", customerClusterName)
			Expect(cluster.Properties.API).ToNot(BeNil(), "cluster %q Properties.API was nil", customerClusterName)
			Expect(cluster.Properties.API.Visibility).ToNot(BeNil(), "cluster %q Properties.API.Visibility was nil", customerClusterName)
			Expect(*cluster.Properties.API.Visibility).To(Equal(hcpsdk20251223preview.VisibilityPublic),
				"cluster %q API visibility should be Public", customerClusterName)
			Expect(cluster.Properties.API.URL).ToNot(BeNil(), "cluster %q Properties.API.URL was nil", customerClusterName)
			apiURL := *cluster.Properties.API.URL
			GinkgoLogr.Info("Cluster created with Public visibility", "clusterName", customerClusterName, "apiURL", apiURL)

			By("creating the node pool")
			nodePoolParams := framework.NewDefaultNodePoolParams20251223()
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.Replicas = int32(2)

			err = tc.CreateNodePoolFromParam20251223(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams.ManagedResourceGroupName,
				customerClusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create node pool %q for PublicAndPrivate KAS cluster %q",
				customerNodePoolName, customerClusterName)

			By("getting admin credentials for the cluster")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20240610(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for cluster %q", customerClusterName)

			By("verifying KAS is reachable from outside the VNet via shared ingress (public path)")
			Eventually(func(g Gomega) {
				statusCode, err := framework.TestHTTPSConnectivityWithStatus(ctx, apiURL+"/healthz", 10*time.Second, true)
				g.Expect(err).NotTo(HaveOccurred(),
					"KAS should be reachable from outside the VNet via shared ingress, but got error: %v", err)
				g.Expect(statusCode).To(Equal(http.StatusOK),
					"KAS /healthz should return 200 OK, got %d", statusCode)
			}, 5*time.Minute, 15*time.Second).Should(Succeed(),
				"KAS public endpoint should be reachable from outside the VNet via shared ingress")
			GinkgoLogr.Info("Confirmed KAS is reachable from outside the VNet via shared ingress (public path)")

			By("verifying worker nodes are Ready (proves kubelet-to-KAS Swift path is functional)")
			Expect(verifiers.VerifyNodeCount(customerClusterName, 2).Verify(ctx, adminRESTConfig)).To(Succeed(),
				"expected 2 nodes for cluster %q", customerClusterName)
			Expect(verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)).To(Succeed(),
				"all nodes should be Ready, proving kubelet-to-KAS connectivity via Swift networking")
			GinkgoLogr.Info("All worker nodes are Ready, confirming Swift networking path to KAS is functional")

			By("verifying bidirectional Swift connectivity by fetching router-default pod logs")
			err = verifiers.VerifyDeploymentLogsReachable("openshift-ingress", "router-default", "router", 10*time.Minute).Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "fetching router-default logs should succeed, proving KAS-to-kubelet Swift path")
			GinkgoLogr.Info("Bidirectional Swift connectivity confirmed via pod log retrieval")

			By("verifying public ingress is reachable from outside the VNet (console URL)")
			var consoleURL string
			Eventually(func(g Gomega) {
				resp, err := hcpClient.Get(ctx, *resourceGroup.Name, customerClusterName, nil)
				g.Expect(err).NotTo(HaveOccurred(), "failed to get cluster %q for console URL", customerClusterName)
				g.Expect(resp.Properties).ToNot(BeNil(), "cluster %q Properties was nil", customerClusterName)
				g.Expect(resp.Properties.Console).ToNot(BeNil(), "cluster %q Properties.Console was nil", customerClusterName)
				g.Expect(resp.Properties.Console.URL).ToNot(BeNil(), "cluster %q Properties.Console.URL was nil", customerClusterName)
				consoleURL = *resp.Properties.Console.URL
			}, 15*time.Minute, 30*time.Second).Should(Succeed(), "console URL should become available for cluster %q", customerClusterName)
			GinkgoLogr.Info("Console URL available", "url", consoleURL)

			// DEBUG: one-time console diagnostics snapshot - revert after investigation complete
			By("DEBUG: collecting one-time console diagnostics snapshot")
			diagKubeClient, diagKubeErr := kubernetes.NewForConfig(adminRESTConfig)
			dynClient, dynClientErr := dynamic.NewForConfig(adminRESTConfig)
			if diagKubeErr != nil || dynClientErr != nil {
				GinkgoLogr.Info("DEBUG: failed to create diagnostic clients", "kubeErr", diagKubeErr, "dynErr", dynClientErr)
			} else {
				// DEBUG: console ClusterOperator conditions
				coGVR := schema.GroupVersionResource{Group: "config.openshift.io", Version: "v1", Resource: "clusteroperators"}
				if co, coErr := dynClient.Resource(coGVR).Get(ctx, "console", metav1.GetOptions{}); coErr != nil {
					GinkgoLogr.Info("DEBUG: console ClusterOperator not found", "error", coErr)
				} else {
					GinkgoLogr.Info("DEBUG: console ClusterOperator status", "status", co.Object["status"])
				}

				// DEBUG: check console-oauth-config ConfigMap existence and content in openshift-config
				if cm, cmErr := diagKubeClient.CoreV1().ConfigMaps("openshift-config").Get(ctx, "console-oauth-config", metav1.GetOptions{}); cmErr != nil {
					GinkgoLogr.Info("DEBUG: console-oauth-config ConfigMap missing in openshift-config", "error", cmErr)
				} else {
					GinkgoLogr.Info("DEBUG: console-oauth-config ConfigMap exists in openshift-config", "data", cm.Data)
				}

				// DEBUG: check console OAuthClient existence and content
				oauthClientGVR := schema.GroupVersionResource{Group: "oauth.openshift.io", Version: "v1", Resource: "oauthclients"}
				if oc, ocErr := dynClient.Resource(oauthClientGVR).Get(ctx, "console", metav1.GetOptions{}); ocErr != nil {
					GinkgoLogr.Info("DEBUG: console OAuthClient not found", "error", ocErr)
				} else {
					GinkgoLogr.Info("DEBUG: console OAuthClient exists", "redirectURIs", oc.Object["redirectURIs"], "grantMethod", oc.Object["grantMethod"])
				}

				// DEBUG: Deployments in openshift-console
				if deps, depErr := diagKubeClient.AppsV1().Deployments("openshift-console").List(ctx, metav1.ListOptions{}); depErr != nil {
					GinkgoLogr.Info("DEBUG: failed to list Deployments in openshift-console", "error", depErr)
				} else {
					for _, d := range deps.Items {
						GinkgoLogr.Info("DEBUG: openshift-console Deployment", "name", d.Name, "readyReplicas", d.Status.ReadyReplicas, "replicas", d.Status.Replicas)
					}
				}

				// DEBUG: Services in openshift-console
				if svcs, svcErr := diagKubeClient.CoreV1().Services("openshift-console").List(ctx, metav1.ListOptions{}); svcErr != nil {
					GinkgoLogr.Info("DEBUG: failed to list Services in openshift-console", "error", svcErr)
				} else {
					for _, s := range svcs.Items {
						GinkgoLogr.Info("DEBUG: openshift-console Service", "name", s.Name, "clusterIP", s.Spec.ClusterIP)
					}
				}

				// DEBUG: Routes in openshift-console
				routeGVR := schema.GroupVersionResource{Group: "route.openshift.io", Version: "v1", Resource: "routes"}
				if routes, routeErr := dynClient.Resource(routeGVR).Namespace("openshift-console").List(ctx, metav1.ListOptions{}); routeErr != nil {
					GinkgoLogr.Info("DEBUG: failed to list Routes in openshift-console", "error", routeErr)
				} else {
					for _, r := range routes.Items {
						spec, _ := r.Object["spec"].(map[string]interface{})
						GinkgoLogr.Info("DEBUG: openshift-console Route", "name", r.GetName(), "host", spec["host"])
					}
				}

				// DEBUG: EndpointSlices in openshift-console
				if eps, epsErr := diagKubeClient.DiscoveryV1().EndpointSlices("openshift-console").List(ctx, metav1.ListOptions{}); epsErr != nil {
					GinkgoLogr.Info("DEBUG: failed to list EndpointSlices in openshift-console", "error", epsErr)
				} else {
					for _, ep := range eps.Items {
						var ready, notReady int
						for _, e := range ep.Endpoints {
							if e.Conditions.Ready != nil && *e.Conditions.Ready {
								ready++
							} else {
								notReady++
							}
						}
						GinkgoLogr.Info("DEBUG: openshift-console EndpointSlice", "name", ep.Name, "ready", ready, "notReady", notReady)
					}
				}

				// DEBUG: Events in openshift-console
				if events, evErr := diagKubeClient.CoreV1().Events("openshift-console").List(ctx, metav1.ListOptions{}); evErr != nil {
					GinkgoLogr.Info("DEBUG: failed to list Events in openshift-console", "error", evErr)
				} else {
					for _, ev := range events.Items {
						GinkgoLogr.Info("DEBUG: openshift-console Event", "reason", ev.Reason, "type", ev.Type, "message", ev.Message, "count", ev.Count)
					}
				}

				// DEBUG: pods in openshift-console-operator
				if pods, podErr := diagKubeClient.CoreV1().Pods("openshift-console-operator").List(ctx, metav1.ListOptions{}); podErr != nil {
					GinkgoLogr.Info("DEBUG: failed to list pods in openshift-console-operator", "error", podErr)
				} else {
					for _, p := range pods.Items {
						GinkgoLogr.Info("DEBUG: openshift-console-operator pod", "name", p.Name, "phase", p.Status.Phase)
					}
				}

				// DEBUG: KAS /healthz returned 200 above, so shared ingress is functional; console 503 is console-specific
				GinkgoLogr.Info("DEBUG: ingress note — KAS /healthz returned 200 OK, shared ingress is functional; console 503 is console-specific")
			}

			// DEBUG: revert after console URL debugging is done
			kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create kubernetes client for console pod debug")

			var lastConsoleStatus int
			Eventually(func(g Gomega) {
				// DEBUG: dump openshift-console pod readiness on every poll
				if pods, podErr := kubeClient.CoreV1().Pods("openshift-console").List(ctx, metav1.ListOptions{}); podErr != nil {
					GinkgoLogr.Info("Console pod debug: failed to list pods", "error", podErr)
				} else {
					for _, p := range pods.Items {
						ready := false
						for _, c := range p.Status.ContainerStatuses {
							if c.Name == "console" {
								ready = c.Ready
							}
						}
						GinkgoLogr.Info("Console pod debug", "pod", p.Name, "phase", p.Status.Phase, "ready", ready)
					}
				}

				statusCode, err := framework.TestHTTPSConnectivityWithStatus(ctx, consoleURL, 10*time.Second, true)
				// DEBUG: log every status change
				if err != nil {
					GinkgoLogr.Info("Console reachability check", "url", consoleURL, "error", err)
				} else if statusCode != lastConsoleStatus {
					GinkgoLogr.Info("Console reachability check", "url", consoleURL, "statusCode", statusCode)
					lastConsoleStatus = statusCode
				}
				g.Expect(err).NotTo(HaveOccurred(),
					"public ingress (console) should be reachable from outside the VNet, but got error: %v", err)
				g.Expect(statusCode).To(BeNumerically("<", http.StatusBadRequest),
					"console should return a successful response or redirect, got %d", statusCode)
			}, 45*time.Minute, 15*time.Second).Should(Succeed(), // DEBUG: extended to 45m for diagnosis
				"public ingress should be reachable from outside the VNet")
			GinkgoLogr.Info("Public ingress reachable from outside the VNet, confirming shared ingress is operational")
		},
	)
})
