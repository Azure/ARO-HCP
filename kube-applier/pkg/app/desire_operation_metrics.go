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
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
)

// Operation type constants for metrics labels
const (
	OperationApply  = "apply"
	OperationDelete = "delete"
	OperationResync = "resync"
)

// Resource type constants for metrics labels
const (
	ResourceTypeCluster  = "cluster"
	ResourceTypeNodePool = "nodepool"
	ResourceTypeUnknown  = "unknown"
)

// desireOperationMetrics tracks operation-level metrics for kube-applier desires.
// These metrics provide visibility into apply/delete/resync operations
// broken down by resource type (cluster vs nodepool) and success status.
type desireOperationMetrics struct {
	operationsTotal        *prometheus.CounterVec
	operationLastTimestamp *prometheus.GaugeVec
}

func newDesireOperationMetrics(registerer prometheus.Registerer) *desireOperationMetrics {
	return &desireOperationMetrics{
		operationsTotal: promauto.With(registerer).NewCounterVec(
			prometheus.CounterOpts{
				Name: "kube_applier_desire_operations_total",
				Help: "Total number of desire operations completed, by operation type, resource type, condition status, and reason",
			},
			[]string{"operation", "resource_type", "status", "reason"},
		),
		operationLastTimestamp: promauto.With(registerer).NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "kube_applier_desire_operation_last_timestamp_seconds",
				Help: "Unix timestamp of the last completed operation, by operation type and resource type",
			},
			[]string{"operation", "resource_type"},
		),
	}
}

// recordApplyDesireOperation records metrics for an ApplyDesire operation.
func (m *desireOperationMetrics) recordApplyDesireOperation(desire *kubeapplier.ApplyDesire) {
	// Guard against nil ResourceID
	if desire.ResourceID == nil {
		return
	}

	resourceType := extractResourceType(desire.ResourceID.String())

	// Determine operation type from the desire's spec
	var operation string
	if desire.Spec.Type == kubeapplier.ApplyDesireTypeDelete {
		operation = OperationDelete
	} else {
		// ServerSideApply is always "apply" - no create/update distinction
		operation = OperationApply
	}

	status, reason := getConditionStatusAndReason(desire.Status.Conditions)
	m.recordOperation(operation, resourceType, status, reason)
}

// recordReadDesireOperation records metrics for a ReadDesire operation (resync).
func (m *desireOperationMetrics) recordReadDesireOperation(desire *kubeapplier.ReadDesire) {
	// Guard against nil ResourceID
	if desire.ResourceID == nil {
		return
	}

	resourceType := extractResourceType(desire.ResourceID.String())
	status, reason := getConditionStatusAndReason(desire.Status.Conditions)
	m.recordOperation(OperationResync, resourceType, status, reason)
}

// recordOperation is a helper that records common metrics to avoid duplication
// between recordApplyDesireOperation and recordReadDesireOperation.
func (m *desireOperationMetrics) recordOperation(operation, resourceType, status, reason string) {
	// Record operation count
	m.operationsTotal.With(prometheus.Labels{
		"operation":     operation,
		"resource_type": resourceType,
		"status":        status,
		"reason":        reason,
	}).Inc()

	// Record last operation timestamp
	m.operationLastTimestamp.With(prometheus.Labels{
		"operation":     operation,
		"resource_type": resourceType,
	}).SetToCurrentTime()
}

// extractResourceType parses the Cosmos resourceID to determine if this is a
// cluster or nodepool desire using Azure SDK ResourceID parsing.
//
// ResourceID format:
//
//	subscriptions/{sub}/resourceGroups/{rg}/providers/microsoft.redhatopenshift/hcpopenshiftclusters/{name}/*desires/{desire}
//	subscriptions/{sub}/resourceGroups/{rg}/providers/microsoft.redhatopenshift/hcpopenshiftclusters/{name}/nodepools/{np}/*desires/{desire}
func extractResourceType(resourceIDStr string) string {
	parsed, err := azcorearm.ParseResourceID(resourceIDStr)
	if err != nil {
		return ResourceTypeUnknown
	}

	// Check the ResourceType to determine cluster vs nodepool
	resourceTypeStr := strings.ToLower(parsed.ResourceType.String())
	if strings.Contains(resourceTypeStr, "nodepools") {
		return ResourceTypeNodePool
	}
	if strings.Contains(resourceTypeStr, "hcpopenshiftclusters") {
		return ResourceTypeCluster
	}

	return ResourceTypeUnknown
}


// getConditionStatusAndReason extracts the status and reason from the "Successful" condition.
// Returns the condition status (True/False/Unknown) and reason, or defaults if not found.
func getConditionStatusAndReason(conditions []metav1.Condition) (string, string) {
	for _, cond := range conditions {
		if cond.Type == kubeapplier.ConditionTypeSuccessful {
			return string(cond.Status), cond.Reason
		}
	}
	// If Successful condition not found, default to Unknown with empty reason
	return string(metav1.ConditionUnknown), ""
}
