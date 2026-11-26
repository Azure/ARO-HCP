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

package verifiers

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	sigyaml "sigs.k8s.io/yaml"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
)

const (
	ccmLBServiceName      = "e2e-ccm-lb-test"
	ccmLBServiceNamespace = "e2e-ccm-lb"
	ccmLBServicePort      = 80

	// ccmLBBackendDeploymentPath is the agnhost serve-hostname Deployment
	// (shared with VerifySimpleWebApp) used as the backend behind the CCM
	// load balancer. It replies to HTTP GET / with the serving pod's hostname.
	ccmLBBackendDeploymentPath = "artifacts/serving_app/deployment.yaml"
	ccmLBBackendLabelKey       = "app"
	ccmLBBackendLabelValue     = "agnhost"
	ccmLBBackendPort           = 8080

	// ccmLBHTTPRequestTimeout bounds each individual HTTP probe through the
	// load balancer so a single hung connection cannot stall the poll.
	ccmLBHTTPRequestTimeout = 10 * time.Second
)

// verifyCCMLoadBalancer deploys an agnhost serve-hostname backend in the guest
// cluster and exposes it through a Service of type LoadBalancer. It waits for
// the Cloud Controller Manager to assign an external IP (proving its Service
// controller reconciled the object using its managed identity), then fetches
// the backend's reply from the test runner via that IP — proving the CCM fully
// wired the data path (LB rule, health probe, backend pool membership of the
// worker NICs, and NSG allow rule), not merely allocated a public IP. It then
// confirms via a direct Azure API call that a corresponding public IP address
// exists in the managed resource group, deletes the Service, and confirms the
// public IP is released — exercising the identity's
// Microsoft.Network/publicIPAddresses/delete permission.
type verifyCCMLoadBalancer struct {
	networkFactory           *armnetwork.ClientFactory
	managedResourceGroupName string
	timeout                  time.Duration
}

func (v verifyCCMLoadBalancer) Name() string {
	return "VerifyCCMLoadBalancer"
}

func (v verifyCCMLoadBalancer) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	ns, err := kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{GenerateName: ccmLBServiceNamespace + "-"},
	}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create test namespace: %w", err)
	}
	nsName := ns.Name
	defer func() {
		delCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if delErr := kubeClient.CoreV1().Namespaces().Delete(delCtx, nsName, metav1.DeleteOptions{}); delErr != nil {
			ginkgo.GinkgoLogr.Error(delErr, "failed to delete CCM test namespace", "namespace", nsName)
		}
	}()

	if err := v.deployBackend(ctx, kubeClient, adminRESTConfig, nsName); err != nil {
		return err
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ccmLBServiceName,
			Namespace: nsName,
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeLoadBalancer,
			Ports: []corev1.ServicePort{{
				Port:       ccmLBServicePort,
				TargetPort: intstr.FromInt32(ccmLBBackendPort),
				Protocol:   corev1.ProtocolTCP,
			}},
			Selector: map[string]string{
				ccmLBBackendLabelKey: ccmLBBackendLabelValue,
			},
		},
	}

	ginkgo.GinkgoWriter.Printf("[%s] creating Service %s/%s of type LoadBalancer\n", v.Name(), nsName, ccmLBServiceName)
	if _, err := kubeClient.CoreV1().Services(nsName).Create(ctx, svc, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create LoadBalancer Service %s/%s: %w", nsName, ccmLBServiceName, err)
	}

	var assignedIP string
	ipErr := pollUntilReady(ctx, v.Name()+"-ip", v.timeout, DefaultPollInterval, adminRESTConfig, DefaultDiagnoseTimeout, nil,
		func(ctx context.Context) error {
			got, getErr := kubeClient.CoreV1().Services(nsName).Get(ctx, ccmLBServiceName, metav1.GetOptions{})
			if getErr != nil {
				return fmt.Errorf("failed to get Service %s/%s: %w", nsName, ccmLBServiceName, getErr)
			}
			if len(got.Status.LoadBalancer.Ingress) == 0 || got.Status.LoadBalancer.Ingress[0].IP == "" {
				return fmt.Errorf("service %s/%s has no external IP yet (status.loadBalancer.ingress is empty)", nsName, ccmLBServiceName)
			}
			assignedIP = got.Status.LoadBalancer.Ingress[0].IP
			ginkgo.GinkgoWriter.Printf("[%s] Service %s/%s received external IP %s\n", v.Name(), nsName, ccmLBServiceName, assignedIP)
			return nil
		},
	)
	if ipErr != nil {
		return fmt.Errorf("ccm did not assign an external IP to LoadBalancer Service %s/%s: %w", nsName, ccmLBServiceName, ipErr)
	}

	if err := v.verifyTrafficThroughLB(ctx, kubeClient, adminRESTConfig, nsName, assignedIP); err != nil {
		return err
	}

	ginkgo.GinkgoWriter.Printf("[%s] verifying public IP %s exists in managed resource group %s via Azure API\n", v.Name(), assignedIP, v.managedResourceGroupName)
	found, observedIPs, err := v.publicIPExistsInMRG(ctx, assignedIP)
	if err != nil {
		return fmt.Errorf("failed to verify public IP %s in managed resource group %s: %w", assignedIP, v.managedResourceGroupName, err)
	}
	if !found {
		return fmt.Errorf("public IP %s not found in managed resource group %s after LB creation; observed IPs: [%s]",
			assignedIP, v.managedResourceGroupName, strings.Join(observedIPs, ", "))
	}
	ginkgo.GinkgoWriter.Printf("[%s] confirmed public IP %s exists in managed resource group %s\n", v.Name(), assignedIP, v.managedResourceGroupName)

	ginkgo.GinkgoWriter.Printf("[%s] deleting Service %s/%s to verify CCM releases the public IP\n", v.Name(), nsName, ccmLBServiceName)
	if err := kubeClient.CoreV1().Services(nsName).Delete(ctx, ccmLBServiceName, metav1.DeleteOptions{}); err != nil {
		return fmt.Errorf("failed to delete LoadBalancer Service %s/%s: %w", nsName, ccmLBServiceName, err)
	}

	deleteErr := pollUntilReady(ctx, v.Name()+"-delete", v.timeout, DefaultPollInterval, adminRESTConfig, DefaultDiagnoseTimeout, nil,
		func(ctx context.Context) error {
			stillExists, _, checkErr := v.publicIPExistsInMRG(ctx, assignedIP)
			if checkErr != nil {
				return fmt.Errorf("failed to check public IP %s in managed resource group %s: %w", assignedIP, v.managedResourceGroupName, checkErr)
			}
			if stillExists {
				return fmt.Errorf("public IP %s still exists in managed resource group %s after Service deletion", assignedIP, v.managedResourceGroupName)
			}
			return nil
		},
	)
	if deleteErr != nil {
		return fmt.Errorf("ccm did not release public IP %s after LoadBalancer Service deletion: %w", assignedIP, deleteErr)
	}

	ginkgo.GinkgoWriter.Printf("[%s] confirmed public IP %s was released from managed resource group %s\n", v.Name(), assignedIP, v.managedResourceGroupName)
	return nil
}

// deployBackend creates the agnhost serve-hostname Deployment in nsName and
// waits for at least one of its pods to become Ready, so the load balancer has
// a healthy backend to forward to once it is provisioned.
func (v verifyCCMLoadBalancer) deployBackend(ctx context.Context, kubeClient kubernetes.Interface, adminRESTConfig *rest.Config, nsName string) error {
	deploymentYAML, err := staticFiles.ReadFile(ccmLBBackendDeploymentPath)
	if err != nil {
		return fmt.Errorf("failed to read backend deployment manifest %s: %w", ccmLBBackendDeploymentPath, err)
	}
	deployment := &appsv1.Deployment{}
	if err := sigyaml.Unmarshal(deploymentYAML, deployment); err != nil {
		return fmt.Errorf("failed to decode backend deployment manifest %s: %w", ccmLBBackendDeploymentPath, err)
	}
	if got := deployment.Spec.Template.Labels[ccmLBBackendLabelKey]; got != ccmLBBackendLabelValue {
		return fmt.Errorf("backend deployment manifest %s has pod label %s=%q, expected %q", ccmLBBackendDeploymentPath, ccmLBBackendLabelKey, got, ccmLBBackendLabelValue)
	}
	deployment.Namespace = nsName

	ginkgo.GinkgoWriter.Printf("[%s] creating backend Deployment %s/%s\n", v.Name(), nsName, deployment.Name)
	if _, err := kubeClient.AppsV1().Deployments(nsName).Create(ctx, deployment, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create backend Deployment %s/%s: %w", nsName, deployment.Name, err)
	}

	var readyPodNames []string
	podErr := pollUntilReady(ctx, v.Name()+"-backend", v.timeout, DefaultPollInterval, adminRESTConfig, DefaultDiagnoseTimeout, nil,
		func(ctx context.Context) error {
			ready, states, listErr := listReadyBackendPods(ctx, kubeClient, nsName)
			if listErr != nil {
				return listErr
			}
			if len(ready) == 0 {
				return fmt.Errorf("no backend pod is Ready yet in %s: [%s]", nsName, strings.Join(states, ", "))
			}
			readyPodNames = ready
			return nil
		},
	)
	if podErr != nil {
		return fmt.Errorf("backend Deployment %s/%s did not become ready: %w", nsName, deployment.Name, podErr)
	}

	ginkgo.GinkgoWriter.Printf("[%s] backend pod(s) Ready in %s: [%s]\n", v.Name(), nsName, strings.Join(readyPodNames, ", "))
	return nil
}

// listReadyBackendPods returns the names of the backend pods selected by the
// LoadBalancer Service that are currently Ready and not terminating, along with
// a short state summary of every selected pod for diagnostics.
func listReadyBackendPods(ctx context.Context, kubeClient kubernetes.Interface, nsName string) ([]string, []string, error) {
	labelSelector := ccmLBBackendLabelKey + "=" + ccmLBBackendLabelValue
	pods, err := kubeClient.CoreV1().Pods(nsName).List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list backend pods in %s with selector %s: %w", nsName, labelSelector, err)
	}

	var ready, states []string
	for i := range pods.Items {
		pod := &pods.Items[i]
		isReady := pod.DeletionTimestamp == nil && isPodReady(pod)
		if isReady {
			ready = append(ready, pod.Name)
		}
		states = append(states, fmt.Sprintf("%s(phase=%s,ready=%t,terminating=%t)", pod.Name, pod.Status.Phase, isReady, pod.DeletionTimestamp != nil))
	}
	return ready, states, nil
}

// isPodReady reports whether the pod's Ready condition is True.
func isPodReady(pod *corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

// verifyTrafficThroughLB polls http://<assignedIP>:<ccmLBServicePort>/ from the
// test runner until the backend replies with the hostname of one of the backend
// pods that is Ready and not terminating at that moment. The set of acceptable
// replies is re-read on every attempt rather than frozen up front, so a backend
// pod being replaced while the load balancer converges does not cause a false
// failure. Azure load balancer rules and health probes typically take a few
// minutes to start forwarding after the external IP is reported, so transient
// failures are retried.
func (v verifyCCMLoadBalancer) verifyTrafficThroughLB(ctx context.Context, kubeClient kubernetes.Interface, adminRESTConfig *rest.Config, nsName, assignedIP string) error {
	url := "http://" + net.JoinHostPort(assignedIP, strconv.Itoa(ccmLBServicePort)) + "/"
	client := &http.Client{Timeout: ccmLBHTTPRequestTimeout}

	ginkgo.GinkgoWriter.Printf("[%s] fetching %s through the CCM load balancer, expecting a Ready backend pod's hostname\n", v.Name(), url)
	start := time.Now()
	var reply string
	err := pollUntilReady(ctx, v.Name()+"-traffic", v.timeout, DefaultPollInterval, adminRESTConfig, DefaultDiagnoseTimeout, nil,
		func(ctx context.Context) error {
			req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if reqErr != nil {
				return fmt.Errorf("failed to build request for %s: %w", url, reqErr)
			}
			resp, getErr := client.Do(req)
			if getErr != nil {
				return fmt.Errorf("no response from load balancer at %s (LB rule, health probe, backend pool, or NSG not ready): %w", url, getErr)
			}
			defer resp.Body.Close()

			body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4096))
			if readErr != nil {
				return fmt.Errorf("failed to read response body from %s: %w", url, readErr)
			}
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("load balancer at %s returned status %d, expected %d; body: %q", url, resp.StatusCode, http.StatusOK, strings.TrimSpace(string(body)))
			}
			got := strings.TrimSpace(string(body))

			// Compare against the backend pods that are Ready right now: the
			// Deployment may have replaced the original pod while the LB was
			// converging, and the replacement replies with its own hostname.
			readyPods, states, listErr := listReadyBackendPods(ctx, kubeClient, nsName)
			if listErr != nil {
				return listErr
			}
			if !slices.Contains(readyPods, got) {
				return fmt.Errorf("load balancer at %s replied %q, which is not the hostname of any Ready, non-terminating backend pod; selected pods: [%s]",
					url, got, strings.Join(states, ", "))
			}
			reply = got
			return nil
		},
	)
	if err != nil {
		return fmt.Errorf("could not reach backend through CCM load balancer at %s: %w", url, err)
	}

	ginkgo.GinkgoWriter.Printf("[%s] received reply %q (a Ready backend pod) from %s after %s\n", v.Name(), reply, url, time.Since(start).Round(time.Second))
	return nil
}

// publicIPExistsInMRG lists public IP addresses in the managed resource group
// and returns whether expectedIP is among them, along with all observed IPs.
func (v verifyCCMLoadBalancer) publicIPExistsInMRG(ctx context.Context, expectedIP string) (bool, []string, error) {
	publicIPClient := v.networkFactory.NewPublicIPAddressesClient()
	pager := publicIPClient.NewListPager(v.managedResourceGroupName, nil)

	var observedIPs []string
	found := false
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return false, nil, fmt.Errorf("failed to list public IP addresses in resource group %s: %w", v.managedResourceGroupName, err)
		}
		for _, pip := range page.Value {
			if pip.Properties == nil || pip.Properties.IPAddress == nil {
				continue
			}
			ip := *pip.Properties.IPAddress
			observedIPs = append(observedIPs, ip)
			if ip == expectedIP {
				found = true
			}
		}
	}

	return found, observedIPs, nil
}

// VerifyCCMLoadBalancer returns a verifier that deploys an agnhost backend and
// exposes it through a LoadBalancer Service in the guest cluster, waits for the
// CCM to assign an external IP, fetches the backend's reply through that IP
// from the test runner, confirms
// via a direct Azure API call that the public IP exists in the managed
// resource group, then deletes the Service and confirms the public IP is
// released — exercising the CCM identity's load balancer, public IP, NSG,
// and NIC permissions in both directions.
func VerifyCCMLoadBalancer(networkFactory *armnetwork.ClientFactory, managedResourceGroupName string, timeout time.Duration) HostedClusterVerifier {
	return verifyCCMLoadBalancer{
		networkFactory:           networkFactory,
		managedResourceGroupName: managedResourceGroupName,
		timeout:                  timeout,
	}
}

// verifyCCMNodeProviderID checks that every node has been initialized by the
// CCM's Node controller: spec.providerID is set (azure:///…) and the
// node.cloudprovider.kubernetes.io/uninitialized taint has been removed.
type verifyCCMNodeProviderID struct{}

func (v verifyCCMNodeProviderID) Name() string {
	return "VerifyCCMNodeProviderID"
}

func (v verifyCCMNodeProviderID) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	nodes, err := kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list nodes: %w", err)
	}
	if len(nodes.Items) == 0 {
		return fmt.Errorf("no nodes found in the cluster")
	}

	var issues []string
	for i := range nodes.Items {
		node := &nodes.Items[i]

		if node.Spec.ProviderID == "" {
			issues = append(issues, fmt.Sprintf("node %s has no spec.providerID (CCM Node controller has not initialized it)", node.Name))
		} else if !strings.HasPrefix(node.Spec.ProviderID, "azure:///") {
			issues = append(issues, fmt.Sprintf("node %s has unexpected spec.providerID %q (expected azure:/// prefix)", node.Name, node.Spec.ProviderID))
		}

		for _, taint := range node.Spec.Taints {
			if taint.Key == "node.cloudprovider.kubernetes.io/uninitialized" {
				issues = append(issues, fmt.Sprintf("node %s still has the uninitialized taint (key=%s, effect=%s); CCM Node controller has not removed it", node.Name, taint.Key, taint.Effect))
			}
		}
	}

	if len(issues) > 0 {
		return fmt.Errorf("ccm node controller issues on %d node(s): %s", len(issues), strings.Join(issues, "; "))
	}

	ginkgo.GinkgoWriter.Printf("[%s] all %d nodes have azure:/// providerID and no uninitialized taint\n", v.Name(), len(nodes.Items))
	return nil
}

// VerifyCCMNodeProviderID returns a verifier that checks every node in the
// guest cluster has been initialized by the CCM's Node controller:
// spec.providerID starts with azure:/// and the
// node.cloudprovider.kubernetes.io/uninitialized taint has been removed.
func VerifyCCMNodeProviderID() HostedClusterVerifier {
	return verifyCCMNodeProviderID{}
}
