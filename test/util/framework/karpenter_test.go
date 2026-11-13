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

package framework

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

type karpenterRequest struct {
	method, path string
	object       map[string]any
}

func TestApplyKarpenterResources(t *testing.T) {
	requests := make(chan karpenterRequest, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var object map[string]any
		if err := json.NewDecoder(r.Body).Decode(&object); err != nil {
			t.Errorf("decode resource: %v", err)
		}
		requests <- karpenterRequest{method: r.Method, path: r.URL.Path, object: object}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(object); err != nil {
			t.Errorf("encode resource: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	config := &rest.Config{Host: server.URL}
	instanceTypes := []string{"Standard_D4s_v3", "Standard_D4s_v5"}
	require.NoError(t, ValidateKarpenterInstanceTypes(instanceTypes), "the test instance types must be valid before resource creation")
	require.NoError(t, ApplyOpenShiftAzureNodeClass(t.Context(), config, "autonode-e2e"), "the test must create an operator-managed NodeClass")
	require.NoError(t, ApplyKarpenterNodePool(t.Context(), config, "autonode-e2e", instanceTypes), "the four-argument call must create the default NodePool")

	nodeClass := <-requests
	assert.Equal(t, http.MethodPost, nodeClass.method, "the NodeClass must be created with POST")
	assert.Equal(t, "/apis/karpenter.hypershift.openshift.io/v1alpha1/openshiftazurenodeclasses", nodeClass.path, "the test must create the canonical OpenShiftAzureNodeClass")
	assert.Equal(t, map[string]any{
		"apiVersion": "karpenter.hypershift.openshift.io/v1alpha1", "kind": "OpenShiftAzureNodeClass",
		"metadata": map[string]any{"name": "autonode-e2e"}, "spec": map[string]any{"osDiskSizeGiB": float64(128)},
	}, nodeClass.object, "the operator must select the image, subnet, and user data")
	pool := <-requests
	assert.Equal(t, map[string]any{
		"apiVersion": "karpenter.sh/v1", "kind": "NodePool", "metadata": map[string]any{"name": "autonode-e2e"},
		"spec": map[string]any{"template": map[string]any{
			"metadata": map[string]any{"labels": map[string]any{"autonode": "true"}},
			"spec": map[string]any{
				"expireAfter":  "720h",
				"nodeClassRef": map[string]any{"group": "karpenter.azure.com", "kind": "AKSNodeClass", "name": "autonode-e2e"},
				"requirements": []any{
					map[string]any{"key": "kubernetes.io/arch", "operator": "In", "values": []any{"amd64"}},
					map[string]any{"key": "kubernetes.io/os", "operator": "In", "values": []any{"linux"}},
					map[string]any{"key": "karpenter.sh/capacity-type", "operator": "In", "values": []any{"on-demand"}},
					map[string]any{"key": "node.kubernetes.io/instance-type", "operator": "In", "values": []any{"Standard_D4s_v3", "Standard_D4s_v5"}},
				},
			},
		}},
	}, pool.object, "the four-argument call must preserve the complete default NodePool creation payload")
	assert.Equal(t, http.MethodPost, pool.method, "the NodePool must be created with POST")
	assert.Equal(t, "/apis/karpenter.sh/v1/nodepools", pool.path, "the test must use the Karpenter v1 NodePool API")
	assert.Equal(t, "karpenter.sh/v1", pool.object["apiVersion"], "the NodePool must declare the served API version")
	assert.Equal(t, "NodePool", pool.object["kind"], "the payload must declare the NodePool resource kind")
	template := pool.object["spec"].(map[string]any)["template"].(map[string]any)
	assert.Equal(t, map[string]any{"labels": map[string]any{"autonode": "true"}}, template["metadata"], "restricted Karpenter labels must not be set")
	spec := template["spec"].(map[string]any)
	assert.NotContains(t, spec, "startupTaints", "the default NodePool payload must not declare startup taints")
	assert.Equal(t, map[string]any{"group": "karpenter.azure.com", "kind": "AKSNodeClass", "name": "autonode-e2e"}, spec["nodeClassRef"], "the NodePool must reference the operator-owned Azure NodeClass")
	assert.Contains(t, spec["requirements"], map[string]any{"key": "node.kubernetes.io/instance-type", "operator": "In", "values": []any{"Standard_D4s_v3", "Standard_D4s_v5"}}, "the NodePool must use the requested instance types")
	assert.Contains(t, spec["requirements"], map[string]any{"key": "karpenter.sh/capacity-type", "operator": "In", "values": []any{"on-demand"}}, "the NodePool must request on-demand capacity")
}

func TestApplyKarpenterNodePoolStartupTaints(t *testing.T) {
	requests := make(chan karpenterRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var object map[string]any
		if err := json.NewDecoder(r.Body).Decode(&object); err != nil {
			t.Errorf("decode NodePool creation payload: %v", err)
		}
		requests <- karpenterRequest{method: r.Method, path: r.URL.Path, object: object}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(object); err != nil {
			t.Errorf("encode NodePool creation response: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	taints := []corev1.Taint{
		{Key: "node.cilium.io/agent-not-ready", Value: "true", Effect: corev1.TaintEffectNoExecute, TimeAdded: &metav1.Time{}},
		{Key: "node.cilium.io/agent-not-ready", Effect: corev1.TaintEffectNoSchedule},
	}
	option := WithKarpenterStartupTaints(taints...)
	taints[0].Key = "caller-mutated"
	require.NoError(t, ApplyKarpenterNodePool(t.Context(), &rest.Config{Host: server.URL}, "autonode-e2e", []string{"vm"}, option), "startup taints must be included in the creation request")
	request := <-requests
	assert.Equal(t, http.MethodPost, request.method, "startup taints must be declared before CREATE, without a PATCH")
	spec := request.object["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
	entries := spec["startupTaints"].([]any)
	require.Len(t, entries, 2, "both startup taint effects must be declared")
	assert.Equal(t, map[string]any{"key": "node.cilium.io/agent-not-ready", "value": "true", "effect": "NoExecute"}, entries[0], "only key, value, and effect may be serialized from the copied input")
	assert.Equal(t, map[string]any{"key": "node.cilium.io/agent-not-ready", "effect": "NoSchedule"}, entries[1], "the no-value taint must retain its key and effect")
	assert.NotContains(t, entries[1].(map[string]any), "value", "an explicit empty value is rejected by the NodePool CRD")
	assert.Empty(t, requests, "startup taints must not require a second API request")
}

func TestDeployPendingWorkloadAntiAffinity(t *testing.T) {
	const (
		namespace = "autonode-e2e-workload"
		name      = "autonode-e2e-pending"
	)
	selector := map[string]string{"autonode": "true"}

	t.Run("default payload has no anti-affinity", func(t *testing.T) {
		deployment := newPendingWorkloadDeployment(namespace, name, selector, pendingWorkloadOptions{})
		require.NotNil(t, deployment, "the builder must return a Deployment")
		assert.Equal(t, int32(0), *deployment.Spec.Replicas, "the workload must start at 0 replicas")
		assert.Equal(t, selector, deployment.Spec.Template.Spec.NodeSelector, "the pod template must use the given node selector")
		assert.Nil(t, deployment.Spec.Template.Spec.Affinity, "the default workload must not constrain scheduling")
	})

	t.Run("replicas spread across nodes", func(t *testing.T) {
		deployment := newPendingWorkloadDeployment(namespace, name, selector, pendingWorkloadOptions{antiAffinityOnHostname: true})
		affinity := deployment.Spec.Template.Spec.Affinity
		require.NotNil(t, affinity, "the option must add an affinity rule")
		require.NotNil(t, affinity.PodAntiAffinity, "the option must add an anti-affinity rule")
		terms := affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution
		require.Len(t, terms, 1, "one rule must keep replicas on different nodes")
		assert.Equal(t, corev1.LabelHostname, terms[0].TopologyKey, "replicas must be spread by host name")
		require.NotNil(t, terms[0].LabelSelector, "the rule must select pods by label")
		assert.Equal(t, map[string]string{"app": name}, terms[0].LabelSelector.MatchLabels, "the rule must select this workload only")
	})
}

func TestKarpenterEmptyInstanceTypesMakeNoRequests(t *testing.T) {
	requests := make(chan struct{}, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests <- struct{}{}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	config := &rest.Config{Host: server.URL}
	// Use the caller's split sequence. Invalid requirements must fail before POST.
	apply := func(instanceTypes []string) error {
		if err := ValidateKarpenterInstanceTypes(instanceTypes); err != nil {
			return err
		}
		if err := ApplyOpenShiftAzureNodeClass(t.Context(), config, "autonode-e2e"); err != nil {
			return err
		}
		return ApplyKarpenterNodePool(t.Context(), config, "autonode-e2e", instanceTypes)
	}
	require.ErrorContains(t, apply(nil), "instanceTypes must not be empty", "the split creation sequence must reject empty instance types before any writes")
	require.ErrorContains(t, ApplyKarpenterNodePool(t.Context(), config, "autonode-e2e", nil), "instanceTypes must not be empty", "NodePool creation must reject empty instance types")
	assert.Empty(t, requests, "validation must prevent every API request")
}

func TestDeleteKarpenterResources(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		poolStatus, classStatus int
		wantError               bool
		wantRequests            int
	}{
		{name: "accepted", poolStatus: 200, classStatus: 200, wantRequests: 2},
		{name: "already absent", poolStatus: 404, classStatus: 404, wantRequests: 2},
		{name: "pool error", poolStatus: 500, classStatus: 200, wantError: true, wantRequests: 1},
		{name: "class forbidden", poolStatus: 200, classStatus: 403, wantError: true, wantRequests: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := make(chan karpenterRequest, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests <- karpenterRequest{method: r.Method, path: r.URL.Path}
				status := tc.classStatus
				if r.URL.Path == "/apis/karpenter.sh/v1/nodepools/autonode-e2e" {
					status = tc.poolStatus
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				if err := json.NewEncoder(w).Encode(map[string]any{"apiVersion": "v1", "kind": "Status", "code": status}); err != nil {
					t.Errorf("encode status: %v", err)
				}
			}))
			t.Cleanup(server.Close)
			err := DeleteOpenShiftAzureNodeClassAndNodePool(t.Context(), &rest.Config{Host: server.URL}, "autonode-e2e")
			if tc.wantError {
				require.Error(t, err, "resource deletion must report an API failure")
			} else {
				require.NoError(t, err, "resource deletion must accept successful and not-found responses")
			}
			require.Len(t, requests, tc.wantRequests, "resource deletion must stop at the first API failure")
			first := <-requests
			assert.Equal(t, http.MethodDelete, first.method, "the NodePool must be deleted with DELETE")
			assert.Equal(t, "/apis/karpenter.sh/v1/nodepools/autonode-e2e", first.path, "the NodePool must be deleted before its NodeClass")
			if tc.wantRequests == 2 {
				second := <-requests
				assert.Equal(t, http.MethodDelete, second.method, "the NodeClass must be deleted with DELETE")
				assert.Equal(t, "/apis/karpenter.hypershift.openshift.io/v1alpha1/openshiftazurenodeclasses/autonode-e2e", second.path, "deletion must target the canonical NodeClass, not the provider-owned object")
			}
		})
	}
}

func TestApplyKarpenterResourceErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		if err := json.NewEncoder(w).Encode(map[string]any{"apiVersion": "v1", "kind": "Status", "code": 403, "message": "test denied"}); err != nil {
			t.Errorf("encode error: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	config := &rest.Config{Host: server.URL}
	require.ErrorContains(t, ApplyOpenShiftAzureNodeClass(t.Context(), config, "autonode-e2e"), "failed to create OpenShiftAzureNodeClass", "NodeClass creation failures must identify the attempted resource")
	require.ErrorContains(t, ApplyKarpenterNodePool(t.Context(), config, "autonode-e2e", []string{"vm"}), "failed to create NodePool", "NodePool creation failures must identify the attempted resource")
}
