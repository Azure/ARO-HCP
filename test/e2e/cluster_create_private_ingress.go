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
	"crypto/tls"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	operatorv1 "github.com/openshift/api/operator/v1"

	hcpsdk20260630preview "github.com/Azure/ARO-HCP/test/sdk/v20260630preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

// This test creates a cluster with private ingress using the v2026-06-30-preview
// API and verifies the ingress is internal. It also serves as the basic
// v2026-06-30-preview API version smoke test — verifying cluster creation,
// credentials, and cluster health — to avoid creating multiple clusters in CI.
var _ = Describe("Customer", func() {
	It("should create a cluster with private ingress using v20260630preview and verify the ingress is internal",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.CreateCluster,
		func(ctx context.Context) {
			const (
				customerClusterName  = "private-ingress"
				customerNodePoolName = "np-1"
				apiVersion           = "2026-06-30-preview"
			)

			tc := framework.NewTestContext()

			By("checking API version availability")
			if !framework.IsDevelopmentEnvironment() {
				resourcesFactory, err := tc.GetARMResourcesClientFactory(ctx)
				Expect(err).NotTo(HaveOccurred(), "failed to get ARM resources client factory")

				providersClient := resourcesFactory.NewProvidersClient()
				provider, err := providersClient.Get(ctx, "Microsoft.RedHatOpenShift", nil)
				Expect(err).NotTo(HaveOccurred(), "failed to get Microsoft.RedHatOpenShift resource provider")

				available := slices.ContainsFunc(provider.ResourceTypes, func(rt *armresources.ProviderResourceType) bool {
					if rt.ResourceType == nil || !strings.EqualFold(*rt.ResourceType, "hcpOpenShiftClusters") {
						return false
					}
					return slices.ContainsFunc(rt.APIVersions, func(v *string) bool {
						return v != nil && strings.EqualFold(*v, apiVersion)
					})
				})

				if !available {
					if time.Now().After(framework.Must(time.Parse(time.RFC3339, "2026-07-31T00:00:00Z"))) {
						Fail(fmt.Sprintf("API version %s should be available for Microsoft.RedHatOpenShift/hcpOpenShiftClusters by 2026-07-31 00:00 UTC", apiVersion))
					}
					Skip(fmt.Sprintf("API version %s is not available for Microsoft.RedHatOpenShift/hcpOpenShiftClusters in this environment", apiVersion))
				}
				GinkgoLogr.Info("API version available", "version", apiVersion)
			}

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "private-ingress", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for private ingress test")

			By("creating cluster parameters with private ingress")
			clusterParams := framework.NewDefaultClusterParams20260630()
			clusterParams.ClusterName = customerClusterName
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.IngressType = "Private"

			By("creating customer resources (infrastructure and managed identities)")
			clusterParams, err = tc.CreateClusterCustomerResources20260630(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for private ingress cluster")

			By("deploying test VM in the same VNet for connectivity verification")
			sshPublicKey, _, err := framework.GenerateSSHKeyPair()
			Expect(err).NotTo(HaveOccurred(), "failed to generate SSH key pair for test VM")

			vmName := fmt.Sprintf("%s-test-vm", customerClusterName)
			vmSize, err := tc.SelectVMSize(ctx, framework.JumpboxVMSizeSelector())
			Expect(err).NotTo(HaveOccurred(), "failed to resolve a jumpbox VM size for private ingress test")

			var deployErr error
			for attempt := 0; attempt < 3; attempt++ {
				_, deployErr = tc.CreateBicepTemplateAndWait(ctx,
					framework.WithTemplateFromFS(TestArtifactsFS, "test-artifacts/generated-test-artifacts/modules/test-vm.json"),
					framework.WithDeploymentName("test-vm"),
					framework.WithScope(framework.BicepDeploymentScopeResourceGroup),
					framework.WithClusterResourceGroup(*resourceGroup.Name),
					framework.WithParameters(map[string]any{
						"vmName":       vmName,
						"vnetName":     clusterParams.VnetName,
						"subnetName":   clusterParams.SubnetName,
						"sshPublicKey": sshPublicKey,
						"vmSize":       vmSize,
					}),
					framework.WithTimeout(30*time.Minute),
				)
				if deployErr == nil || strings.Contains(deployErr.Error(), "SkuNotAvailable") {
					break
				}
				time.Sleep(20 * time.Second)
			}
			Expect(deployErr).NotTo(HaveOccurred(), "failed to deploy test VM for private ingress verification")

			By("creating the HCP cluster with private ingress via v20260630preview")
			err = tc.CreateHCPClusterFromParam20260630(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %q with private ingress", customerClusterName)

			By("verifying cluster was created with private ingress type via ARM GET")
			clientFactory := tc.Get20260630ClientFactoryOrDie(ctx)
			cluster, err := clientFactory.NewHcpOpenShiftClustersClient().Get(
				ctx,
				*resourceGroup.Name,
				customerClusterName,
				nil,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get cluster %q to verify private ingress", customerClusterName)
			Expect(cluster.Properties).ToNot(BeNil(), "cluster %q Properties was nil", customerClusterName)
			Expect(cluster.Properties.Ingress).ToNot(BeNil(), "cluster %q Properties.Ingress was nil", customerClusterName)
			Expect(cluster.Properties.Ingress.Type).ToNot(BeNil(), "cluster %q Properties.Ingress.Type was nil", customerClusterName)
			Expect(*cluster.Properties.Ingress.Type).To(Equal(hcpsdk20260630preview.IngressTypePrivate),
				"cluster %q ingress type should be Private", customerClusterName)
			GinkgoLogr.Info("Cluster created with private ingress", "clusterName", customerClusterName)

			By("creating the node pool")
			nodePoolParams := framework.NewDefaultNodePoolParams20260630()
			nodePoolParams.ClusterName = customerClusterName
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.Replicas = int32(2)

			err = tc.CreateNodePoolFromParam20260630(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams.ManagedResourceGroupName,
				customerClusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create node pool %q for private ingress cluster %q",
				customerNodePoolName, customerClusterName)

			By("getting admin credentials for the cluster")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20240610(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for private ingress cluster %q", customerClusterName)

			By("ensuring the cluster is viable (basic v2026 API verification)")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig,
				verifiers.VerifyIngressControllerScope(operatorv1.InternalLoadBalancer),
			)
			Expect(err).NotTo(HaveOccurred(), "failed to verify cluster health or IngressController scope for cluster %q", customerClusterName)
			GinkgoLogr.Info("Cluster health and IngressController scope verified")

			By("deploying a sample web app to verify ingress connectivity")
			dynamicClient, err := dynamic.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create dynamic client from admin REST config")

			appNamespace, appRouteHost, err := deploySampleApp(ctx, dynamicClient)
			Expect(err).NotTo(HaveOccurred(), "failed to deploy sample web app for ingress connectivity test")
			defer cleanupSampleApp(ctx, dynamicClient, appNamespace)

			appURL := "https://" + appRouteHost
			GinkgoLogr.Info("Sample app deployed", "namespace", appNamespace, "url", appURL)

			By("verifying ingress is reachable from VM inside the VNet")
			curlCmd := fmt.Sprintf("curl -k -s -o /dev/null -w '%%{http_code}' --connect-timeout 10 %s", appURL)
			var previousOutput string
			Eventually(func(g Gomega) {
				output, runErr := framework.RunVMCommand(ctx, tc, *resourceGroup.Name, vmName, curlCmd, 2*time.Minute)
				g.Expect(runErr).NotTo(HaveOccurred(), "RunVMCommand failed for ingress connectivity test")

				httpCode := strings.TrimSpace(output)
				if httpCode != previousOutput {
					GinkgoLogr.Info("VM ingress connectivity check", "httpCode", httpCode)
					previousOutput = httpCode
				}
				g.Expect(httpCode).NotTo(Equal("000"),
					"should receive HTTP response from internal LB via VM, got curl error code 000")
			}, 10*time.Minute, 15*time.Second).Should(Succeed())

			By("verifying ingress is NOT reachable from outside the VNet")
			err = testIngressConnectivity(ctx, appURL, 10*time.Second)
			Expect(err).To(HaveOccurred(),
				"private ingress should not be reachable from outside the VNet, but connection succeeded")
			GinkgoLogr.Info("Confirmed ingress is not reachable from outside the VNet")
		},
	)
})

// deploySampleApp creates a simple web app (agnhost serve-hostname) with a
// Service and Route in a new namespace. Returns the namespace name and the
// route host, or an error.
func deploySampleApp(ctx context.Context, dynamicClient dynamic.Interface) (string, string, error) {
	// Create namespace
	nsObj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Namespace",
			"metadata": map[string]interface{}{
				"generateName": "e2e-private-ingress-",
			},
		},
	}
	ns, err := dynamicClient.Resource(schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}).
		Create(ctx, nsObj, metav1.CreateOptions{})
	if err != nil {
		return "", "", fmt.Errorf("failed to create namespace: %w", err)
	}
	nsName := ns.GetName()

	// Create deployment
	deployment := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]interface{}{
				"name": "agnhost-server",
			},
			"spec": map[string]interface{}{
				"replicas": int64(1),
				"selector": map[string]interface{}{
					"matchLabels": map[string]interface{}{"app": "agnhost"},
				},
				"template": map[string]interface{}{
					"metadata": map[string]interface{}{
						"labels": map[string]interface{}{"app": "agnhost"},
					},
					"spec": map[string]interface{}{
						"containers": []interface{}{
							map[string]interface{}{
								"name":  "agnhost",
								"image": "registry.k8s.io/e2e-test-images/agnhost:2.39",
								"args":  []interface{}{"serve-hostname", "--port=8080"},
								"ports": []interface{}{
									map[string]interface{}{
										"name":          "http",
										"containerPort": int64(8080),
										"protocol":      "TCP",
									},
								},
							},
						},
					},
				},
			},
		},
	}
	_, err = dynamicClient.Resource(schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}).
		Namespace(nsName).Create(ctx, deployment, metav1.CreateOptions{})
	if err != nil {
		return nsName, "", fmt.Errorf("failed to create deployment: %w", err)
	}

	// Create service
	service := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Service",
			"metadata": map[string]interface{}{
				"name": "agnhost",
			},
			"spec": map[string]interface{}{
				"ports": []interface{}{
					map[string]interface{}{
						"name":       "http",
						"port":       int64(80),
						"targetPort": int64(8080),
						"protocol":   "TCP",
					},
				},
				"selector": map[string]interface{}{"app": "agnhost"},
				"type":     "ClusterIP",
			},
		},
	}
	_, err = dynamicClient.Resource(schema.GroupVersionResource{Version: "v1", Resource: "services"}).
		Namespace(nsName).Create(ctx, service, metav1.CreateOptions{})
	if err != nil {
		return nsName, "", fmt.Errorf("failed to create service: %w", err)
	}

	// Create route
	route := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "route.openshift.io/v1",
			"kind":       "Route",
			"metadata": map[string]interface{}{
				"name": "agnhost",
			},
			"spec": map[string]interface{}{
				"port": map[string]interface{}{
					"targetPort": "http",
				},
				"tls": map[string]interface{}{
					"termination": "edge",
				},
				"to": map[string]interface{}{
					"kind":   "Service",
					"name":   "agnhost",
					"weight": int64(100),
				},
			},
		},
	}
	routeGVR := schema.GroupVersionResource{Group: "route.openshift.io", Version: "v1", Resource: "routes"}
	_, err = dynamicClient.Resource(routeGVR).Namespace(nsName).Create(ctx, route, metav1.CreateOptions{})
	if err != nil {
		return nsName, "", fmt.Errorf("failed to create route: %w", err)
	}

	// Wait for route to get a host
	var host string
	pollCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	for {
		r, err := dynamicClient.Resource(routeGVR).Namespace(nsName).Get(pollCtx, "agnhost", metav1.GetOptions{})
		if err == nil {
			h, _, _ := unstructured.NestedString(r.Object, "spec", "host")
			if h != "" {
				host = h
				break
			}
		}
		select {
		case <-pollCtx.Done():
			return nsName, "", fmt.Errorf("timed out waiting for route host to be assigned")
		case <-time.After(5 * time.Second):
		}
	}

	return nsName, host, nil
}

// cleanupSampleApp removes the namespace created by deploySampleApp.
func cleanupSampleApp(ctx context.Context, dynamicClient dynamic.Interface, namespace string) {
	if namespace == "" {
		return
	}
	_ = dynamicClient.Resource(schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}).
		Delete(ctx, namespace, metav1.DeleteOptions{})
}

// testIngressConnectivity attempts an HTTPS connection to the given URL.
// Returns nil if the connection succeeds, or an error if it fails.
// Redirects are disabled to avoid false positives from OAuth redirects.
func testIngressConnectivity(ctx context.Context, url string, timeout time.Duration) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(timeoutCtx, "GET", url, nil)
	if err != nil {
		return err
	}

	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}