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

package metricscontrollers

import (
	"context"
	"strings"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/Azure/ARO-HCP/internal/api"
)

var operationMetricLabelNames = []string{"resource_id", "subscription_id", "resource_type", "operation_type", "phase"}

type operationPhaseMetricsHandler struct {
	phaseInfo          *prometheus.GaugeVec
	startTime          *prometheus.GaugeVec
	lastTransitionTime *prometheus.GaugeVec
	// phaseEnterTime preserves one series per phase an operation has visited,
	// in contrast to the gauges above which only carry the current phase.
	phaseEnterTime *prometheus.GaugeVec
}

// NewOperationPhaseMetricsHandler creates a metrics handler for operation metrics.
func NewOperationPhaseMetricsHandler(r prometheus.Registerer) Handler[*api.Operation] {
	h := &operationPhaseMetricsHandler{
		phaseInfo: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "backend_resource_operation_phase_info",
			Help: "Current phase of each operation (value is always 1).",
		}, operationMetricLabelNames),
		startTime: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "backend_resource_operation_start_time_seconds",
			Help: "Unix timestamp when the operation started.",
		}, operationMetricLabelNames),
		lastTransitionTime: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "backend_resource_operation_last_transition_time_seconds",
			Help: "Unix timestamp when the operation last changed phase.",
		}, operationMetricLabelNames),
		phaseEnterTime: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "backend_resource_operation_phase_enter_time_seconds",
			Help: "Unix timestamp when the operation first entered each phase. One series per phase visited.",
		}, operationMetricLabelNames),
	}
	r.MustRegister(h.phaseInfo, h.startTime, h.lastTransitionTime, h.phaseEnterTime)
	return h
}

func (h *operationPhaseMetricsHandler) Sync(_ context.Context, op *api.Operation) {
	resourceID := resourceIDMetricLabel(op.GetResourceID())
	if len(resourceID) == 0 {
		return
	}
	subscriptionID := subscriptionIDMetricLabel(op.GetResourceID())
	if op.OperationID == nil {
		h.Delete(resourceID)
		return
	}

	// Clear any previous series for this resource before writing the current labels.
	// Phase and operation labels are part of the metric identity, so updates would
	// otherwise leave stale series behind for older label combinations.
	h.Delete(resourceID)

	labels := prometheus.Labels{
		"resource_id":     resourceID,
		"subscription_id": subscriptionID,
		"resource_type":   resourceIDToTypeMetricLabel(op.ExternalID),
		"operation_type":  operationTypeMetricLabel(op.Request),
		"phase":           phaseMetricLabel(op.Status),
	}
	h.phaseInfo.With(labels).Set(1.0)

	if !op.StartTime.IsZero() {
		h.startTime.With(labels).Set(float64(op.StartTime.Unix()))
	}
	if !op.LastTransitionTime.IsZero() {
		h.lastTransitionTime.With(labels).Set(float64(op.LastTransitionTime.Unix()))
	}

	// Emit one series per recorded phase so duration queries can subtract
	// timestamps across phases. The phase label here reflects the recorded
	// phase, not the current one, so these series intentionally diverge from
	// phaseInfo above.
	for phase, at := range op.PhaseTimestamps {
		if at.IsZero() {
			continue
		}
		phaseLabels := prometheus.Labels{
			"resource_id":     resourceID,
			"subscription_id": subscriptionID,
			"resource_type":   resourceIDToTypeMetricLabel(op.ExternalID),
			"operation_type":  operationTypeMetricLabel(op.Request),
			"phase":           phaseMetricLabel(phase),
		}
		h.phaseEnterTime.With(phaseLabels).Set(float64(at.Unix()))
	}
}

func (h *operationPhaseMetricsHandler) Delete(key string) {
	if len(key) == 0 {
		return
	}

	deleteSelector := prometheus.Labels{"resource_id": key}
	h.phaseInfo.DeletePartialMatch(deleteSelector)
	h.startTime.DeletePartialMatch(deleteSelector)
	h.lastTransitionTime.DeletePartialMatch(deleteSelector)
	h.phaseEnterTime.DeletePartialMatch(deleteSelector)
}

func operationTypeMetricLabel(request api.OperationRequest) string {
	return strings.ToLower(string(request))
}
