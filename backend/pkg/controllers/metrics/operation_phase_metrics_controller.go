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
	"time"

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
// collapse to the newest retained operation by StartTime, with the Cosmos
// document key breaking ties so unordered informer resyncs cannot roll
// the gauges back to an older attempt. Operations of DIFFERENT types (e.g. a
// completed "create" and an in-flight "delete") coexist independently
// and do not clobber each other. Delete selects the newest surviving
// sibling, or clears the gauges when no operations remain.
type operationPhaseMetricsHandler struct {
	phaseInfo          *prometheus.GaugeVec
	startTime          *prometheus.GaugeVec
	lastTransitionTime *prometheus.GaugeVec
	// Not guarded by a mutex: threadiness=1 serializes all access.
	// A mutex would not help at higher threadiness because Sync's
	// gauge delete-then-set is not atomic across Prometheus calls.
	operationsBookkeeper map[string]operationIdentity // cosmosDocKey: identity (reverse lookup for Delete)
	operationsByIdentity map[operationIdentity]map[string]operationMetricState
}

type operationIdentity struct {
	resourceID    string
	operationType string
}

type operationMetricState struct {
	labels             prometheus.Labels
	startTime          time.Time
	lastTransitionTime time.Time
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
		operationsByIdentity: make(map[operationIdentity]map[string]operationMetricState, 2000),
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

	opType := operationTypeMetricLabel(op.Request)
	cosmosKey := resourceIDMetricLabel(op.GetResourceID())
	identity := operationIdentity{
		resourceID:    resourceID,
		operationType: opType,
	}
	if previous, tracked := h.operationsBookkeeper[cosmosKey]; tracked && previous != identity {
		h.Delete(cosmosKey)
	}
	h.operationsBookkeeper[cosmosKey] = identity
	if h.operationsByIdentity[identity] == nil {
		h.operationsByIdentity[identity] = make(map[string]operationMetricState)
	}
	h.operationsByIdentity[identity][cosmosKey] = operationMetricState{
		labels: prometheus.Labels{
			"resource_id":     resourceID,
			"subscription_id": subscriptionID,
			"resource_type":   resourceIDToTypeMetricLabel(op.ExternalID),
			"operation_type":  opType,
			"phase":           phaseMetricLabel(op.Status),
		},
		startTime:          op.StartTime,
		lastTransitionTime: op.LastTransitionTime,
	}
	h.updateMetrics(identity)
}

func (h *operationPhaseMetricsHandler) Delete(key string) {
	identity, ok := h.operationsBookkeeper[key]
	if !ok {
		return
	}
	delete(h.operationsBookkeeper, key)
	delete(h.operationsByIdentity[identity], key)
	if len(h.operationsByIdentity[identity]) == 0 {
		delete(h.operationsByIdentity, identity)
	}
	h.updateMetrics(identity)
}

func (h *operationPhaseMetricsHandler) updateMetrics(identity operationIdentity) {
	var newest operationMetricState
	var newestKey string
	for key, candidate := range h.operationsByIdentity[identity] {
		if newestKey == "" || candidate.startTime.After(newest.startTime) ||
			(candidate.startTime.Equal(newest.startTime) && key > newestKey) {
			newest, newestKey = candidate, key
		}
	}

	h.deleteByResourceIDAndOperationType(identity.resourceID, identity.operationType)
	if newestKey == "" {
		return
	}
	h.phaseInfo.With(newest.labels).Set(1)
	if !newest.startTime.IsZero() {
		h.startTime.With(newest.labels).Set(float64(newest.startTime.Unix()))
	}
	if !newest.lastTransitionTime.IsZero() {
		h.lastTransitionTime.With(newest.labels).Set(float64(newest.lastTransitionTime.Unix()))
	}
}

// deleteByResourceIDAndOperationType clears all gauge series matching the
// given resource_id + operation_type before emitting the selected operation.
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
