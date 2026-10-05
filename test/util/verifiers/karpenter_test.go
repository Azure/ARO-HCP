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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
)

func writeKarpenterResponse(t *testing.T, w http.ResponseWriter, status int, object map[string]any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(object); err != nil {
		t.Errorf("encode response: %v", err)
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
					assert.Equal(t, "/apis/karpenter.hypershift.openshift.io/v1alpha1/openshiftazurenodeclasses/autonode-e2e", r.URL.Path)
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
				require.NoError(t, err)
				assert.Empty(t, providerReads, "provider readiness must not be a second gate")
			} else {
				require.ErrorContains(t, err, "OpenShiftAzureNodeClass")
				assert.Contains(t, err.Error(), "autonode-e2e")
				assert.NotEmpty(t, providerReads, "timeout must collect read-only provider diagnostics")
				if state != "missing" {
					assert.Contains(t, err.Error(), "VersionPending")
					assert.Contains(t, err.Error(), "waiting for ignition")
				}
			}
		})
	}
}

func TestKarpenterCRDsIncludeOpenShiftAzureNodeClass(t *testing.T) {
	assert.Contains(t, karpenterGuestCRDs, schema.GroupVersionResource{
		Group: "karpenter.hypershift.openshift.io", Version: "v1alpha1", Resource: "openshiftazurenodeclasses",
	})
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
				assert.Equal(t, http.MethodGet, r.Method)
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
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.wantError)
			}
		})
	}
}
