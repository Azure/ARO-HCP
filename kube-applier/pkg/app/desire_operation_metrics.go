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

package app

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
)

// Operation type constants for metrics labels
const (
	OperationApply  = "apply"
	OperationDelete = "delete"
	OperationResync = "resync"
)

// desireOperationMetrics tracks operation-level metrics for kube-applier desires.
// These metrics provide visibility into apply/delete/resync operations broken
// down by the GVR of the target Kubernetes object (group, resource) and the
// reconciliation outcome (condition status and reason). The GVR is the dimension
// the applier actually acts on — where RBAC, CRD availability, and admission
// webhook outcomes diverge — rather than the ARM ownership hierarchy the applier
// is agnostic to.
type desireOperationMetrics struct {
	operationsTotal        *prometheus.CounterVec
	operationLastTimestamp *prometheus.GaugeVec
}

func newDesireOperationMetrics(registerer prometheus.Registerer) *desireOperationMetrics {
	return &desireOperationMetrics{
		operationsTotal: promauto.With(registerer).NewCounterVec(
			prometheus.CounterOpts{
				Name: "kube_applier_desire_operations_total",
				Help: "Total number of desire operations completed, by operation type, target GVR (group, resource), condition status, and reason",
			},
			[]string{"operation", "group", "resource", "status", "reason"},
		),
		operationLastTimestamp: promauto.With(registerer).NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "kube_applier_desire_operation_last_timestamp_seconds",
				Help: "Unix timestamp of the last completed operation, by operation type and target GVR (group, resource)",
			},
			[]string{"operation", "group", "resource"},
		),
	}
}

// recordApplyDesireOperation records metrics for an ApplyDesire operation.
func (m *desireOperationMetrics) recordApplyDesireOperation(desire *kubeapplierapi.ApplyDesire) {
	// Determine operation type from the desire's spec.
	operation := OperationApply
	if desire.Spec.Type == kubeapplierapi.ApplyDesireTypeDelete {
		// ServerSideApply is always "apply" - no create/update distinction.
		operation = OperationDelete
	}

	status, reason := getConditionStatusAndReason(desire.Status.Conditions)
	m.recordOperation(operation, desire.Spec.TargetItem.Group, desire.Spec.TargetItem.Resource, status, reason)
}

// recordReadDesireOperation records metrics for a ReadDesire operation (resync).
func (m *desireOperationMetrics) recordReadDesireOperation(desire *kubeapplierapi.ReadDesire) {
	status, reason := getConditionStatusAndReason(desire.Status.Conditions)
	m.recordOperation(OperationResync, desire.Spec.TargetItem.Group, desire.Spec.TargetItem.Resource, status, reason)
}

// recordOperation is a helper that records common metrics to avoid duplication
// between recordApplyDesireOperation and recordReadDesireOperation.
func (m *desireOperationMetrics) recordOperation(operation, group, resource, status, reason string) {
	// Record operation count
	m.operationsTotal.With(prometheus.Labels{
		"operation": operation,
		"group":     group,
		"resource":  resource,
		"status":    status,
		"reason":    reason,
	}).Inc()

	// Record last operation timestamp
	m.operationLastTimestamp.With(prometheus.Labels{
		"operation": operation,
		"group":     group,
		"resource":  resource,
	}).SetToCurrentTime()
}

// getConditionStatusAndReason extracts the status and reason from the "Successful" condition.
// Returns the condition status (True/False/Unknown) and reason, or defaults if not found.
func getConditionStatusAndReason(conditions []metav1.Condition) (string, string) {
	for _, cond := range conditions {
		if cond.Type == kubeapplierapi.ConditionTypeSuccessful {
			return string(cond.Status), cond.Reason
		}
	}
	// If Successful condition not found, default to Unknown with empty reason
	return string(metav1.ConditionUnknown), ""
}
