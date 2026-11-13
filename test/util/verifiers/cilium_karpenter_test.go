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
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	clienttesting "k8s.io/client-go/testing"

	"github.com/Azure/ARO-HCP/test/util/framework"
)

func ciliumTrafficTestNodes() []corev1.Node {
	source := readyKarpenterTestNode()
	source.Labels["karpenter.sh/initialized"] = "true"
	source.Labels[corev1.LabelHostname] = source.Name
	source.Spec.PodCIDRs = []string{"10.128.0.0/23"}
	target := *source.DeepCopy()
	target.Name, target.UID = "regular-worker", "regular-uid"
	target.Labels = map[string]string{corev1.LabelHostname: target.Name}
	target.Spec.PodCIDRs = nil
	target.Spec.PodCIDR = "10.128.2.0/23"
	return []corev1.Node{source, target}
}

func TestCiliumTrafficNodeSelection(t *testing.T) {
	options := CiliumCrossNodeTrafficOptions{SourceNodeName: "autonode-1"}
	for _, tc := range []struct {
		name      string
		options   *CiliumCrossNodeTrafficOptions
		mutate    func([]corev1.Node)
		wantError string
	}{
		{name: "distinct Karpenter and regular nodes"},
		{name: "not initialized", mutate: func(nodes []corev1.Node) { delete(nodes[0].Labels, "karpenter.sh/initialized") }, wantError: "not initialized"},
		{name: "wrong source labels", mutate: func(nodes []corev1.Node) { delete(nodes[0].Labels, "autonode") }, wantError: "autonode=true"},
		{name: "not Azure", mutate: func(nodes []corev1.Node) { nodes[0].Spec.ProviderID = "other:///vm" }, wantError: "Azure providerID"},
		{name: "source not ready", mutate: func(nodes []corev1.Node) { nodes[0].Status.Conditions = nil }, wantError: "not Ready+schedulable"},
		{name: "Cilium taint", mutate: func(nodes []corev1.Node) {
			nodes[0].Spec.Taints = []corev1.Taint{{Key: "node.cilium.io/agent-not-ready", Effect: corev1.TaintEffectNoExecute}}
		}, wantError: "still carries taint"},
		{name: "unregistered taint", mutate: func(nodes []corev1.Node) { nodes[0].Spec.Taints = []corev1.Taint{{Key: "karpenter.sh/unregistered"}} }, wantError: "still carries taint"},
		{name: "target is Karpenter", mutate: func(nodes []corev1.Node) { nodes[1].Labels[framework.KarpenterNodePoolLabel] = "" }, wantError: "non-Karpenter target"},
		{name: "same UID", mutate: func(nodes []corev1.Node) { nodes[1].UID = nodes[0].UID }, wantError: "distinct non-empty UIDs"},
		{
			name: "explicit Karpenter target",
			options: &CiliumCrossNodeTrafficOptions{
				SourceNodeName: "autonode-1",
				TargetNodeName: "second-karpenter",
			},
			mutate: func(nodes []corev1.Node) {
				second := nodes[0].DeepCopy()
				second.Name, second.UID = "second-karpenter", "second-uid"
				second.Labels[corev1.LabelHostname] = second.Name
				nodes[1] = *second
			},
		},
		{
			name: "explicit target missing",
			options: &CiliumCrossNodeTrafficOptions{
				SourceNodeName: "autonode-1",
				TargetNodeName: "absent-node",
			},
			wantError: "does not exist",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nodes := ciliumTrafficTestNodes()
			if tc.mutate != nil {
				tc.mutate(nodes)
			}
			selected := options
			if tc.options != nil {
				selected = *tc.options
			}
			source, target, err := ciliumTrafficNodes(nodes, selected)
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError, "node selection must reject an invalid cross-node path")
				return
			}
			require.NoError(t, err, "the named initialized Karpenter node and a regular worker must be selected")
			assert.Equal(t, "autonode-1", source.Name, "traffic must originate from the named Karpenter node")
			assert.NotEqual(t, source.UID, target.UID, "source and target must be different node objects")
		})
	}
}

func TestCiliumTrafficAllocationsAndPlacement(t *testing.T) {
	cluster := netip.MustParsePrefix("10.128.0.0/14")
	for _, cidrs := range [][]string{nil, {"invalid"}, {"10.128.0.0/24"}, {"10.0.0.0/23"}} {
		_, err := ciliumTrafficAllocation(cidrs, cluster, 23)
		require.Error(t, err, "allocation %v must not satisfy a /23 allocation inside the configured pod CIDR", cidrs)
	}
	allocations, err := ciliumTrafficAllocation([]string{"10.128.0.0/23"}, cluster, 23)
	require.NoError(t, err, "a cluster-pool /23 inside the configured pod CIDR must pass")
	node := ciliumTrafficTestNodes()[0]
	assert.Equal(t, []string{"10.128.0.0/23"}, observedNodePodCIDRs(&node), "PodCIDRs must be the primary Node observation")
	target := ciliumTrafficTestNodes()[1]
	assert.Equal(t, []string{"10.128.2.0/23"}, observedNodePodCIDRs(&target), "PodCIDR must be used when PodCIDRs is empty")
	client := fake.NewClientset(&node)
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "curl"}, Spec: corev1.PodSpec{NodeName: node.Name}, Status: corev1.PodStatus{PodIP: "10.128.0.10"}}
	require.NoError(t, checkCiliumTrafficPod(t.Context(), client, pod, &node, allocations), "a scheduler-bound pod inside the source allocation must pass")
	pod.Status.PodIP = "10.128.2.10"
	pod.Status.PodIPs = []corev1.PodIP{{IP: "10.128.0.10"}}
	require.ErrorContains(t, checkCiliumTrafficPod(t.Context(), client, pod, &node, allocations), "must belong", "the pod IP must belong to its own node's allocation")
	pod.Spec.NodeName = target.Name
	require.ErrorContains(t, checkCiliumTrafficPod(t.Context(), client, pod, &node, allocations), "must bind", "the scheduler must bind the pod to the selected node")
	pod.Spec.NodeName = node.Name
	wrongUID := *node.DeepCopy()
	wrongUID.UID = "replacement"
	require.ErrorContains(t, checkCiliumTrafficPod(t.Context(), client, pod, &wrongUID, allocations), "node UID", "a node replacement must not satisfy the original placement check")
}

func TestCiliumCrossNodeTrafficVerifier(t *testing.T) {
	for _, tc := range []struct {
		name, body, wantError string
		cleanupDenied         bool
	}{
		{name: "traffic succeeds", body: "server"},
		{name: "response mismatch", body: "another-server", wantError: "response must equal server pod name"},
		{name: "cleanup error joins primary failure", body: "wrong", wantError: "response must equal server pod name", cleanupDenied: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nodes := ciliumTrafficTestNodes()
			pods := map[string]corev1.Pod{}
			var deletes []string
			namespaceDeleted := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				write := func(object any) {
					w.Header().Set("Content-Type", "application/json")
					if err := json.NewEncoder(w).Encode(object); err != nil {
						t.Errorf("encode traffic probe API response: %v", err)
					}
				}
				notFound := func() {
					writeKarpenterResponse(t, w, 404, map[string]any{"apiVersion": "v1", "kind": "Status", "reason": "NotFound", "code": 404})
				}
				switch {
				case r.Method == http.MethodDelete:
					deletes = append(deletes, r.URL.Path)
					if strings.Contains(r.URL.Path, "/pods/") {
						delete(pods, r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:])
					} else {
						namespaceDeleted = true
					}
					if tc.cleanupDenied && strings.HasSuffix(r.URL.Path, "/curl") {
						writeKarpenterResponse(t, w, 403, map[string]any{"apiVersion": "v1", "kind": "Status", "code": 403, "reason": "Forbidden"})
					} else {
						write(map[string]any{"apiVersion": "v1", "kind": "Status", "status": "Success"})
					}
				case r.URL.Path == "/api/v1/nodes":
					write(corev1.NodeList{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "NodeList"}, Items: nodes})
				case strings.HasPrefix(r.URL.Path, "/api/v1/nodes/"):
					for _, node := range nodes {
						if strings.HasSuffix(r.URL.Path, "/"+node.Name) {
							write(node)
							return
						}
					}
					notFound()
				case strings.Contains(r.URL.Path, "/ciliumnodes/"):
					cidr := "10.128.0.0/23"
					if strings.HasSuffix(r.URL.Path, "/regular-worker") {
						cidr = "10.128.2.0/23"
					}
					write(map[string]any{"apiVersion": "cilium.io/v2", "kind": "CiliumNode", "spec": map[string]any{"ipam": map[string]any{"podCIDRs": []string{cidr}}}})
				case r.URL.Path == "/api/v1/namespaces/kube-system/configmaps/cilium-config":
					write(corev1.ConfigMap{Data: map[string]string{"ipam": "cluster-pool", "routing-mode": "tunnel", "tunnel-protocol": "vxlan", "tunnel-port": "4789"}})
				case strings.HasSuffix(r.URL.Path, "/log"):
					_, err := fmt.Fprint(w, tc.body)
					assert.NoError(t, err, "the curl log response must be readable")
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/pods"):
					var pod corev1.Pod
					require.NoError(t, json.NewDecoder(r.Body).Decode(&pod), "probe pod creation must contain a valid pod")
					assert.Empty(t, pod.Spec.NodeName, "the scheduler, not Spec.NodeName, must bind the probe")
					assert.False(t, pod.Spec.HostNetwork, "the probe must use pod networking")
					assert.False(t, *pod.Spec.AutomountServiceAccountToken, "the probe must not mount a service account token")
					assert.True(t, *pod.Spec.SecurityContext.RunAsNonRoot, "the probe must run as a non-root user")
					assert.Equal(t, corev1.SeccompProfileTypeRuntimeDefault, pod.Spec.SecurityContext.SeccompProfile.Type, "the probe must use the default seccomp profile")
					security := pod.Spec.Containers[0].SecurityContext
					assert.False(t, *security.AllowPrivilegeEscalation, "the probe must not permit privilege escalation")
					assert.True(t, *security.ReadOnlyRootFilesystem, "the probe root filesystem must be read-only")
					assert.Equal(t, []corev1.Capability{"ALL"}, security.Capabilities.Drop, "the probe must drop all capabilities")
					assert.Equal(t, "true", pod.Annotations["karpenter.sh/do-not-disrupt"], "Karpenter must not disrupt a live probe")
					assert.Empty(t, pod.Spec.Containers[0].Ports, "the probe must not use host ports")
					if pod.Name == "server" {
						assert.Empty(t, pod.Spec.Containers[0].Command, "agnhost must retain its image ENTRYPOINT")
						assert.Equal(t, []string{"serve-hostname", "--port=8080"}, pod.Spec.Containers[0].Args, "agnhost must serve its pod hostname on port 8080")
						assert.Equal(t, pod.Name, pod.Spec.Hostname, "the response hostname must equal the server pod name")
						pod.Spec.NodeName, pod.Status.PodIP, pod.Status.Phase = "regular-worker", "10.128.2.10", corev1.PodRunning
					} else {
						assert.Equal(t, "true", pod.Spec.NodeSelector["autonode"], "curl must select an AutoNode worker")
						assert.Equal(t, "autonode-e2e", pod.Spec.NodeSelector[framework.KarpenterNodePoolLabel], "curl must select this test's Karpenter NodePool")
						assert.Equal(t, "autonode-1", pod.Spec.NodeSelector[corev1.LabelHostname], "curl must select the workload's exact node")
						assert.Contains(t, pod.Spec.Containers[0].Args, "http://10.128.2.10:8080", "curl must request the literal server pod IP")
						assert.Equal(t, []string{"--noproxy", "*", "--proxy", ""}, pod.Spec.Containers[0].Args[:4], "curl must disable HTTP proxies")
						pod.Spec.NodeName, pod.Status.PodIP, pod.Status.Phase = "autonode-1", "10.128.0.10", corev1.PodSucceeded
					}
					pods[pod.Name] = pod
					write(pod)
				case strings.Contains(r.URL.Path, "/pods/"):
					name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
					if pod, ok := pods[name]; ok {
						write(pod)
					} else {
						notFound()
					}
				case r.Method == http.MethodPost && r.URL.Path == "/api/v1/namespaces":
					var ns corev1.Namespace
					require.NoError(t, json.NewDecoder(r.Body).Decode(&ns), "probe namespace creation must be valid")
					write(ns)
				case namespaceDeleted:
					notFound()
				default:
					t.Errorf("unexpected traffic probe request: %s %s", r.Method, r.URL.Path)
					notFound()
				}
			}))
			t.Cleanup(server.Close)
			err := VerifyCiliumCrossNodePodTraffic(CiliumCrossNodeTrafficOptions{CiliumNamespace: "kube-system", SourceNodeName: "autonode-1", ExpectedPodCIDR: "10.128.0.0/14", ExpectedHostPrefix: 23, ExpectedTunnelPort: 4789}, 100*time.Millisecond).Verify(t.Context(), &rest.Config{Host: server.URL, ContentConfig: rest.ContentConfig{ContentType: "application/json"}})
			if tc.wantError == "" {
				require.NoError(t, err, "direct pod-IP traffic must return the target pod hostname")
			} else {
				require.ErrorContains(t, err, tc.wantError, "the primary traffic failure must be retained")
			}
			if tc.cleanupDenied {
				require.ErrorContains(t, err, "failed to remove Cilium traffic probe pods: the Karpenter node cannot drain while its do-not-disrupt probe pod remains", "cleanup failure must be joined with the traffic failure")
			}
			require.Len(t, deletes, 3, "both pods and their namespace must be deleted even after failure")
			assert.True(t, strings.HasSuffix(deletes[0], "/pods/curl"), "the source do-not-disrupt pod must be deleted first")
			assert.True(t, strings.HasSuffix(deletes[1], "/pods/server"), "the server must be deleted before its namespace")
			assert.NotContains(t, deletes[2], "/pods/", "the namespace must be deleted last")
		})
	}
}

func TestCiliumTrafficConfigAndCleanupFailure(t *testing.T) {
	config := &corev1.ConfigMap{Data: map[string]string{"ipam": "cluster-pool", "routing-mode": "tunnel", "tunnel-protocol": "vxlan", "tunnel-port": "4789"}}
	require.NoError(t, checkCiliumTrafficConfig(config, 4789), "cluster-pool VXLAN tunnel configuration must pass")
	for _, key := range []string{"ipam", "routing-mode", "tunnel-protocol", "tunnel-port"} {
		wrong := config.DeepCopy()
		wrong.Data[key] = "wrong"
		require.ErrorContains(t, checkCiliumTrafficConfig(wrong, 4789), key, "configuration mismatch must identify %s", key)
	}
	client := fake.NewClientset()
	client.PrependReactor("get", "pods", func(action clienttesting.Action) (bool, runtime.Object, error) {
		return true, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: action.(clienttesting.GetAction).GetName()}}, nil
	})
	client.PrependReactor("delete", "pods", func(action clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, action.(clienttesting.DeleteAction).GetName(), fmt.Errorf("probe remains"))
	})
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	err := removeCiliumTrafficProbes(ctx, client, "probes", &rest.Config{})
	require.ErrorContains(t, err, "the Karpenter node cannot drain", "cleanup must fail if a do-not-disrupt probe remains")
}
