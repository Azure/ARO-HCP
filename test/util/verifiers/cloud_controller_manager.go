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
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
)

const (
	ccmLBServiceName      = "e2e-ccm-lb-test"
	ccmLBServiceNamespace = "e2e-ccm-lb"
	ccmLBServicePort      = 80
)

// verifyCCMLoadBalancer creates a Service of type LoadBalancer in the guest
// cluster, waits for the Cloud Controller Manager to assign an external IP
// (proving its Service controller reconciled the object using its managed
// identity), confirms via a direct Azure API call that a corresponding
// public IP address exists in the managed resource group, then deletes the
// Service and confirms the public IP is released — exercising the identity's
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

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ccmLBServiceName,
			Namespace: nsName,
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeLoadBalancer,
			Ports: []corev1.ServicePort{{
				Port:     ccmLBServicePort,
				Protocol: corev1.ProtocolTCP,
			}},
			Selector: map[string]string{
				"app": "e2e-ccm-lb-dummy",
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

// publicIPExistsInMRG lists public IP addresses in the managed resource group
// and returns whether expectedIP is among them, along with all observed IPs.
func (v verifyCCMLoadBalancer) publicIPExistsInMRG(ctx context.Context, expectedIP string) (bool, []string, error) {
	publicIPClient := v.networkFactory.NewPublicIPAddressesClient()
	pager := publicIPClient.NewListPager(v.managedResourceGroupName, nil)

	var observedIPs []string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return false, nil, fmt.Errorf("failed to list public IP addresses in resource group %s: %w", v.managedResourceGroupName, err)
		}
		for _, pip := range page.Value {
			if pip.Properties != nil && pip.Properties.IPAddress != nil {
				ip := *pip.Properties.IPAddress
				observedIPs = append(observedIPs, ip)
				if ip == expectedIP {
					return true, observedIPs, nil
				}
			}
		}
	}

	return false, observedIPs, nil
}

// VerifyCCMLoadBalancer returns a verifier that creates a LoadBalancer Service
// in the guest cluster, waits for the CCM to assign an external IP, confirms
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
