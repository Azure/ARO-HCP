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
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/test/util/framework"
)

// TODO: consolidate this verifier with the Cilium verifiers in cilium.go.
//
// This file and cilium.go both prove that Cilium forwards pod traffic. Each file
// covers different faults:
//   - cilium.go runs the upstream connectivity suite. That suite proves DNS
//     resolution, service load balancing, and the DNS CiliumNetworkPolicy.
//   - this file sends one request by pod IP between the Karpenter node and a
//     regular worker. It proves node placement, the per-node IPAM allocation,
//     and the tunnel settings.
//
// The AutoNode Cilium spec runs both. Keep both while both sets of checks matter.
// A later change can remove the overlap: keep the DNS-dependent subset of the
// connectivity suite, and add the placement and allocation checks to it.

// CiliumCrossNodeTrafficOptions sets the source node and the expected tunnel
// configuration.
type CiliumCrossNodeTrafficOptions struct {
	CiliumNamespace string
	SourceNodeName  string
	// TargetNodeName pins the destination. Leave it empty to use the first Ready
	// node without a Karpenter NodePool label.
	TargetNodeName     string
	ExpectedPodCIDR    string
	ExpectedHostPrefix int32
	ExpectedTunnelPort uint16
}

type verifyCiliumCrossNodePodTraffic struct {
	options CiliumCrossNodeTrafficOptions
	timeout time.Duration
}

func (v verifyCiliumCrossNodePodTraffic) Name() string {
	return "VerifyCiliumCrossNodePodTraffic"
}

// VerifyCiliumCrossNodePodTraffic sends one request by pod IP from the
// Karpenter-provisioned node to a regular worker. It reads the tunnel settings
// from the Cilium configuration. It does not capture VXLAN packets.
func VerifyCiliumCrossNodePodTraffic(options CiliumCrossNodeTrafficOptions, timeout time.Duration) HostedClusterVerifier {
	return verifyCiliumCrossNodePodTraffic{options: options, timeout: timeout}
}

func (v verifyCiliumCrossNodePodTraffic) Verify(ctx context.Context, adminRESTConfig *rest.Config) (retErr error) {
	if v.timeout <= 0 {
		return fmt.Errorf("%s: timeout must be > 0, got %s", v.Name(), v.timeout)
	}
	if v.options.SourceNodeName == "" || v.options.CiliumNamespace == "" {
		return fmt.Errorf("source node name and Cilium namespace must be set")
	}
	clusterCIDR, err := netip.ParsePrefix(v.options.ExpectedPodCIDR)
	if err != nil {
		return fmt.Errorf("invalid expected pod CIDR %q: %w", v.options.ExpectedPodCIDR, err)
	}
	if v.options.ExpectedHostPrefix < int32(clusterCIDR.Bits()) || v.options.ExpectedHostPrefix > int32(clusterCIDR.Addr().BitLen()) {
		return fmt.Errorf("expected host prefix %d must fit inside %s", v.options.ExpectedHostPrefix, clusterCIDR)
	}
	ctx, cancel := context.WithTimeout(ctx, v.timeout)
	defer cancel()
	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}
	dynamicClient, err := dynamic.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}
	namespace := "cilium-karpenter-traffic-" + rand.String(6)
	if _, err := kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create Cilium traffic probe namespace: %w", err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cleanupCancel()
		retErr = errors.Join(retErr, removeCiliumTrafficProbes(cleanupCtx, kubeClient, namespace, adminRESTConfig))
	}()

	var source, target *corev1.Node
	var previousAllocation string
	return pollUntilReady(ctx, v.Name(), v.timeout, DefaultPollInterval, adminRESTConfig, DefaultDiagnoseTimeout, nil, func(ctx context.Context) error {
		nodes, err := kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("failed to find traffic probe nodes: %w", err)
		}
		currentSource, currentTarget, err := ciliumTrafficNodes(nodes.Items, v.options)
		if err != nil {
			return fmt.Errorf("%w; observed nodes: %s", err, nodeSummaries(nodes.Items))
		}
		if source == nil {
			source, target = currentSource.DeepCopy(), currentTarget.DeepCopy()
		}
		for _, expected := range []*corev1.Node{source, target} {
			found := false
			for i := range nodes.Items {
				if nodes.Items[i].Name == expected.Name && nodes.Items[i].UID == expected.UID {
					*expected = *nodes.Items[i].DeepCopy()
					found = true
				}
			}
			if !found {
				return fmt.Errorf("traffic probe node %q UID %q no longer exists", expected.Name, expected.UID)
			}
		}
		if err := ciliumTrafficSourceReady(source); err != nil {
			return err
		}
		// Same gate as the selection step. It accepts a Karpenter node as the
		// target and requires that node to be initialized and untainted.
		if err := ciliumTrafficTargetReady(target); err != nil {
			return err
		}
		config, err := kubeClient.CoreV1().ConfigMaps(v.options.CiliumNamespace).Get(ctx, "cilium-config", metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to read Cilium tunnel configuration: %w", err)
		}
		if err := checkCiliumTrafficConfig(config, v.options.ExpectedTunnelPort); err != nil {
			return err
		}
		allocations := make([][]netip.Prefix, 2)
		var observed []string
		for i, node := range []*corev1.Node{source, target} {
			ciliumNode, err := dynamicClient.Resource(schema.GroupVersionResource{Group: "cilium.io", Version: "v2", Resource: "ciliumnodes"}).Get(ctx, node.Name, metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("failed to get CiliumNode allocation for %q: %w", node.Name, err)
			}
			cidrs, _, err := unstructured.NestedStringSlice(ciliumNode.Object, "spec", "ipam", "podCIDRs")
			if err != nil {
				return fmt.Errorf("failed to read CiliumNode %q podCIDRs: %w", node.Name, err)
			}
			observed = append(observed, fmt.Sprintf("%s: Node.Spec.PodCIDRs=%v CiliumNode.spec.ipam.podCIDRs=%v", node.Name, observedNodePodCIDRs(node), cidrs))
			allocations[i], err = ciliumTrafficAllocation(cidrs, clusterCIDR, v.options.ExpectedHostPrefix)
			if err != nil {
				return fmt.Errorf("node %q: %w; observed %s", node.Name, err, strings.Join(observed, "; "))
			}
		}
		for _, sourceCIDR := range allocations[0] {
			for _, targetCIDR := range allocations[1] {
				if sourceCIDR.Overlaps(targetCIDR) {
					return fmt.Errorf("source and target must have distinct CiliumNode allocations, observed %s and %s", sourceCIDR, targetCIDR)
				}
			}
		}
		allocationSummary := strings.Join(observed, "; ")
		if allocationSummary != previousAllocation {
			ginkgo.GinkgoLogr.Info("Cilium traffic probe allocations", "expectedPodCIDR", clusterCIDR.String(), "expectedHostPrefix", v.options.ExpectedHostPrefix, "observed", allocationSummary)
			previousAllocation = allocationSummary
		}

		server := ciliumTrafficProbePod(namespace, "server", map[string]string{corev1.LabelHostname: target.Labels[corev1.LabelHostname]})
		server.Spec.Hostname = server.Name
		server.Spec.Containers[0].Image = "registry.k8s.io/e2e-test-images/agnhost:2.39"
		// The pod uses the image ENTRYPOINT. The test passes serve-hostname as an
		// agnhost argument.
		server.Spec.Containers[0].Args = []string{"serve-hostname", "--port=8080"}
		server, err = ensureCiliumTrafficPod(ctx, kubeClient, server)
		if err != nil {
			return err
		}
		if err := checkCiliumTrafficPod(ctx, kubeClient, server, target, allocations[1]); err != nil {
			return err
		}
		if server.Status.Phase != corev1.PodRunning {
			return fmt.Errorf("traffic server must be Running, observed %s", ciliumTrafficPodStatus(server))
		}
		sourceSelector := map[string]string{
			"autonode":                       "true",
			framework.KarpenterNodePoolLabel: source.Labels[framework.KarpenterNodePoolLabel],
			corev1.LabelHostname:             source.Labels[corev1.LabelHostname],
		}
		curl := ciliumTrafficProbePod(namespace, "curl", sourceSelector)
		curl.Spec.RestartPolicy = corev1.RestartPolicyOnFailure
		curl.Spec.Containers[0].Image = "quay.io/cilium/alpine-curl:v1.5.0@sha256:7b286939730d8af1149ef88dba15739d8330bb83d7d9853a23e5ab4043e2d33c"
		curl.Spec.Containers[0].Command = []string{"curl"}
		curl.Spec.Containers[0].Args = []string{"--noproxy", "*", "--proxy", "", "--fail", "--silent", "--show-error", "--connect-timeout", "5", "--max-time", "10", "http://" + net.JoinHostPort(server.Status.PodIP, "8080")}
		curl, err = ensureCiliumTrafficPod(ctx, kubeClient, curl)
		if err != nil {
			return err
		}
		if err := checkCiliumTrafficPod(ctx, kubeClient, curl, source, allocations[0]); err != nil {
			return err
		}
		if curl.Status.Phase != corev1.PodSucceeded {
			return fmt.Errorf("direct pod-IP request from %q to %s must succeed, observed curl %s", source.Name, server.Status.PodIP, ciliumTrafficPodStatus(curl))
		}
		body, err := kubeClient.CoreV1().Pods(namespace).GetLogs(curl.Name, &corev1.PodLogOptions{Container: "probe"}).DoRaw(ctx)
		if err != nil {
			return fmt.Errorf("failed to read direct pod-IP response: %w", err)
		}
		if string(body) != server.Name {
			return fmt.Errorf("direct pod-IP response must equal server pod name %q, observed %q", server.Name, body)
		}
		return nil
	})
}

func ciliumTrafficSourceReady(node *corev1.Node) error {
	if reason := karpenterNodeNotReadyReason(node); reason != "" {
		return fmt.Errorf("source node %q: %s", node.Name, reason)
	}
	if node.Labels["autonode"] != "true" || node.Labels[framework.KarpenterNodePoolLabel] == "" {
		return fmt.Errorf("source node %q must carry autonode=true and a Karpenter NodePool label", node.Name)
	}
	if reason := karpenterAdditionalNodeReason(node, nil, KarpenterProvisioningChecks{RequireInitialized: true, AbsentTaintKeys: []string{"karpenter.sh/unregistered", "node.cilium.io/agent-not-ready"}}); reason != "" {
		return fmt.Errorf("source node %q: %s", node.Name, reason)
	}
	if !strings.HasPrefix(strings.ToLower(node.Spec.ProviderID), "azure:///") {
		return fmt.Errorf("source node %q must have an Azure providerID, observed %q", node.Name, node.Spec.ProviderID)
	}
	return nil
}

// ciliumTrafficTargetReady checks the destination node. A Karpenter node must
// also be initialized and free of the startup taints, or the server pod cannot
// schedule on it.
func ciliumTrafficTargetReady(node *corev1.Node) error {
	if node.DeletionTimestamp != nil {
		return fmt.Errorf("target node %q is deleting", node.Name)
	}
	if !nodeReadyAndSchedulable(node) {
		return fmt.Errorf("target node %q must be Ready and schedulable", node.Name)
	}
	if _, karpenter := node.Labels[framework.KarpenterNodePoolLabel]; !karpenter {
		return nil
	}
	checks := KarpenterProvisioningChecks{
		RequireInitialized: true,
		AbsentTaintKeys:    []string{"karpenter.sh/unregistered", "node.cilium.io/agent-not-ready"},
	}
	if reason := karpenterAdditionalNodeReason(node, nil, checks); reason != "" {
		return fmt.Errorf("target node %q: %s", node.Name, reason)
	}
	return nil
}

func ciliumTrafficNodes(nodes []corev1.Node, options CiliumCrossNodeTrafficOptions) (*corev1.Node, *corev1.Node, error) {
	slices.SortFunc(nodes, func(a, b corev1.Node) int { return strings.Compare(a.Name, b.Name) })
	var source, target *corev1.Node
	for i := range nodes {
		node := &nodes[i]
		if node.Name == options.SourceNodeName {
			source = node
			continue
		}
		if options.TargetNodeName != "" {
			if node.Name == options.TargetNodeName {
				target = node
			}
			continue
		}
		// Default target: the first Ready node that Karpenter did not provision.
		if _, karpenter := node.Labels[framework.KarpenterNodePoolLabel]; !karpenter && target == nil && nodeReadyAndSchedulable(node) && node.DeletionTimestamp == nil {
			target = node
		}
	}
	if source == nil || target == nil {
		if options.TargetNodeName != "" {
			return nil, nil, fmt.Errorf("target node %q does not exist", options.TargetNodeName)
		}
		return nil, nil, fmt.Errorf("expected source %q and a distinct Ready non-Karpenter target node", options.SourceNodeName)
	}
	if err := ciliumTrafficTargetReady(target); err != nil {
		return nil, nil, err
	}
	if err := ciliumTrafficSourceReady(source); err != nil {
		return nil, nil, err
	}
	if source.UID == "" || target.UID == "" || source.UID == target.UID {
		return nil, nil, fmt.Errorf("traffic probe nodes must have distinct non-empty UIDs, observed source=%q target=%q", source.UID, target.UID)
	}
	if source.Labels[corev1.LabelHostname] == "" || target.Labels[corev1.LabelHostname] == "" {
		return nil, nil, fmt.Errorf("traffic probe nodes must have kubernetes.io/hostname labels")
	}
	return source, target, nil
}

func checkCiliumTrafficConfig(config *corev1.ConfigMap, tunnelPort uint16) error {
	// This check reads the configured port. It does not measure the port from
	// traffic.
	for _, entry := range [][2]string{{"ipam", "cluster-pool"}, {"routing-mode", "tunnel"}, {"tunnel-protocol", "vxlan"}, {"tunnel-port", strconv.Itoa(int(tunnelPort))}} {
		if config.Data[entry[0]] != entry[1] {
			return fmt.Errorf("cilium configuration %s/%s must have %s=%s, observed %q", config.Namespace, config.Name, entry[0], entry[1], config.Data[entry[0]])
		}
	}
	return nil
}

func observedNodePodCIDRs(node *corev1.Node) []string {
	if len(node.Spec.PodCIDRs) > 0 {
		return node.Spec.PodCIDRs
	}
	if node.Spec.PodCIDR != "" {
		return []string{node.Spec.PodCIDR}
	}
	return nil
}

func ciliumTrafficAllocation(cidrs []string, cluster netip.Prefix, hostPrefix int32) ([]netip.Prefix, error) {
	if len(cidrs) == 0 {
		return nil, fmt.Errorf("CiliumNode must have a cluster-pool pod CIDR allocation")
	}
	var allocations []netip.Prefix
	for _, cidr := range cidrs {
		allocation, err := netip.ParsePrefix(cidr)
		if err != nil || allocation.Bits() != int(hostPrefix) || !cluster.Contains(allocation.Addr()) {
			return nil, fmt.Errorf("CiliumNode allocation %q must have prefix /%d inside %s", cidr, hostPrefix, cluster)
		}
		allocations = append(allocations, allocation.Masked())
	}
	return allocations, nil
}

func ciliumTrafficProbePod(namespace, name string, selector map[string]string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Annotations: map[string]string{"karpenter.sh/do-not-disrupt": "true"}},
		Spec: corev1.PodSpec{
			NodeSelector: selector, HostNetwork: false, AutomountServiceAccountToken: ptr.To(false), RestartPolicy: corev1.RestartPolicyNever,
			SecurityContext: &corev1.PodSecurityContext{RunAsNonRoot: ptr.To(true), SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}},
			Containers: []corev1.Container{{Name: "probe", SecurityContext: &corev1.SecurityContext{
				AllowPrivilegeEscalation: ptr.To(false), ReadOnlyRootFilesystem: ptr.To(true), Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
			}}},
		},
	}
}

func ensureCiliumTrafficPod(ctx context.Context, client kubernetes.Interface, pod *corev1.Pod) (*corev1.Pod, error) {
	current, err := client.CoreV1().Pods(pod.Namespace).Get(ctx, pod.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		current, err = client.CoreV1().Pods(pod.Namespace).Create(ctx, pod, metav1.CreateOptions{})
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get or create traffic probe pod %s/%s: %w", pod.Namespace, pod.Name, err)
	}
	return current, nil
}

func checkCiliumTrafficPod(ctx context.Context, client kubernetes.Interface, pod *corev1.Pod, expected *corev1.Node, allocations []netip.Prefix) error {
	if pod.Spec.NodeName != expected.Name {
		return fmt.Errorf("traffic probe pod %q must bind to node %q, observed node=%q %s", pod.Name, expected.Name, pod.Spec.NodeName, ciliumTrafficPodStatus(pod))
	}
	node, err := client.CoreV1().Nodes().Get(ctx, pod.Spec.NodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to verify bound node UID for pod %q: %w", pod.Name, err)
	}
	if node.UID == "" || node.UID != expected.UID {
		return fmt.Errorf("traffic probe pod %q must bind to node UID %q, observed %q", pod.Name, expected.UID, node.UID)
	}
	if pod.Status.PodIP == "" {
		return fmt.Errorf("traffic probe pod %q must have a pod IP, observed phase=%s", pod.Name, pod.Status.Phase)
	}
	ips := append([]corev1.PodIP{{IP: pod.Status.PodIP}}, pod.Status.PodIPs...)
	for _, ip := range ips {
		address, err := netip.ParseAddr(ip.IP)
		if err != nil || !slices.ContainsFunc(allocations, func(prefix netip.Prefix) bool { return prefix.Contains(address) }) {
			return fmt.Errorf("traffic probe pod %q IP %q must belong to node %q CiliumNode allocation %v", pod.Name, ip.IP, node.Name, allocations)
		}
	}
	return nil
}

func ciliumTrafficPodStatus(pod *corev1.Pod) string {
	status, _ := json.Marshal(pod.Status)
	return string(status)
}

func removeCiliumTrafficProbes(ctx context.Context, client kubernetes.Interface, namespace string, config *rest.Config) error {
	var cleanupErr error
	for _, name := range []string{"curl", "server"} {
		if err := client.CoreV1().Pods(namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("delete pod %s: %w", name, err))
		}
	}
	if err := client.CoreV1().Namespaces().Delete(ctx, namespace, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("delete namespace %s: %w", namespace, err))
	}
	err := pollUntilReady(ctx, "RemoveCiliumTrafficProbes", 2*time.Minute, DefaultPollInterval, config, DefaultDiagnoseTimeout, nil, func(ctx context.Context) error {
		for _, name := range []string{"curl", "server"} {
			if _, err := client.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
				return fmt.Errorf("pod %s/%s remains or removal cannot be confirmed: %v", namespace, name, err)
			}
		}
		if _, err := client.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
			return fmt.Errorf("namespace %s remains or removal cannot be confirmed: %v", namespace, err)
		}
		return nil
	})
	if err = errors.Join(cleanupErr, err); err != nil {
		return fmt.Errorf("failed to remove Cilium traffic probe pods: the Karpenter node cannot drain while its do-not-disrupt probe pod remains: %w", err)
	}
	return nil
}
