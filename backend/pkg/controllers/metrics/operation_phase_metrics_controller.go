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

package metrics

import (
	"context"
	"strings"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/utils"
)

var operationMetricLabelNames = []string{"resource_id", "subscription_id", "resource_type", "operation_type", "phase"}

// operationPhaseMetricsHandler emits and clears the
// backend_resource_operation_* metric family.
//
// resource_id derivation:
//
// resource_id is the lowercased ARM resource id of the cluster /
// nodepool / external auth this operation targets (op.ExternalID).
// It is NOT the cosmos doc id stored in
// op.ResourceID, which exists only for unique cosmos addressing and
// has no meaning to operators correlating metrics with customer ARM
// resources. This matches the format already used by the sibling
// backend_resource_provision_state metric family.
//
// Multiple operations of the SAME type on the same ARM resource id
// collapse to one series. On the AllOperations() informer's unordered
// iteration (every relist / resync, or backend restart) whichever
// same-type operation is processed last wins for that resource_id +
// operation_type combination. Operations of DIFFERENT types (e.g. a
// completed "create" and an in-flight "delete") coexist independently
// and do not clobber each other. When the last operation for a
// resource ages out of the Cosmos TTL, Delete clears the gauge using
// a refcount to avoid clobbering sibling operations' active series.
type operationPhaseMetricsHandler struct {
	phaseInfo          *prometheus.GaugeVec
	startTime          *prometheus.GaugeVec
	lastTransitionTime *prometheus.GaugeVec
	// Not guarded by a mutex: threadiness=1 serializes all access.
	// A mutex would not help at higher threadiness because Sync's
	// gauge delete-then-set is not atomic across Prometheus calls.
	operationsBookkeeper map[string]operationIdentity // cosmosDocKey: identity (reverse lookup for Delete)
	operationsCounter    map[operationIdentity]int    // identity: count of cosmos docs contributing to this gauge
}

type operationIdentity struct {
	resourceID    string
	operationType string
}

// NewOperationPhaseMetricsHandler creates a metrics handler for operation metrics.
func NewOperationPhaseMetricsHandler(r prometheus.Registerer) Handler[*coreapi.Operation] {
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
		// Pre-sized to steady-state: bounded by operations within
		// the 7-day CosmosDB TTL per shard (~1k-2k entries, ~100 clusters).
		operationsBookkeeper: make(map[string]operationIdentity, 2000),
		operationsCounter:    make(map[operationIdentity]int, 2000),
	}
	r.MustRegister(h.phaseInfo, h.startTime, h.lastTransitionTime)
	return h
}

func (h *operationPhaseMetricsHandler) Sync(ctx context.Context, op *coreapi.Operation) {
	resourceID := resourceIDMetricLabel(op.ExternalID)
	if len(resourceID) == 0 {
		// op.ExternalID is expected to always be populated for production
		// operations (every frontend construction site passes the target
		// resource ID into cosmosstorageutils.NewOperation). Log when the invariant
		// breaks so an operator notices instead of staring at a silently
		// missing metric. This logs once per Sync event for the offending
		// op; if an operation persists with nil ExternalID across resyncs
		// the log will repeat per reconcile, which is bounded by the op
		// count and gives count-based alerting a hook.
		logger := utils.LoggerFromContext(ctx)
		logger.Info("operation has no ExternalID; skipping metric emission",
			"missing_external_id", true,
			"cosmos_doc_id", resourceIDMetricLabel(op.GetResourceID()))
		return
	}
	subscriptionID := subscriptionIDMetricLabel(op.ExternalID)
	if op.OperationID == nil {
		// Implicit operation (e.g. child-resource cleanup along with
		// parent). Don't emit a metric series for it, and don't
		// track it in operationsBookkeeper: a sibling operation with
		// the same ExternalID may already own the emitted series for
		// this resource_id and we must not blank it.
		return
	}

	// Clear any previous series for this resource and operation type before writing
	// the current labels. Phase is part of the metric identity, so updates would
	// otherwise leave stale series behind for older phase values. We scope the
	// deletion to resource_id + operation_type so that concurrent operations of
	// different types (e.g. a completed "create" and an in-flight "delete") coexist
	// without clobbering each other on informer relists.
	opType := operationTypeMetricLabel(op.Request)
	h.deleteByResourceIDAndOperationType(resourceID, opType)

	labels := prometheus.Labels{
		"resource_id":     resourceID,
		"subscription_id": subscriptionID,
		"resource_type":   resourceIDToTypeMetricLabel(op.ExternalID),
		"operation_type":  opType,
		"phase":           phaseMetricLabel(op.Status),
	}
	h.phaseInfo.With(labels).Set(1.0)

	if !op.StartTime.IsZero() {
		h.startTime.With(labels).Set(float64(op.StartTime.Unix()))
	}
	if !op.LastTransitionTime.IsZero() {
		h.lastTransitionTime.With(labels).Set(float64(op.LastTransitionTime.Unix()))
	}
	// Track this operation so Delete can resolve the Cosmos doc key back
	// to the metric identity and decrement the refcount. The bookkeeper
	// maps cosmosKey → identity; the counter tracks how many Cosmos docs
	// contribute to each identity's gauge. See Delete for the cleanup path.
	cosmosKey := resourceIDMetricLabel(op.GetResourceID())
	newID := operationIdentity{
		resourceID:    resourceID,
		operationType: opType,
	}

	if existingID, tracked := h.operationsBookkeeper[cosmosKey]; tracked {
		// Re-sync: this cosmosKey was already seen in a previous Sync. If the identity hasn't changed, skip.
		if existingID != newID {
			// If ExternalID or Request changed, adjust the counts to keep the two maps consistent.
			h.operationsCounter[existingID]--
			if h.operationsCounter[existingID] <= 0 {
				delete(h.operationsCounter, existingID)
				h.deleteByResourceIDAndOperationType(existingID.resourceID, existingID.operationType)
			}
			h.operationsBookkeeper[cosmosKey] = newID
			h.operationsCounter[newID]++
		}
	} else {
		// First time seeing this cosmosKey
		h.operationsBookkeeper[cosmosKey] = newID
		h.operationsCounter[newID]++
	}
}

// Delete clears gauge series when the last Cosmos operation document
// for a (resourceID, operationType) pair expires. The controller
// framework calls Delete with the lowercased Cosmos document id.
// operationsBookkeeper resolves it to the metric identity, and
// operationsCounter tracks how many documents still contribute.
// When the count reaches zero, the gauge is safe to clear without
// clobbering a sibling operation's active series.
func (h *operationPhaseMetricsHandler) Delete(key string) {
	identity, ok := h.operationsBookkeeper[key]
	if !ok {
		return
	}
	delete(h.operationsBookkeeper, key)
	h.operationsCounter[identity]--
	if h.operationsCounter[identity] <= 0 {
		delete(h.operationsCounter, identity)
		h.deleteByResourceIDAndOperationType(identity.resourceID, identity.operationType)
	}
}

// deleteByResourceIDAndOperationType clears all gauge series matching the
// given resource_id + operation_type. Called by Sync (to clear stale phase
// labels before re-emitting) and by Delete (when the refcount reaches zero).
func (h *operationPhaseMetricsHandler) deleteByResourceIDAndOperationType(resourceID, operationType string) {
	if len(resourceID) == 0 {
		return
	}
	deleteSelector := prometheus.Labels{"resource_id": resourceID, "operation_type": operationType}
	h.phaseInfo.DeletePartialMatch(deleteSelector)
	h.startTime.DeletePartialMatch(deleteSelector)
	h.lastTransitionTime.DeletePartialMatch(deleteSelector)
}

func operationTypeMetricLabel(request coreapi.OperationRequest) string {
	return strings.ToLower(string(request))
}
