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
	require.NoError(t, ValidateKarpenterInstanceTypes(instanceTypes))
	require.NoError(t, ApplyOpenShiftAzureNodeClass(t.Context(), config, "autonode-e2e"))
	require.NoError(t, ApplyKarpenterNodePool(t.Context(), config, "autonode-e2e", instanceTypes))

	nodeClass := <-requests
	assert.Equal(t, http.MethodPost, nodeClass.method)
	assert.Equal(t, "/apis/karpenter.hypershift.openshift.io/v1alpha1/openshiftazurenodeclasses", nodeClass.path)
	assert.Equal(t, map[string]any{
		"apiVersion": "karpenter.hypershift.openshift.io/v1alpha1", "kind": "OpenShiftAzureNodeClass",
		"metadata": map[string]any{"name": "autonode-e2e"}, "spec": map[string]any{"osDiskSizeGiB": float64(128)},
	}, nodeClass.object, "the operator must select the image, subnet, and user data")
	pool := <-requests
	assert.Equal(t, http.MethodPost, pool.method)
	assert.Equal(t, "/apis/karpenter.sh/v1/nodepools", pool.path)
	assert.Equal(t, "karpenter.sh/v1", pool.object["apiVersion"])
	assert.Equal(t, "NodePool", pool.object["kind"])
	template := pool.object["spec"].(map[string]any)["template"].(map[string]any)
	assert.Equal(t, map[string]any{"labels": map[string]any{"autonode": "true"}}, template["metadata"], "restricted Karpenter labels must not be set")
	spec := template["spec"].(map[string]any)
	assert.Equal(t, map[string]any{"group": "karpenter.azure.com", "kind": "AKSNodeClass", "name": "autonode-e2e"}, spec["nodeClassRef"])
	assert.Contains(t, spec["requirements"], map[string]any{"key": "node.kubernetes.io/instance-type", "operator": "In", "values": []any{"Standard_D4s_v3", "Standard_D4s_v5"}})
	assert.Contains(t, spec["requirements"], map[string]any{"key": "karpenter.sh/capacity-type", "operator": "In", "values": []any{"on-demand"}})
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
	require.ErrorContains(t, apply(nil), "instanceTypes must not be empty")
	require.ErrorContains(t, ApplyKarpenterNodePool(t.Context(), config, "autonode-e2e", nil), "instanceTypes must not be empty")
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
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Len(t, requests, tc.wantRequests)
			first := <-requests
			assert.Equal(t, http.MethodDelete, first.method)
			assert.Equal(t, "/apis/karpenter.sh/v1/nodepools/autonode-e2e", first.path)
			if tc.wantRequests == 2 {
				second := <-requests
				assert.Equal(t, http.MethodDelete, second.method)
				assert.Equal(t, "/apis/karpenter.hypershift.openshift.io/v1alpha1/openshiftazurenodeclasses/autonode-e2e", second.path)
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
	require.ErrorContains(t, ApplyOpenShiftAzureNodeClass(t.Context(), config, "autonode-e2e"), "failed to create OpenShiftAzureNodeClass")
	require.ErrorContains(t, ApplyKarpenterNodePool(t.Context(), config, "autonode-e2e", []string{"vm"}), "failed to create NodePool")
}
