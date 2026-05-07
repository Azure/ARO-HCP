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
	"github.com/Azure/ARO-HCP/internal/utils"
)

var operationMetricLabelNames = []string{"resource_id", "subscription_id", "resource_type", "operation_type", "phase"}

// operationPhaseMetricsHandler emits and clears the
// backend_resource_operation_* metric family.
//
// resource_id derivation:
//
// resource_id is the lowercased ARM resource id of the cluster /
// nodepool / external auth this operation targets (op.ExternalID, via
// op.MetricResourceID()). It is NOT the cosmos doc id stored in
// op.ResourceID, which exists only for unique cosmos addressing and
// has no meaning to operators correlating metrics with customer ARM
// resources. This matches the format already used by the sibling
// backend_resource_provision_state metric family.
//
// Multiple operations on the same ARM resource id collapse to one
// series. On the AllOperations() informer's unordered iteration
// (every relist / resync, or backend restart) whichever operation
// is processed last wins for that resource_id; this may temporarily
// or persistently reflect an older terminal op until a later emit
// for that resource supersedes it. If the resource is idle, the stale
// labels can persist indefinitely. Separate limitation: when the
// last operation for a resource ages out of the Cosmos TTL, its
// series persists until process restart (Delete is a no-op; see
// Delete doc-comment for the rationale). Resource-level conditions
// are the longer-term direction.
type operationPhaseMetricsHandler struct {
	phaseInfo          *prometheus.GaugeVec
	startTime          *prometheus.GaugeVec
	lastTransitionTime *prometheus.GaugeVec
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
	}
	r.MustRegister(h.phaseInfo, h.startTime, h.lastTransitionTime)
	return h
}

func (h *operationPhaseMetricsHandler) Sync(ctx context.Context, op *api.Operation) {
	resourceID := resourceIDMetricLabel(op.MetricResourceID())
	if len(resourceID) == 0 {
		// op.ExternalID is expected to always be populated for production
		// operations (every frontend construction site passes the target
		// resource ID into database.NewOperation). Surface a warning when
		// the invariant breaks so an operator notices instead of staring
		// at a silently missing metric. This logs once per Sync event
		// for the offending op; if an operation persists with nil
		// ExternalID across resyncs the log will repeat per reconcile,
		// which is bounded by the op count and gives count-based
		// alerting a hook.
		logger := utils.LoggerFromContext(ctx)
		logger.Info("operation has no ExternalID; skipping metric emission",
			"cosmos_doc_id", resourceIDMetricLabel(op.GetResourceID()))
		return
	}
	subscriptionID := subscriptionIDMetricLabel(op.MetricResourceID())
	if op.OperationID == nil {
		// Implicit operation (e.g. child-resource cleanup along with
		// parent). Don't emit a metric series for it, and don't
		// deleteByResourceID either: a sibling operation with the
		// same ExternalID may already own the emitted series for
		// this resource_id and we must not blank it. Same rationale
		// as Delete being a no-op (see Delete doc-comment).
		return
	}

	// Clear any previous series for this resource before writing the current labels.
	// Phase and operation labels are part of the metric identity, so updates would
	// otherwise leave stale series behind for older label combinations. Multiple
	// operations sharing this resource_id collapse to the latest-emitted labels.
	h.deleteByResourceID(resourceID)

	labels := prometheus.Labels{
		"resource_id":     resourceID,
		"subscription_id": subscriptionID,
		"resource_type":   resourceIDToTypeMetricLabel(op.MetricResourceID()),
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
}

// Delete is intentionally a no-op for operation phase metrics.
//
// The controller framework calls handler.Delete with the indexer key,
// which for Operations is the lowercased Cosmos document id. That key
// no longer matches the resource_id metric label after this PR
// (resource_id is now derived from op.ExternalID, the ARM resource id).
// Deleting by the cosmos key would not find any series; deleting by
// resource_id would blank any sibling operation's currently-emitted
// series for the same ARM resource (multiple operation documents can
// share one resource_id label). Sync's pre-emit DeletePartialMatch
// implicitly cleans up obsolete labels whenever any operation for
// a resource is processed, so explicit Delete is unnecessary.
//
// The trade-off: when the LAST operation for a resource ages out of
// the Cosmos TTL with no surviving sibling, the series persists in
// the in-memory prom registry until process restart / pod replacement;
// affects only resources that go fully idle. The alternative
// (per-resource active-op counting) reintroduces handler-local
// bookkeeping, which is disproportionate for an operation-phase
// metric whose longer-term direction is resource-level conditions.
func (h *operationPhaseMetricsHandler) Delete(key string) {
	// no-op; see doc-comment above
}

// deleteByResourceID is used by Sync to clear stale labels before
// writing new ones. Delete intentionally does not call this; see
// the Delete doc-comment.
func (h *operationPhaseMetricsHandler) deleteByResourceID(resourceID string) {
	if len(resourceID) == 0 {
		return
	}
	deleteSelector := prometheus.Labels{"resource_id": resourceID}
	h.phaseInfo.DeletePartialMatch(deleteSelector)
	h.startTime.DeletePartialMatch(deleteSelector)
	h.lastTransitionTime.DeletePartialMatch(deleteSelector)
}

func operationTypeMetricLabel(request api.OperationRequest) string {
	return strings.ToLower(string(request))
}
