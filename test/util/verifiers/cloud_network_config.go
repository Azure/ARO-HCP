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
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"

	"github.com/Azure/ARO-HCP/test/util/framework"
)

// egressIPName is the name of the EgressIP object this verifier creates.
const egressIPName = "e2e-cloud-network-config-test"

// verifyCloudNetworkConfigEgressIP labels nodeName egress-assignable,
// creates an EgressIP, waits for OVN-Kubernetes to assign it to that node,
// and confirms via a direct Azure API call that the
// cloud-network-config-controller identity added the requested IP as a
// secondary private IP configuration on the node's NIC. It then deletes the
// EgressIP and confirms the secondary IP configuration is removed,
// exercising the identity's Microsoft.Network/networkInterfaces/write
// permission in both directions.
type verifyCloudNetworkConfigEgressIP struct {
	computeFactory *armcompute.ClientFactory
	networkFactory *armnetwork.ClientFactory
	nodeName       string
	egressIP       string
	timeout        time.Duration
}

func (v verifyCloudNetworkConfigEgressIP) Name() string {
	return fmt.Sprintf("VerifyCloudNetworkConfigEgressIP(%s)", v.egressIP)
}

func (v verifyCloudNetworkConfigEgressIP) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	// OVN-Kubernetes only considers egress-assignable-labeled nodes eligible
	// to host an EgressIP, on every supported platform including Azure.
	// Label v.nodeName so it's the (only) eligible node, making assignment
	// deterministic, and unlabel it afterward regardless of outcome.
	if err := framework.LabelNodeEgressAssignable(ctx, adminRESTConfig, v.nodeName); err != nil {
		return fmt.Errorf("failed to label node %s egress-assignable: %w", v.nodeName, err)
	}
	defer func() {
		_ = framework.UnlabelNodeEgressAssignable(context.Background(), adminRESTConfig, v.nodeName)
	}()

	if err := framework.CreateEgressIP(ctx, adminRESTConfig, egressIPName, v.egressIP); err != nil {
		return fmt.Errorf("failed to create EgressIP to exercise the cloud-network-config-controller identity: %w", err)
	}
	defer func() {
		_ = framework.DeleteEgressIP(context.Background(), adminRESTConfig, egressIPName)
	}()

	nodeName, err := framework.WaitForEgressIPAssignedNode(ctx, adminRESTConfig, egressIPName, v.egressIP, v.timeout)
	if err != nil {
		return fmt.Errorf("EgressIP %q was never assigned to a node: %w", egressIPName, err)
	}
	if nodeName != v.nodeName {
		return fmt.Errorf("EgressIP %q was assigned to node %q, expected the labeled node %q", egressIPName, nodeName, v.nodeName)
	}

	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}
	node, err := kubeClient.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get node %q assigned the EgressIP: %w", nodeName, err)
	}

	assignName := v.Name() + "-assign"
	assignErr := pollUntilReady(ctx, assignName, v.timeout, DefaultPollInterval, adminRESTConfig, DefaultDiagnoseTimeout, nil, func(ctx context.Context) error {
		nic, resolveErr := framework.ResolveNodePrimaryNIC(ctx, v.computeFactory, v.networkFactory, node)
		if resolveErr != nil {
			return fmt.Errorf("failed to resolve network interface for node %s: %w", nodeName, resolveErr)
		}
		if !framework.NICHasPrivateIP(nic, v.egressIP) {
			return fmt.Errorf("network interface for node %s does not yet have secondary IP configuration %s", nodeName, v.egressIP)
		}
		return nil
	})
	if assignErr != nil {
		return fmt.Errorf("cloud-network-config-controller identity did not add egress IP %s to node %s's NIC: %w", v.egressIP, nodeName, assignErr)
	}

	if err := framework.DeleteEgressIP(ctx, adminRESTConfig, egressIPName); err != nil {
		return fmt.Errorf("failed to delete EgressIP %q: %w", egressIPName, err)
	}

	releaseName := v.Name() + "-release"
	releaseErr := pollUntilReady(ctx, releaseName, v.timeout, DefaultPollInterval, adminRESTConfig, DefaultDiagnoseTimeout, nil, func(ctx context.Context) error {
		nic, resolveErr := framework.ResolveNodePrimaryNIC(ctx, v.computeFactory, v.networkFactory, node)
		if resolveErr != nil {
			return fmt.Errorf("failed to resolve network interface for node %s: %w", nodeName, resolveErr)
		}
		if framework.NICHasPrivateIP(nic, v.egressIP) {
			return fmt.Errorf("network interface for node %s still has secondary IP configuration %s", nodeName, v.egressIP)
		}
		return nil
	})
	if releaseErr != nil {
		return fmt.Errorf("cloud-network-config-controller identity did not remove egress IP %s from node %s's NIC: %w", v.egressIP, nodeName, releaseErr)
	}

	return nil
}

// VerifyCloudNetworkConfigEgressIP returns a verifier that labels nodeName
// egress-assignable, creates an EgressIP requesting egressIP (which must be
// available in nodeName's subnet), confirms via a direct Azure API call
// that the cloud-network-config-controller identity assigns it as a
// secondary private IP configuration on the node's NIC, then deletes the
// EgressIP and confirms the secondary IP configuration is removed.
func VerifyCloudNetworkConfigEgressIP(computeFactory *armcompute.ClientFactory, networkFactory *armnetwork.ClientFactory, nodeName, egressIP string, timeout time.Duration) HostedClusterVerifier {
	return verifyCloudNetworkConfigEgressIP{
		computeFactory: computeFactory,
		networkFactory: networkFactory,
		nodeName:       nodeName,
		egressIP:       egressIP,
		timeout:        timeout,
	}
}
