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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"

	"github.com/Azure/ARO-HCP/test/util/framework"
)

type nodeClassVersionTestState struct {
	class   *unstructured.Unstructured
	nodes   []corev1.Node
	claims  []unstructured.Unstructured
	oldNode *corev1.Node
}

func TestKarpenterNodeClassVersion(t *testing.T) {
	for _, tc := range []struct {
		name      string
		initial   bool
		change    func(*nodeClassVersionTestState)
		wantError string
	}{
		{name: "replacement node passes"},
		{name: "initial 4.21 node passes", initial: true},
		{name: "old node remains with target kubelet", change: func(s *nodeClassVersionTestState) {
			s.oldNode = s.nodes[0].DeepCopy()
			s.oldNode.Name = "old-node"
		}, wantError: `old node "old-node" still exists; expected NotFound`},
		{name: "label removal does not prove replacement", change: func(s *nodeClassVersionTestState) {
			s.oldNode = s.nodes[0].DeepCopy()
			s.oldNode.Name = "old-node"
			s.oldNode.Labels = nil
		}, wantError: `old node "old-node" still exists; expected NotFound`},
		{name: "deleting old node is not gone", change: func(s *nodeClassVersionTestState) {
			s.oldNode = s.nodes[0].DeepCopy()
			s.oldNode.Name = "old-node"
			now := metav1.Now()
			s.oldNode.DeletionTimestamp = &now
			s.oldNode.Finalizers = []string{"karpenter.sh/termination"}
		}, wantError: "finalizers=[karpenter.sh/termination]"},
		{name: "new node with old kubelet fails", change: func(s *nodeClassVersionTestState) {
			s.nodes[0].Status.NodeInfo.KubeletVersion = "v1.34.9+old"
		}, wantError: "minor 34, expected 35 for OCP 4.22.23"},
		{name: "every node must have the target kubelet", change: func(s *nodeClassVersionTestState) {
			old := *s.nodes[0].DeepCopy()
			old.Name = "another-node"
			old.Status.NodeInfo.KubeletVersion = "v1.34.9+old"
			s.nodes = append(s.nodes, old)
		}, wantError: "another-node (KubeletVersion v1.34.9+old"},
		{name: "invalid kubelet fails", change: func(s *nodeClassVersionTestState) {
			s.nodes[0].Status.NodeInfo.KubeletVersion = "unknown"
		}, wantError: `cannot parse KubeletVersion "unknown"`},
		{name: "no nodes fails", change: func(s *nodeClassVersionTestState) {
			s.nodes = nil
		}, wantError: "no nodes match karpenter.sh/nodepool=autonode-e2e"},
		{name: "stale resolved version fails", change: func(s *nodeClassVersionTestState) {
			s.class.Object["status"].(map[string]any)["version"] = "4.21.43"
		}, wantError: `NodeClass status.version="4.21.43"; expected "4.22.23"`},
		{name: "unchanged desired version fails", change: func(s *nodeClassVersionTestState) {
			s.class.Object["spec"].(map[string]any)["version"] = "4.21.43"
		}, wantError: `NodeClass spec.version="4.21.43"; expected "4.22.23"`},
		{name: "unresolved image fails", change: func(s *nodeClassVersionTestState) {
			delete(s.class.Object["status"].(map[string]any), "releaseImage")
		}, wantError: `NodeClass status.releaseImage=""; expected a resolved image`},
		{name: "not ready NodeClass fails", change: func(s *nodeClassVersionTestState) {
			s.class.Object["status"].(map[string]any)["conditions"] = []any{map[string]any{"type": "Ready", "status": "False"}}
		}, wantError: `NodeClass Ready="False"; expected True`},
		{name: "not ready node fails", change: func(s *nodeClassVersionTestState) {
			s.nodes[0].Status.Conditions[0].Status = corev1.ConditionFalse
		}, wantError: "not Ready+schedulable"},
		{name: "cordoned node fails", change: func(s *nodeClassVersionTestState) {
			s.nodes[0].Spec.Unschedulable = true
		}, wantError: "not Ready+schedulable"},
		{name: "unlinked node fails", change: func(s *nodeClassVersionTestState) {
			s.nodes[0].Spec.ProviderID = ""
		}, wantError: "Spec.ProviderID is still empty"},
		{name: "cloud initialization taint fails", change: func(s *nodeClassVersionTestState) {
			s.nodes[0].Spec.Taints = []corev1.Taint{{Key: framework.NodeUninitializedTaint}}
		}, wantError: framework.NodeUninitializedTaint},
		{name: "uninitialized node fails", change: func(s *nodeClassVersionTestState) {
			delete(s.nodes[0].Labels, "karpenter.sh/initialized")
			s.claims = nil
		}, wantError: "Ready but not initialized"},
		{name: "initialized claim passes", change: func(s *nodeClassVersionTestState) {
			delete(s.nodes[0].Labels, "karpenter.sh/initialized")
		}},
		{name: "terminating replacement fails", change: func(s *nodeClassVersionTestState) {
			now := metav1.Now()
			s.nodes[0].DeletionTimestamp = &now
		}, wantError: "is terminating; expected a live replacement"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			version := "4.22.23"
			previousNames := []string{"old-node"}
			node := readyKarpenterTestNode()
			node.Status.NodeInfo.KubeletVersion = "v1.35.8+target"
			node.Labels["karpenter.sh/initialized"] = "true"
			if tc.initial {
				version = "4.21.43"
				previousNames = nil
				node.Status.NodeInfo.KubeletVersion = "v1.34.9+initial"
			}
			state := nodeClassVersionTestState{
				class: &unstructured.Unstructured{Object: map[string]any{
					"apiVersion": "karpenter.hypershift.openshift.io/v1alpha1", "kind": "OpenShiftAzureNodeClass",
					"metadata": map[string]any{"name": "autonode-e2e"},
					"spec":     map[string]any{"version": version},
					"status": map[string]any{
						"version": version, "releaseImage": "quay.io/test/release@sha256:target",
						"conditions": []any{map[string]any{"type": "Ready", "status": "True"}},
					},
				}},
				nodes: []corev1.Node{node}, claims: []unstructured.Unstructured{testKarpenterClaim("new-claim")},
			}
			if tc.change != nil {
				tc.change(&state)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodGet, r.Method, "the verifier must not cause node replacement")
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/apis/karpenter.hypershift.openshift.io/v1alpha1/openshiftazurenodeclasses/autonode-e2e", "/apis/karpenter.azure.com/v1beta1/aksnodeclasses/autonode-e2e":
					writeKarpenterResponse(t, w, http.StatusOK, state.class.Object)
				case "/api/v1/nodes":
					assert.Equal(t, "karpenter.sh/nodepool=autonode-e2e", r.URL.Query().Get("labelSelector"), "the version check must select Karpenter nodes, not regular workers")
					err := json.NewEncoder(w).Encode(corev1.NodeList{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "NodeList"}, Items: state.nodes})
					assert.NoError(t, err, "the node response must encode")
				case "/api/v1/nodes/old-node":
					assert.Empty(t, r.URL.Query().Get("labelSelector"), "the old node check must not depend on labels")
					if state.oldNode != nil {
						assert.NoError(t, json.NewEncoder(w).Encode(state.oldNode), "the old node response must encode")
						return
					}
					writeKarpenterResponse(t, w, http.StatusNotFound, map[string]any{"apiVersion": "v1", "kind": "Status", "status": "Failure", "reason": "NotFound", "code": 404})
				case "/apis/karpenter.sh/v1/nodeclaims":
					assert.Equal(t, "karpenter.sh/nodepool=autonode-e2e", r.URL.Query().Get("labelSelector"), "the claim check must select the test NodePool")
					items := make([]any, 0, len(state.claims))
					for _, claim := range state.claims {
						items = append(items, claim.Object)
					}
					writeKarpenterResponse(t, w, http.StatusOK, map[string]any{"apiVersion": "karpenter.sh/v1", "kind": "NodeClaimList", "items": items})
				default:
					t.Errorf("unexpected verifier request: %s", r.URL)
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(server.Close)
			evidence := NodeClassUpgradeEvidence{PreviousNodeNames: previousNames}
			err := VerifyKarpenterNodeClassVersion("autonode-e2e", version, evidence, 30*time.Millisecond).Verify(t.Context(), &rest.Config{Host: server.URL})
			if tc.wantError == "" {
				require.NoError(t, err, "the resolved version and all node checks must pass")
			} else {
				require.ErrorContains(t, err, tc.wantError, "the failure must identify the missing upgrade signal")
				assert.Contains(t, err.Error(), "4.22.23", "the failure must include the expected OpenShift release")
			}
		})
	}
}

func TestKarpenterNodeClassVersionInput(t *testing.T) {
	for _, version := range []string{"4.22", "bad", "4.99.0", "5.0.0"} {
		err := VerifyKarpenterNodeClassVersion("autonode-e2e", version, NodeClassUpgradeEvidence{}, time.Minute).Verify(t.Context(), nil)
		require.Error(t, err, "invalid or unmapped versions must fail before any API request")
	}
	names := []string{"old-node"}
	claims := []string{"old-claim"}
	evidence := NodeClassUpgradeEvidence{
		PreviousNodeNames:      names,
		PreviousNodeClaimNames: claims,
		PreviousNodeClassHash:  "hash",
	}
	verifier := VerifyKarpenterNodeClassVersion("autonode-e2e", "4.22.23", evidence, time.Minute).(verifyKarpenterNodeClassVersion)
	names[0] = "changed"
	claims[0] = "changed"
	assert.Equal(t, []string{"old-node"}, verifier.evidence.PreviousNodeNames, "the verifier must keep its own copy of the old node names")
	assert.Equal(t, []string{"old-claim"}, verifier.evidence.PreviousNodeClaimNames, "the verifier must keep its own copy of the old NodeClaim names")
	assert.Equal(t, "hash", verifier.evidence.PreviousNodeClassHash, "the verifier must keep the previous NodeClass hash")
}
