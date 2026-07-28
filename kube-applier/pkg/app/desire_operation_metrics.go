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
	"strconv"
	"strings"
	"sync"

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
	operationLastTimestamp  *prometheus.GaugeVec
	mu                      sync.RWMutex
	lastProcessedGeneration map[string]int64 // tracks desire resourceID -> instance version to detect changes
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
	// Guard against nil ResourceID
	if desire.CosmosMetadata.ResourceID == nil {
		return
	}

	resourceIDStr := desire.CosmosMetadata.ResourceID.String()
	resourceType := extractResourceType(resourceIDStr)

	m.mu.Lock()
	operation := determineApplyOperation(desire, m.lastProcessedGeneration)
	// Update last processed instance version while holding lock
	m.lastProcessedGeneration[resourceIDStr] = desire.CosmosMetadata.InstanceVersion
	m.mu.Unlock()

	successful := isDesireSuccessful(desire.Status.Conditions)

	// Record metrics (extracted to helper to avoid duplication)
	m.recordOperation(operation, resourceType, successful)
}

// recordReadDesireOperation records metrics for a ReadDesire operation (resync).
func (m *desireOperationMetrics) recordReadDesireOperation(desire *kubeapplier.ReadDesire) {
	// Guard against nil ResourceID
	if desire.CosmosMetadata.ResourceID == nil {
		return
	}

	resourceIDStr := desire.CosmosMetadata.ResourceID.String()
	resourceType := extractResourceType(resourceIDStr)
	successful := isDesireSuccessful(desire.Status.Conditions)

	m.mu.Lock()
	// Update last processed instance version while holding lock
	m.lastProcessedGeneration[resourceIDStr] = desire.CosmosMetadata.InstanceVersion
	m.mu.Unlock()

	// ReadDesire operations are always "resync" operations
	m.recordOperation(OperationResync, resourceType, successful)
}

// recordOperation is a helper that records common metrics to avoid duplication
// between recordApplyDesireOperation and recordReadDesireOperation.
func (m *desireOperationMetrics) recordOperation(operation, resourceType string, successful bool) {
	// Record operation count
	m.operationsTotal.With(prometheus.Labels{
		"operation":     operation,
		"resource_type": resourceType,
		"successful":    strconv.FormatBool(successful),
	}).Inc()

	// Record last operation timestamp
	m.operationLastTimestamp.With(prometheus.Labels{
		"operation":     operation,
		"resource_type": resourceType,
	}).SetToCurrentTime()
}

// extractResourceType parses the Cosmos resourceID to determine if this is a
// cluster or nodepool desire.
//
// ResourceID format:
//   subscriptions/{sub}/resourceGroups/{rg}/providers/microsoft.redhatopenshift/hcpopenshiftclusters/{name}/*desires/{desire}
//   subscriptions/{sub}/resourceGroups/{rg}/providers/microsoft.redhatopenshift/hcpopenshiftclusters/{name}/nodepools/{np}/*desires/{desire}
func extractResourceType(resourceID string) string {
	// Use case-insensitive matching to handle variations in casing
	lowerResourceID := strings.ToLower(resourceID)
	if strings.Contains(lowerResourceID, "/nodepools/") {
		return ResourceTypeNodePool
	}
	if strings.Contains(lowerResourceID, "/hcpopenshiftclusters/") {
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

// cleanupStaleEntries removes entries from lastProcessedGeneration for desires
// that are no longer present in the informer stores. This prevents unbounded
// memory growth when desires are deleted.
func (m *desireOperationMetrics) cleanupStaleEntries(currentDesires map[string]bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for resourceIDStr := range m.lastProcessedGeneration {
		if !currentDesires[resourceIDStr] {
			delete(m.lastProcessedGeneration, resourceIDStr)
		}
	}
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
