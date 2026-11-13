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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"

	"github.com/Azure/ARO-HCP/test/util/framework"
)

func writeKarpenterResponse(t *testing.T, w http.ResponseWriter, status int, object map[string]any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(object); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

func readyKarpenterTestNode() corev1.Node {
	return corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "autonode-1", UID: "node-uid", Labels: map[string]string{framework.KarpenterNodePoolLabel: "autonode-e2e", "autonode": "true"}},
		Spec:       corev1.NodeSpec{ProviderID: "azure:///subscriptions/test/virtualMachines/node"},
		Status:     corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue, Reason: "KubeletReady", Message: "node is ready"}}},
	}
}

func testKarpenterClaim(name string) unstructured.Unstructured {
	return unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "karpenter.sh/v1", "kind": "NodeClaim",
		"metadata": map[string]any{"name": name, "uid": name + "-uid"},
		"spec": map[string]any{"startupTaints": []any{
			map[string]any{"key": "node.cilium.io/agent-not-ready", "value": "true", "effect": "NoExecute"},
			map[string]any{"key": "node.cilium.io/agent-not-ready", "effect": "NoSchedule"},
		}},
		"status": map[string]any{"nodeName": "autonode-1", "conditions": []any{map[string]any{"type": "Initialized", "status": "True", "reason": "Ready", "message": "claim initialized"}}},
	}}
}

func TestKarpenterProvisioningChecks(t *testing.T) {
	expected := []corev1.Taint{
		{Key: "node.cilium.io/agent-not-ready", Value: "ignored", Effect: corev1.TaintEffectNoSchedule},
		{Key: "node.cilium.io/agent-not-ready", Effect: corev1.TaintEffectNoExecute},
	}
	for _, tc := range []struct {
		name      string
		checks    KarpenterProvisioningChecks
		mutate    func(*corev1.Node, *[]unstructured.Unstructured)
		wantError string
	}{
		{name: "zero value preserves readiness without initialization"},
		{name: "declarations compare key and effect as a set", checks: KarpenterProvisioningChecks{ExpectedStartupTaints: append(slices.Clone(expected), expected[0])}},
		{name: "every live claim must declare taints", checks: KarpenterProvisioningChecks{ExpectedStartupTaints: expected}, mutate: func(_ *corev1.Node, claims *[]unstructured.Unstructured) {
			second := testKarpenterClaim("second")
			delete(second.Object, "spec")
			*claims = append(*claims, second)
		}, wantError: "startup-taint declaration mismatch"},
		{name: "terminating claims do not need declarations", checks: KarpenterProvisioningChecks{ExpectedStartupTaints: expected}, mutate: func(_ *corev1.Node, claims *[]unstructured.Unstructured) {
			second := testKarpenterClaim("terminating")
			delete(second.Object, "spec")
			now := metav1.Now()
			second.SetDeletionTimestamp(&now)
			*claims = append(*claims, second)
		}},
		{name: "initialization label", checks: KarpenterProvisioningChecks{RequireInitialized: true}, mutate: func(node *corev1.Node, claims *[]unstructured.Unstructured) {
			node.Labels["karpenter.sh/initialized"] = "true"
			*claims = nil
		}},
		{name: "matching initialized claim", checks: KarpenterProvisioningChecks{RequireInitialized: true}},
		{name: "ready is not initialized", checks: KarpenterProvisioningChecks{RequireInitialized: true}, mutate: func(_ *corev1.Node, claims *[]unstructured.Unstructured) {
			*claims = nil
		}, wantError: "Ready but not initialized"},
		{name: "another node's claim cannot initialize this node", checks: KarpenterProvisioningChecks{RequireInitialized: true}, mutate: func(_ *corev1.Node, claims *[]unstructured.Unstructured) {
			(*claims)[0].Object["status"].(map[string]any)["nodeName"] = "another-node"
		}, wantError: "Ready but not initialized"},
		{name: "absent taints match any effect", checks: KarpenterProvisioningChecks{AbsentTaintKeys: []string{"node.cilium.io/agent-not-ready"}}, mutate: func(node *corev1.Node, _ *[]unstructured.Unstructured) {
			node.Spec.Taints = []corev1.Taint{{Key: "node.cilium.io/agent-not-ready", Value: "true", Effect: corev1.TaintEffectPreferNoSchedule}}
		}, wantError: "still carries taint"},
		{name: "specific node must satisfy checks", checks: KarpenterProvisioningChecks{NodeName: "another-node"}, wantError: "required node=\"another-node\""},
		{name: "terminating claims do not exceed bound", checks: KarpenterProvisioningChecks{MaxConcurrentNodeClaims: 1}, mutate: func(_ *corev1.Node, claims *[]unstructured.Unstructured) {
			second := testKarpenterClaim("terminating")
			now := metav1.Now()
			second.SetDeletionTimestamp(&now)
			*claims = append(*claims, second)
		}},
		{name: "concurrent bound stops early", checks: KarpenterProvisioningChecks{MaxConcurrentNodeClaims: 2}, mutate: func(_ *corev1.Node, claims *[]unstructured.Unstructured) {
			*claims = append(*claims, testKarpenterClaim("second"), testKarpenterClaim("third"))
		}, wantError: "concurrent NodeClaim count 3 exceeds limit 2: check startup taint propagation and node replacement"},
		{name: "provider ID is still required", mutate: func(node *corev1.Node, _ *[]unstructured.Unstructured) { node.Spec.ProviderID = "" }, wantError: "Spec.ProviderID is still empty"},
		{name: "cloud initialization is still required", mutate: func(node *corev1.Node, _ *[]unstructured.Unstructured) {
			node.Spec.Taints = []corev1.Taint{{Key: framework.NodeUninitializedTaint}}
		}, wantError: framework.NodeUninitializedTaint},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := readyKarpenterTestNode()
			claims := []unstructured.Unstructured{testKarpenterClaim("first")}
			if tc.mutate != nil {
				tc.mutate(&node, &claims)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodGet, r.Method, "provisioning verification must not change guest resources")
				if r.URL.Path == "/api/v1/nodes" {
					w.Header().Set("Content-Type", "application/json")
					if err := json.NewEncoder(w).Encode(corev1.NodeList{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "NodeList"}, Items: []corev1.Node{node}}); err != nil {
						t.Errorf("encode node readiness: %v", err)
					}
					return
				}
				assert.Equal(t, "/apis/karpenter.sh/v1/nodeclaims", r.URL.Path, "claim checks must read the pool's NodeClaims")
				items := make([]any, 0, len(claims))
				for _, claim := range claims {
					items = append(items, claim.Object)
				}
				writeKarpenterResponse(t, w, 200, map[string]any{"apiVersion": "karpenter.sh/v1", "kind": "NodeClaimList", "items": items})
			}))
			t.Cleanup(server.Close)
			timeout := 40 * time.Millisecond
			if tc.name == "concurrent bound stops early" {
				timeout = time.Minute
			}
			start := time.Now()
			err := VerifyKarpenterNodeProvisioned("autonode-e2e", timeout, tc.checks).Verify(t.Context(), &rest.Config{Host: server.URL})
			if tc.wantError == "" {
				require.NoError(t, err, "a node that satisfies all requested checks must pass")
			} else {
				require.ErrorContains(t, err, tc.wantError, "the failure must identify the requested check")
				if tc.name == "concurrent bound stops early" {
					assert.Less(t, time.Since(start), time.Second, "a bound breach must cancel polling before its deadline")
				}
			}
		})
	}
}

func TestKarpenterDiagnosticsAreSortedAndDetailed(t *testing.T) {
	node := readyKarpenterTestNode()
	node.Spec.Taints = []corev1.Taint{{Key: "z", Value: "true", Effect: corev1.TaintEffectNoExecute}, {Key: "a", Effect: corev1.TaintEffectNoSchedule}}
	other := *node.DeepCopy()
	other.Name = "a-node"
	summary := nodeSummaries([]corev1.Node{node, other})
	assert.Equal(t, summary, nodeSummaries([]corev1.Node{other, node}), "node list order must not change diagnostics")
	for _, detail := range []string{"uid=node-uid", "Ready=True", "KubeletReady", "node is ready", "initialized=", "labels=", "a=:NoSchedule z=true:NoExecute", "providerID="} {
		assert.Contains(t, summary, detail, "node diagnostics must include %s", detail)
	}
	first, second := testKarpenterClaim("a"), testKarpenterClaim("z")
	claimSummary := summarizeNodeClaims([]unstructured.Unstructured{second, first})
	assert.Equal(t, claimSummary, summarizeNodeClaims([]unstructured.Unstructured{first, second}), "claim list order must not change diagnostics")
	for _, detail := range []string{"uid=a-uid", "deletionTimestamp=none", "Initialized=True", "claim initialized"} {
		assert.Contains(t, claimSummary, detail, "claim diagnostics must include %s", detail)
	}
}

func TestOpenShiftAzureNodeClassReady(t *testing.T) {
	for _, state := range []string{"missing", "False", "Unknown", "True"} {
		t.Run(state, func(t *testing.T) {
			providerReads := make(chan struct{}, 4)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodGet, r.Method, "NodeClass verification must not write resources")
				conditions := []any{}
				if strings.Contains(r.URL.Path, "/aksnodeclasses/") {
					providerReads <- struct{}{}
					conditions = []any{map[string]any{"type": "Ready", "status": "True"}}
				} else {
					assert.Equal(t, "/apis/karpenter.hypershift.openshift.io/v1alpha1/openshiftazurenodeclasses/autonode-e2e", r.URL.Path, "readiness must use the canonical NodeClass API")
					if state != "missing" {
						conditions = []any{map[string]any{"type": "Ready", "status": state, "reason": "VersionPending", "message": "waiting for ignition"}}
					}
				}
				writeKarpenterResponse(t, w, 200, map[string]any{
					"apiVersion": "karpenter.hypershift.openshift.io/v1alpha1", "kind": "OpenShiftAzureNodeClass",
					"metadata": map[string]any{"name": "autonode-e2e"}, "status": map[string]any{"conditions": conditions, "version": "4.22.0", "releaseImage": "release"},
				})
			}))
			t.Cleanup(server.Close)
			timeout := 100 * time.Millisecond
			if state == "True" {
				timeout = time.Second
			}
			err := VerifyOpenShiftAzureNodeClassReady("autonode-e2e", timeout).Verify(t.Context(), &rest.Config{Host: server.URL})
			if state == "True" {
				require.NoError(t, err, "the canonical Ready=True condition must satisfy NodeClass readiness")
				assert.Empty(t, providerReads, "provider readiness must not be a second gate")
			} else {
				require.ErrorContains(t, err, "OpenShiftAzureNodeClass", "readiness failure must identify the canonical NodeClass kind")
				assert.Contains(t, err.Error(), "autonode-e2e", "readiness failure must identify the requested NodeClass name")
				assert.NotEmpty(t, providerReads, "timeout must collect read-only provider diagnostics")
				if state != "missing" {
					assert.Contains(t, err.Error(), "VersionPending", "readiness diagnostics must retain the condition reason")
					assert.Contains(t, err.Error(), "waiting for ignition", "readiness diagnostics must retain the condition message")
				}
			}
		})
	}
}

func TestKarpenterCRDsIncludeOpenShiftAzureNodeClass(t *testing.T) {
	assert.Contains(t, karpenterGuestCRDs, schema.GroupVersionResource{
		Group: "karpenter.hypershift.openshift.io", Version: "v1alpha1", Resource: "openshiftazurenodeclasses",
	}, "guest CRD discovery must include the canonical OpenShiftAzureNodeClass API")
}

func TestKarpenterDeprovisionedRequiresNodeClassDeletion(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		classStatus, providerStatus int
		wantError                   string
	}{
		{name: "all absent", classStatus: 404, providerStatus: 404},
		{name: "canonical finalizer remains", classStatus: 200, providerStatus: 404, wantError: "azure-nodeclass-finalizer"},
		{name: "provider remains", classStatus: 404, providerStatus: 200, wantError: "aksnodeclasses"},
		{name: "forbidden is not absent", classStatus: 403, providerStatus: 404, wantError: "failed to check deletion"},
		{name: "provider error is not absent", classStatus: 404, providerStatus: 500, wantError: "failed to check deletion"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodGet, r.Method, "deprovisioning verification must not change guest resources")
				if r.URL.Path == "/api/v1/nodes" || r.URL.Path == "/apis/karpenter.sh/v1/nodeclaims" {
					writeKarpenterResponse(t, w, 200, map[string]any{"apiVersion": "v1", "kind": "List", "items": []any{}})
					return
				}
				status := tc.classStatus
				if strings.Contains(r.URL.Path, "/aksnodeclasses/") {
					status = tc.providerStatus
				}
				if status != 200 {
					writeKarpenterResponse(t, w, status, map[string]any{"apiVersion": "v1", "kind": "Status", "code": status, "message": "test API response"})
					return
				}
				writeKarpenterResponse(t, w, 200, map[string]any{
					"apiVersion": "karpenter.hypershift.openshift.io/v1alpha1", "kind": "OpenShiftAzureNodeClass",
					"metadata": map[string]any{"name": "autonode-e2e", "deletionTimestamp": "2026-11-11T10:00:00Z", "finalizers": []any{"hypershift.openshift.io/azure-nodeclass-finalizer"}},
				})
			}))
			t.Cleanup(server.Close)
			timeout := 100 * time.Millisecond
			if tc.wantError == "" {
				timeout = time.Second
			}
			err := VerifyKarpenterDeprovisioned("autonode-e2e", timeout).Verify(t.Context(), &rest.Config{Host: server.URL})
			if tc.wantError == "" {
				require.NoError(t, err, "deprovisioning must pass when nodes, claims, and both NodeClasses are absent")
			} else {
				require.ErrorContains(t, err, tc.wantError, "deprovisioning must identify the remaining resource or failed deletion check")
			}
		})
	}
}
