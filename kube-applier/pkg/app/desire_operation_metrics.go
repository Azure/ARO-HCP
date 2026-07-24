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
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
)

// Operation type constants for metrics labels
const (
	OperationCreate = "create"
	OperationUpdate = "update"
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
// These metrics provide visibility into create/update/delete/resync operations
// broken down by resource type (cluster vs nodepool) and success status.
type desireOperationMetrics struct {
	operationsTotal         *prometheus.CounterVec
	operationDuration       *prometheus.HistogramVec
	operationLastTimestamp  *prometheus.GaugeVec
	lastProcessedGeneration map[string]int64 // tracks desire resourceID -> generation to detect changes
}

func newDesireOperationMetrics(registerer prometheus.Registerer) *desireOperationMetrics {
	return &desireOperationMetrics{
		operationsTotal: promauto.With(registerer).NewCounterVec(
			prometheus.CounterOpts{
				Name: "kube_applier_desire_operations_total",
				Help: "Total number of desire operations completed, by operation type, resource type, and success status",
			},
			[]string{"operation", "resource_type", "successful"},
		),
		operationDuration: promauto.With(registerer).NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "kube_applier_desire_operation_duration_seconds",
				Help:    "Duration of desire operations in seconds, by operation type and resource type",
				Buckets: []float64{0.1, 0.5, 1.0, 2.5, 5.0, 10.0, 30.0, 60.0, 120.0},
			},
			[]string{"operation", "resource_type"},
		),
		operationLastTimestamp: promauto.With(registerer).NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "kube_applier_desire_operation_last_timestamp_seconds",
				Help: "Unix timestamp of the last completed operation, by operation type and resource type",
			},
			[]string{"operation", "resource_type"},
		),
		lastProcessedGeneration: make(map[string]int64),
	}
}

// recordApplyDesireOperation records metrics for an ApplyDesire operation.
// It determines the operation type (create/update/delete) based on the desire's
// type, status, and previous state.
func (m *desireOperationMetrics) recordApplyDesireOperation(desire *kubeapplier.ApplyDesire) {
	resourceType := extractResourceType(desire.CosmosMetadata.ResourceID.String())
	operation := determineApplyOperation(desire, m.lastProcessedGeneration)
	successful := isDesireSuccessful(desire.Status.Conditions)

	// Record operation count
	m.operationsTotal.With(prometheus.Labels{
		"operation":     operation,
		"resource_type": resourceType,
		"successful":    formatBool(successful),
	}).Inc()

	// Record operation duration if we have timing information
	if duration := calculateOperationDuration(desire.Status.Conditions); duration > 0 {
		m.operationDuration.With(prometheus.Labels{
			"operation":     operation,
			"resource_type": resourceType,
		}).Observe(duration.Seconds())
	}

	// Record last operation timestamp
	m.operationLastTimestamp.With(prometheus.Labels{
		"operation":     operation,
		"resource_type": resourceType,
	}).SetToCurrentTime()

	// Update last processed instance version
	m.lastProcessedGeneration[desire.CosmosMetadata.ResourceID.String()] = desire.CosmosMetadata.InstanceVersion
}

// recordReadDesireOperation records metrics for a ReadDesire operation (resync).
func (m *desireOperationMetrics) recordReadDesireOperation(desire *kubeapplier.ReadDesire) {
	resourceType := extractResourceType(desire.CosmosMetadata.ResourceID.String())
	successful := isDesireSuccessful(desire.Status.Conditions)

	// ReadDesire operations are always "resync" operations
	m.operationsTotal.With(prometheus.Labels{
		"operation":     OperationResync,
		"resource_type": resourceType,
		"successful":    formatBool(successful),
	}).Inc()

	// Record operation duration if we have timing information
	if duration := calculateOperationDuration(desire.Status.Conditions); duration > 0 {
		m.operationDuration.With(prometheus.Labels{
			"operation":     OperationResync,
			"resource_type": resourceType,
		}).Observe(duration.Seconds())
	}

	// Record last operation timestamp
	m.operationLastTimestamp.With(prometheus.Labels{
		"operation":     OperationResync,
		"resource_type": resourceType,
	}).SetToCurrentTime()

	// Update last processed instance version
	m.lastProcessedGeneration[desire.CosmosMetadata.ResourceID.String()] = desire.CosmosMetadata.InstanceVersion
}

// extractResourceType parses the Cosmos resourceID to determine if this is a
// cluster or nodepool desire.
//
// ResourceID format:
//   subscriptions/{sub}/resourceGroups/{rg}/providers/microsoft.redhatopenshift/hcpopenshiftclusters/{name}/*desires/{desire}
//   subscriptions/{sub}/resourceGroups/{rg}/providers/microsoft.redhatopenshift/hcpopenshiftclusters/{name}/nodepools/{np}/*desires/{desire}
func extractResourceType(resourceID string) string {
	if strings.Contains(resourceID, "/nodepools/") {
		return ResourceTypeNodePool
	}
	if strings.Contains(resourceID, "/hcpopenshiftclusters/") {
		return ResourceTypeCluster
	}
	return ResourceTypeUnknown
}

// determineApplyOperation determines whether an ApplyDesire represents a create,
// update, or delete operation.
//
// Logic:
//   - If Type=Delete: operation is "delete"
//   - If Type=ServerSideApply and no previous instance version seen: operation is "create"
//   - If Type=ServerSideApply and previous instance version exists: operation is "update"
func determineApplyOperation(desire *kubeapplier.ApplyDesire, lastProcessed map[string]int64) string {
	if desire.Spec.Type == kubeapplier.ApplyDesireTypeDelete {
		return OperationDelete
	}

	// ServerSideApply: distinguish create vs update based on whether we've seen this desire before
	if _, seen := lastProcessed[desire.CosmosMetadata.ResourceID.String()]; seen {
		return OperationUpdate
	}

	return OperationCreate
}

// isDesireSuccessful checks if the "Successful" condition is True.
func isDesireSuccessful(conditions []metav1.Condition) bool {
	for _, cond := range conditions {
		if cond.Type == kubeapplier.ConditionTypeSuccessful {
			return cond.Status == metav1.ConditionTrue
		}
	}
	return false
}

// calculateOperationDuration calculates the duration between when the desire was
// last updated and when it was last transitioned to Successful status.
// Returns 0 if timing information is not available.
func calculateOperationDuration(conditions []metav1.Condition) time.Duration {
	for _, cond := range conditions {
		if cond.Type == kubeapplier.ConditionTypeSuccessful && cond.Status == metav1.ConditionTrue {
			// Use LastTransitionTime as a proxy for operation completion time
			// This is an approximation but provides useful latency metrics
			now := time.Now()
			transitionTime := cond.LastTransitionTime.Time
			if transitionTime.IsZero() {
				return 0
			}
			// If the transition was recent (within last collection interval), calculate duration
			if now.Sub(transitionTime) < desireCollectInterval {
				// Use the time since last transition as operation duration
				// This is approximate but provides useful visibility
				return now.Sub(transitionTime)
			}
		}
	}
	return 0
}

// formatBool converts a boolean to "true" or "false" string for Prometheus labels.
func formatBool(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
