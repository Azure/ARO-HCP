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
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type staticSubscriptionState string

func (s staticSubscriptionState) GetSubscriptionState(string) string {
	return string(s)
}

func TestMetricsMiddlewareClientClass(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		userAgent string
		wantClass string
	}{
		{
			name:      "other when unset",
			userAgent: "",
			wantClass: clientClassOther,
		},
		{
			name:      "aso+capz from observed UA",
			userAgent: "aso-controller/v2.13.0-hcpclusters.9 cluster-api-provider-azure/v1.22.1-mce-217",
			wantClass: clientClassASOCAPZ,
		},
		{
			name:      "capz only",
			userAgent: "cluster-api-provider-azure/v1.22.1-mce-217",
			wantClass: clientClassCAPZ,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reg := prometheus.NewRegistry()
			mm := NewMetricsMiddleware(reg, staticSubscriptionState("Registered"))
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
					assert.Equal(t, tt.wantClass, labelValue(t, mf.GetMetric()[0], "client_class"))
				case requestDurationName:
					sawDuration = true
					assert.Equal(t, tt.wantClass, labelValue(t, mf.GetMetric()[0], "client_class"))
				}
			}
			assert.True(t, sawCounter, "expected %s", requestCounterName)
			assert.True(t, sawDuration, "expected %s", requestDurationName)
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
