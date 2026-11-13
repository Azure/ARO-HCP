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
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/client-go/rest"
)

func TestUpdateOpenShiftAzureNodeClassVersion(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				assert.Equal(t, http.MethodPatch, r.Method, "the update must patch the existing NodeClass")
				assert.Equal(t, "/apis/karpenter.hypershift.openshift.io/v1alpha1/openshiftazurenodeclasses/autonode-e2e", r.URL.Path, "the update must use the customer NodeClass API")
				assert.Equal(t, "application/merge-patch+json", r.Header.Get("Content-Type"), "the patch must preserve other fields")
				var body map[string]any
				assert.NoError(t, json.NewDecoder(r.Body).Decode(&body), "the patch must contain valid JSON")
				assert.Equal(t, map[string]any{"spec": map[string]any{"version": "4.22.23"}}, body, "the patch must change only spec.version")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				if status == http.StatusOK {
					_, _ = w.Write([]byte(`{"apiVersion":"karpenter.hypershift.openshift.io/v1alpha1","kind":"OpenShiftAzureNodeClass","metadata":{"name":"autonode-e2e"}}`))
				} else {
					_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"Status","status":"Failure","reason":"Forbidden","code":403}`))
				}
			}))
			t.Cleanup(server.Close)
			err := UpdateOpenShiftAzureNodeClassVersion(t.Context(), &rest.Config{Host: server.URL}, "autonode-e2e", "4.22.23")
			if status == http.StatusOK {
				require.NoError(t, err, "a successful patch must return no error")
			} else {
				require.ErrorContains(t, err, `failed to set OpenShiftAzureNodeClass "autonode-e2e" spec.version to "4.22.23"`, "an API failure must identify the NodeClass and the target version")
			}
			assert.Equal(t, int32(1), requests.Load(), "the update must not recreate resources or delete nodes")
		})
	}
}

func TestUpdateOpenShiftAzureNodeClassVersionRejectsInvalidInput(t *testing.T) {
	for _, version := range []string{"", "4.22", "not-a-version", "4.22.0-" + strings.Repeat("a", 64)} {
		err := UpdateOpenShiftAzureNodeClassVersion(t.Context(), nil, "autonode-e2e", version)
		require.ErrorContains(t, err, "must be a full semantic version", "invalid versions must fail before an API request")
	}
	err := UpdateOpenShiftAzureNodeClassVersion(t.Context(), nil, "", "4.22.23")
	require.ErrorContains(t, err, "name must not be empty", "an empty name must fail before an API request")
}
