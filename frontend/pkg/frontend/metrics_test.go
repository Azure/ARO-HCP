// Copyright 2025 Microsoft Corporation
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

package frontend

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

func TestMetricsMiddlewareUserAgent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		userAgent string
		wantLabel string
	}{
		{
			name:      "other when unset",
			userAgent: "",
			wantLabel: userAgentOther,
		},
		{
			name:      "aso+capz keeps product tokens with versions",
			userAgent: "azsdk-go-generic/v2.13.0-hcpclusters.9 (go1.24.13; linux) aso-controller/v2.13.0-hcpclusters.9 cluster-api-provider-azure/v1.22.1-mce-217",
			wantLabel: "aso-controller/v2.13.0-hcpclusters.9 cluster-api-provider-azure/v1.22.1-mce-217",
		},
		{
			name:      "capz only keeps product token",
			userAgent: "cluster-api-provider-azure/v1.22.1-mce-217",
			wantLabel: "cluster-api-provider-azure/v1.22.1-mce-217",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reg := prometheus.NewRegistry()
			mm := NewMetricsMiddleware(reg)
			middleware := mm.Metrics()

			req := httptest.NewRequest(http.MethodGet, "/subscriptions/00000000-0000-0000-0000-000000000000?api-version=2024-01-01", nil)
			if tt.userAgent != "" {
				req.Header.Set("User-Agent", tt.userAgent)
			}
			rr := httptest.NewRecorder()
			middleware(rr, req, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			})

			require.Equal(t, http.StatusOK, rr.Code)

			metrics, err := reg.Gather()
			require.NoError(t, err)

			var sawCounter, sawDuration bool
			for _, mf := range metrics {
				switch mf.GetName() {
				case requestCounterName:
					sawCounter = true
					assert.Equal(t, tt.wantLabel, labelValue(t, mf.GetMetric()[0], "user_agent"))
				case requestDurationName:
					sawDuration = true
					assert.Equal(t, tt.wantLabel, labelValue(t, mf.GetMetric()[0], "user_agent"))
				}
			}
			assert.True(t, sawCounter, "expected %s", requestCounterName)
			assert.True(t, sawDuration, "expected %s", requestDurationName)
		})
	}
}

func TestEmitExternalAuthStateTransition(t *testing.T) {
	tests := []struct {
		name            string
		oldState        coreapi.ProvisioningState
		newState        coreapi.ProvisioningState
		resourceType    string
		expectIncrement bool
		expectFromState string
		expectToState   string
	}{
		{
			name:            "update: Succeeded to Accepted",
			oldState:        coreapi.ProvisioningStateSucceeded,
			newState:        coreapi.ProvisioningStateAccepted,
			resourceType:    coreapi.ExternalAuthResourceType.String(),
			expectIncrement: true,
			expectFromState: "succeeded",
			expectToState:   "accepted",
		},
		{
			// NewOperation forces OperationRequestDelete to ProvisioningStateDeleting
			// (see addDeleteExternalAuthToTransaction), so this is the real delete
			// transition, distinct from update's "accepted" target above.
			name:            "delete: Failed to Deleting",
			oldState:        coreapi.ProvisioningStateFailed,
			newState:        coreapi.ProvisioningStateDeleting,
			resourceType:    coreapi.ExternalAuthResourceType.String(),
			expectIncrement: true,
			expectFromState: "failed",
			expectToState:   "deleting",
		},
		{
			name:         "same state does not increment",
			oldState:     coreapi.ProvisioningStateSucceeded,
			newState:     coreapi.ProvisioningStateSucceeded,
			resourceType: coreapi.ExternalAuthResourceType.String(),
		},
		{
			name:         "empty old state does not increment",
			oldState:     "",
			newState:     coreapi.ProvisioningStateAccepted,
			resourceType: coreapi.ExternalAuthResourceType.String(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			externalAuthStateTransitionsTotal.Reset()

			emitExternalAuthStateTransition(tc.oldState, tc.newState, tc.resourceType)

			if tc.expectIncrement {
				count := testutil.ToFloat64(externalAuthStateTransitionsTotal.WithLabelValues(
					tc.expectFromState,
					tc.expectToState,
					strings.ToLower(tc.resourceType),
				))
				assert.Equal(t, 1.0, count,
					"expected counter=1 for from_state=%s, to_state=%s",
					tc.expectFromState, tc.expectToState)
			} else {
				assert.Equal(t, 0, testutil.CollectAndCount(externalAuthStateTransitionsTotal),
					"expected no counter series")
			}
		})
	}
}

func labelValue(t *testing.T, m *dto.Metric, name string) string {
	t.Helper()
	for _, l := range m.GetLabel() {
		if l.GetName() == name {
			return l.GetValue()
		}
	}
	t.Fatalf("label %q not found", name)
	return ""
}
