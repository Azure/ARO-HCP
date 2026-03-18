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

package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/go-logr/logr"
	"github.com/microsoft/go-otel-audit/audit/base"
	"github.com/microsoft/go-otel-audit/audit/conn"
	"github.com/microsoft/go-otel-audit/audit/msgs"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestEnsureDefaults(t *testing.T) {
	expected := `{"CallerIpAddress":"192.168.1.1","CallerIdentities":{"10":[{"Identity":"Unknown","Description":"Unknown"}]},"OperationCategories":[10],"TargetResources":{"Unknown":[{"Name":"Unknown","Cluster":"","DataCenter":"","Region":"Unknown"}]},"CallerAccessLevels":["Unknown"],"OperationAccessLevel":"Unknown","OperationName":"Unknown","OperationResultDescription":"","CallerAgent":"Unknown","OperationCategoryDescription":"","OperationType":0,"OperationResult":0}`

	record := &msgs.Record{}
	ensureDefaults(record)

	current, err := json.Marshal(record)
	require.NoError(t, err)

	assert.Equal(t, expected, string(current))
}

type mockClient struct {
	sendErr error
}

func (m *mockClient) Send(ctx context.Context, msg msgs.Msg, options ...base.SendOption) error {
	return m.sendErr
}

func TestAuditClientSendMetrics(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		sendErr      error
		expectTotal  float64
		expectErrors float64
	}{
		{
			name:         "successful send increments total only",
			sendErr:      nil,
			expectTotal:  1,
			expectErrors: 0,
		},
		{
			name:         "failed send increments both counters",
			sendErr:      fmt.Errorf("send failed"),
			expectTotal:  1,
			expectErrors: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reg := prometheus.NewRegistry()
			totalSend := promauto.With(reg).NewCounter(prometheus.CounterOpts{
				Name: MetricAuditLogRecordsTotal,
			})
			sendErrors := promauto.With(reg).NewCounter(prometheus.CounterOpts{
				Name: MetricAuditLogSendErrorsTotal,
			})
			client := &AuditClient{
				client:     &mockClient{sendErr: tt.sendErr},
				totalSend:  totalSend,
				sendErrors: sendErrors,
			}

			err := client.Send(t.Context(), msgs.Msg{})
			if tt.sendErr != nil {
				assert.ErrorIs(t, err, tt.sendErr)
			} else {
				assert.NoError(t, err)
			}

			metrics, err := reg.Gather()
			require.NoError(t, err)

			for _, mf := range metrics {
				switch mf.GetName() {
				case MetricAuditLogRecordsTotal:
					require.Len(t, mf.GetMetric(), 1)
					assert.Equal(t, tt.expectTotal, mf.GetMetric()[0].GetCounter().GetValue())
				case MetricAuditLogSendErrorsTotal:
					require.Len(t, mf.GetMetric(), 1)
					assert.Equal(t, tt.expectErrors, mf.GetMetric()[0].GetCounter().GetValue())
				}
			}
		})
	}
}

func TestNewOtelAuditClientConnectionFallback(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                 string
		connectSucceeds      bool
		expectDegradedMetric float64
	}{
		{
			name:                 "connect succeeds",
			connectSucceeds:      true,
			expectDegradedMetric: 0,
		},
		{
			name:                 "connect fails falls back to noop",
			connectSucceeds:      false,
			expectDegradedMetric: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := utils.ContextWithLogger(t.Context(), logr.Discard())
			reg := prometheus.NewRegistry()
			connectFunc := func() (conn.Audit, error) {
				if tt.connectSucceeds {
					return conn.NewNoOP(), nil
				}
				return nil, fmt.Errorf("connect failed")
			}
			client, err := NewOtelAuditClient(ctx, connectFunc, reg)
			require.NoError(t, err)
			require.NotNil(t, client)

			// Verify Send works (especially after fallback to noop conn).
			err = client.Send(t.Context(), msgs.Msg{Type: msgs.ControlPlane})
			require.NoError(t, err)

			// Verify all three metrics are registered and have expected values.
			metrics, err := reg.Gather()
			require.NoError(t, err)

			expectedMetrics := map[string]bool{
				MetricAuditLogConnectionDegraded: false,
				MetricAuditLogRecordsTotal:       false,
				MetricAuditLogSendErrorsTotal:    false,
			}
			for _, mf := range metrics {
				switch mf.GetName() {
				case MetricAuditLogConnectionDegraded:
					require.Len(t, mf.GetMetric(), 1)
					assert.Equal(t, tt.expectDegradedMetric, mf.GetMetric()[0].GetGauge().GetValue())
					expectedMetrics[MetricAuditLogConnectionDegraded] = true
				case MetricAuditLogRecordsTotal:
					require.Len(t, mf.GetMetric(), 1)
					assert.Equal(t, float64(1), mf.GetMetric()[0].GetCounter().GetValue())
					expectedMetrics[MetricAuditLogRecordsTotal] = true
				case MetricAuditLogSendErrorsTotal:
					expectedMetrics[MetricAuditLogSendErrorsTotal] = true
				}
			}
			for name, found := range expectedMetrics {
				assert.True(t, found, "metric %s not registered", name)
			}
		})
	}
}
