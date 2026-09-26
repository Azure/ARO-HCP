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

package otelaudit

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/microsoft/go-otel-audit/audit"
	"github.com/microsoft/go-otel-audit/audit/conn"
	"github.com/microsoft/go-otel-audit/audit/msgs"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

func TestAuditDisabledDoesNotRequireIdentity(t *testing.T) {
	registry := prometheus.NewRegistry()
	client, err := NewOtelAuditClient(t.Context(), false, uuid.Nil, registry)
	require.NoError(t, err)
	require.NoError(t, client.Send(t.Context(), msgs.Msg{Type: msgs.ControlPlane}))
	require.Equal(t, float64(1), auditMetric(t, registry, MetricAuditLogRecordsTotal))
	require.Zero(t, auditMetric(t, registry, MetricAuditLogSendErrorsTotal))
	require.Zero(t, auditMetric(t, registry, MetricAuditLogConnectionDegraded))
}

func TestAuditRequiresServiceIdentity(t *testing.T) {
	_, err := NewOtelAuditClient(t.Context(), true, uuid.Nil, prometheus.NewRegistry())
	require.ErrorIs(t, err, audit.ErrValidation)
}

func TestAuditClientSendMetrics(t *testing.T) {
	// The upstream client has no Close API. Isolate its process-lifetime workers.
	if os.Getenv("ARO_HCP_AUDIT_TEST") != t.Name() {
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^"+t.Name()+"$", "-test.timeout=15s")
		command.Env = append(os.Environ(), "ARO_HCP_AUDIT_TEST="+t.Name())
		output, err := command.CombinedOutput()
		require.NoError(t, err, "%s", output)
		return
	}
	registry := prometheus.NewRegistry()
	client, err := newOtelAuditClient(t.Context(), uuid.MustParse("11111111-1111-4111-8111-111111111111"), func() (conn.Audit, error) {
		return conn.NewNoOP(), nil
	}, registry)
	require.NoError(t, err)

	// Enabled forwarding must still validate records and account for genuine
	// submission errors, even when the transport itself discards records.
	require.NoError(t, client.Send(t.Context(), msgs.Msg{Type: msgs.ControlPlane}))
	require.ErrorIs(t, client.Send(t.Context(), msgs.Msg{}), audit.ErrValidation)
	require.Equal(t, float64(2), auditMetric(t, registry, MetricAuditLogRecordsTotal))
	require.Equal(t, float64(1), auditMetric(t, registry, MetricAuditLogSendErrorsTotal))
}

func auditMetric(t *testing.T, registry *prometheus.Registry, name string) float64 {
	t.Helper()
	families, err := registry.Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() == name {
			metric := family.Metric[0]
			if metric.Counter != nil {
				return metric.Counter.GetValue()
			}
			return metric.GetGauge().GetValue()
		}
	}
	t.Fatalf("metric %s not found", name)
	return 0
}
