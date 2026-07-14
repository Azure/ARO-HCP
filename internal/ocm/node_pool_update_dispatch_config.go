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

package ocm

import (
	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api"
)

// nodePoolUpdateDispatchConfig is a dispatch-specific canonical model of the NodePool's
// Cluster Service fields that are considered by the node pool cluster service update dispatch
// controller. Its shape intentionally does not mirror RP resources or the Cluster Service API.
// Conversion functions project external state into this form and back out when dispatching to
// Cluster Service.
//
// The same struct is built from either RP desired state (from HCPOpenShiftClusterNodePool) or
// from the live Cluster Service NodePool. This applies only to NodePool CS updates. Cluster and
// external auth updates use separate dispatch paths and update dispatch config structs. Drift
// between the two projections may trigger the node pool cluster service update dispatch controller
// to PATCH Cluster Service.
//
// The node pool cluster service update dispatch controller compares desired and actual configs in
// this canonical form and sends a CS PATCH only when they differ.
//
// Note: This does not include all fields updatable via the NodePool Cluster Service API, only
// the subset that the node pool cluster service update dispatch controller considers.
//
// Note: Do not embed internal/api struct types (for example api.NodePoolAutoScaling)
// in this struct or its nested field types. We want to make those internal/api struct types
// independent of this so they can evolve independently. For example, if a field here referenced
// an internal/api struct type directly, any new field added to that struct would be automatically
// considered as updatable automatically, but we might not want that field to be updatable and/or
// CS side doesn't really support updating it. Instead, define curated local structs with only the
// fields that dispatch should hash and sync, and copy values explicitly from api types at the
// conversion boundaries. Using api/internal enum or scalar types for individual curated fields is
// fine. This is because adding an enum/scalar field they do not pull extra fields, but adding
// struct types does.
//
// Node drain timeout has special diff semantics: when RP stores nil (inherit cluster default),
// Cluster Service PATCH omit/null cannot clear or re-inherit that default. nodePoolUpdateDispatchConfigsForDiff
// normalizes the CS value out of the diff so dispatch does not endlessly PATCH fields CS cannot
// change. Operation state and Hypershift reconciliation use NodePoolUpdateDispatchConfigEffectiveNodeDrainTimeoutMinutes
// to compare the effective desired value instead.
//
// IMPORTANT: how to add a new dispatch-managed config field:
//
// Dispatch and operation state are related but wired separately. You need both for a correct
// node pool update experience:
//   - Dispatch (this file): detects drift and PATCHes Cluster Service.
//   - Operation state (operation_node_pool_update.go, operation_node_pool_update_state_calculation.go):
//     decides when the ARM node pool update operation can leave Updating and report Succeeded.
//
// If you wire dispatch but skip operation state calculation, the update may be sent to CS but the
// operation can succeed too early (or stay Updating forever if dispatch is missing).
//
// Before you start:
//
//   - Confirm this is a node-pool-level Cluster Service update (not cluster or external auth).
//   - Confirm Cluster Service supports updating the field on an existing node pool.
//   - Identify where RP desired state lives (HCPOpenShiftClusterNodePool.Properties, etc.).
//
// 1. Dispatch wiring (this file)
//
//   - Add the field to nodePoolUpdateDispatchConfig or a curated nested struct.
//     Do not embed internal/api struct types.
//   - Populate it in nodePoolUpdateDispatchConfigFromRP. This ensures RP projection works correctly.
//   - Populate it in nodePoolUpdateDispatchConfigFromCS. This ensures CS projection works correctly.
//   - Apply it in applyToCSBuilder. This ensures the CS builder works correctly.
//   - If the field has CS PATCH limitations (like node drain timeout inherit/clear), document and
//     handle normalization in nodePoolUpdateDispatchConfigsForDiff and wire effective desired
//     comparison helpers for operation state if needed.
//
// 2. Operation state wiring (backend/pkg/controllers/operationcontrollers/operation_node_pool_update.go)
//
//	determineOperationState aggregates several sources and picks the worst state. Your new
//	check must succeed along with version resolution, CS checks and Hypershift checks.
//
//	Choose where to observe the change and implement it:
//	  - Prefer observing directly from the Management Cluster side when the field is visible there
//	    after propagation. Most dispatch fields today are checked in here: labels, replicas/autoscaling, taints, node drain timeout, ...
//	  - Observe from Cluster Service when the Management Cluster side is not a reliable source of
//	    truth yet or simply can't be calculated from there.
//	  - Sometimes you might want to observe it on both sides for extra validation.
//
//	Return (false, message) with a clear message when observed != desired so Updating state
//	is actionable in logs.
//
// 3. Tests
//
//   - node_pool_update_dispatch_config_test.go: hash, FromCS, round-trip, apply payload, diff
//     normalization if applicable.
//   - operation_node_pool_update_state_calculation_test.go: match/mismatch cases for your new
//     helper and, if useful, an end-to-end hypershiftNodePoolOperationState case.
//   - Consider both "not applied yet" (Updating) and "applied" (Succeeded) scenarios.
//
// 4. Sanity checks
//
//   - Create path: applyToCSBuilder is also used by BuildCSNodePool in internal/ocm/convert.go
//     when the frontend first creates a Cluster Service node pool (not only when the update
//     dispatch controller PATCHes an existing one). Verify the new field is present on create.
//   - Desired state must exist in Cosmos before dispatch can sync it. If customers set this
//     field via ARM, also wire the full ingest path: ARM API, frontend validation/conversion,
//     and persistence onto HCPOpenShiftClusterNodePool. Internal-only fields still need whatever
//     backend path writes the value Cosmos holds.
type nodePoolUpdateDispatchConfig struct {
	Labels                  map[string]string                        `json:"labels,omitempty"`
	Replicas                int32                                    `json:"replicas,omitempty"`
	AutoScaling             *nodePoolUpdateDispatchConfigAutoScaling `json:"autoScaling,omitempty"`
	Taints                  []NodePoolUpdateDispatchConfigTaint      `json:"taints,omitempty"`
	NodeDrainTimeoutMinutes *int32                                   `json:"nodeDrainTimeoutMinutes,omitempty"`
}

// nodePoolUpdateDispatchConfigAutoScaling is the curated autoscaling subset used for
// dispatch hash and sync. See nodePoolUpdateDispatchConfig: do not embed api.NodePoolAutoScaling.
type nodePoolUpdateDispatchConfigAutoScaling struct {
	Min int32 `json:"min,omitempty"`
	Max int32 `json:"max,omitempty"`
}

// NodePoolUpdateDispatchConfigTaint is the curated taint subset used for dispatch
// hash and sync. See nodePoolUpdateDispatchConfig: do not embed api.Taint.
type NodePoolUpdateDispatchConfigTaint struct {
	Effect string `json:"effect,omitempty"`
	Key    string `json:"key,omitempty"`
	Value  string `json:"value,omitempty"`
}

// NodePoolUpdateDispatchConfigDiffJSON returns canonical JSON for both sides of the
// dispatch diff comparison, including drain-timeout normalization when RP has no override.
func NodePoolUpdateDispatchConfigDiffJSON(nodePool *api.HCPOpenShiftClusterNodePool, csNodePool *arohcpv1alpha1.NodePool) (string, string, error) {
	desiredConfig, actualConfig := nodePoolUpdateDispatchConfigsForDiff(nodePool, csNodePool)

	desiredRaw, err := desiredConfig.canonicalJSON()
	if err != nil {
		return "", "", err
	}
	actualRaw, err := actualConfig.canonicalJSON()
	if err != nil {
		return "", "", err
	}

	return string(desiredRaw), string(actualRaw), nil
}

// nodePoolUpdateDispatchConfigsForDiff builds RP and CS projections for diff comparison.
// When RP has no drain-timeout override, the CS value is cleared from the actual side so
// dispatch does not endlessly PATCH fields CS cannot change via omit/null.
func nodePoolUpdateDispatchConfigsForDiff(nodePool *api.HCPOpenShiftClusterNodePool, csNodePool *arohcpv1alpha1.NodePool) (*nodePoolUpdateDispatchConfig, *nodePoolUpdateDispatchConfig) {
	desiredConfig := nodePoolUpdateDispatchConfigFromRP(nodePool)
	actualConfig := nodePoolUpdateDispatchConfigFromCS(csNodePool)
	// When RP has no drain-timeout override, CS PATCH cannot clear or re-inherit via omit/null.
	// Normalize the CS value out of the diff so we do not endlessly dispatch PATCHes that cannot change it.
	if desiredConfig.NodeDrainTimeoutMinutes == nil {
		actualConfig.NodeDrainTimeoutMinutes = nil
	}
	return desiredConfig, actualConfig
}

// NodePoolUpdateDispatchConfigJSONFromRP returns the canonical JSON of the dispatch config
// projected from RP desired state.
func NodePoolUpdateDispatchConfigJSONFromRP(nodePool *api.HCPOpenShiftClusterNodePool) (string, error) {
	raw, err := nodePoolUpdateDispatchConfigFromRP(nodePool).canonicalJSON()
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// NodePoolUpdateDispatchConfigJSONFromCS returns the canonical JSON of the dispatch config
// projected from a Cluster Service node pool.
func NodePoolUpdateDispatchConfigJSONFromCS(csNodePool *arohcpv1alpha1.NodePool) (string, error) {
	raw, err := nodePoolUpdateDispatchConfigFromCS(csNodePool).canonicalJSON()
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// nodePoolUpdateDispatchConfigFromRP projects RP desired state into the dispatch canonical form.
func nodePoolUpdateDispatchConfigFromRP(nodePool *api.HCPOpenShiftClusterNodePool) *nodePoolUpdateDispatchConfig {
	config := &nodePoolUpdateDispatchConfig{
		Labels:                  nodePool.Properties.Labels,
		Taints:                  NodePoolUpdateDispatchConfigTaintsFromRP(nodePool.Properties.Taints),
		NodeDrainTimeoutMinutes: nodePool.Properties.NodeDrainTimeoutMinutes,
	}

	if nodePool.Properties.AutoScaling != nil {
		config.AutoScaling = &nodePoolUpdateDispatchConfigAutoScaling{
			Min: nodePool.Properties.AutoScaling.Min,
			Max: nodePool.Properties.AutoScaling.Max,
		}
	} else {
		config.Replicas = nodePool.Properties.Replicas
	}

	return config
}

// NodePoolUpdateDispatchConfigTaintsFromRP copies taints from RP into the dispatch canonical form.
func NodePoolUpdateDispatchConfigTaintsFromRP(taints []api.Taint) []NodePoolUpdateDispatchConfigTaint {
	if len(taints) == 0 {
		return nil
	}

	out := make([]NodePoolUpdateDispatchConfigTaint, 0, len(taints))
	for _, t := range taints {
		out = append(out, NodePoolUpdateDispatchConfigTaint{
			Effect: string(t.Effect),
			Key:    t.Key,
			Value:  t.Value,
		})
	}
	return out
}

// nodePoolUpdateDispatchConfigFromCS projects a Cluster Service node pool into the dispatch
// canonical form.
func nodePoolUpdateDispatchConfigFromCS(csNodePool *arohcpv1alpha1.NodePool) *nodePoolUpdateDispatchConfig {
	config := &nodePoolUpdateDispatchConfig{
		Labels: NodePoolUpdateDispatchConfigLabelsFromCS(csNodePool),
	}

	if autoscaling, ok := csNodePool.GetAutoscaling(); ok && autoscaling != nil {
		config.AutoScaling = &nodePoolUpdateDispatchConfigAutoScaling{
			Min: int32(autoscaling.MinReplica()),
			Max: int32(autoscaling.MaxReplica()),
		}
	} else {
		config.Replicas = int32(csNodePool.Replicas())
	}

	config.Taints = NodePoolUpdateDispatchConfigTaintsFromCS(csNodePool)
	config.NodeDrainTimeoutMinutes = NodePoolUpdateDispatchConfigNodeDrainTimeoutFromCS(csNodePool)

	return config
}

// NodePoolUpdateDispatchConfigEffectiveNodeDrainTimeoutMinutes returns the drain timeout
// Hypershift reconciliation should expect. When RP stores an explicit override, that
// value is returned. When RP stores nil (no override), the live Cluster Service node pool
// value is returned because CS PATCH omit/null cannot clear or re-inherit cluster default.
func NodePoolUpdateDispatchConfigEffectiveNodeDrainTimeoutMinutes(nodePool *api.HCPOpenShiftClusterNodePool, csNodePool *arohcpv1alpha1.NodePool) *int32 {
	if nodePool.Properties.NodeDrainTimeoutMinutes != nil {
		return nodePool.Properties.NodeDrainTimeoutMinutes
	}
	return NodePoolUpdateDispatchConfigNodeDrainTimeoutFromCS(csNodePool)
}

// NodePoolUpdateDispatchConfigNodeDrainTimeoutFromCS extracts the node drain timeout in
// minutes from a Cluster Service node pool object. Returns nil when unset.
func NodePoolUpdateDispatchConfigNodeDrainTimeoutFromCS(in *arohcpv1alpha1.NodePool) *int32 {
	if nodeDrainGracePeriod, ok := in.GetNodeDrainGracePeriod(); ok {
		if unit, ok := nodeDrainGracePeriod.GetUnit(); ok && unit == csNodeDrainGracePeriodUnit {
			v := int32(nodeDrainGracePeriod.Value())
			return &v
		}
	}
	return nil
}

// NodePoolUpdateDispatchConfigLabelsFromCS extracts labels from a Cluster Service node pool.
func NodePoolUpdateDispatchConfigLabelsFromCS(csNodePool *arohcpv1alpha1.NodePool) map[string]string {
	if labels, ok := csNodePool.GetLabels(); ok {
		return labels
	}
	return nil
}

// NodePoolUpdateDispatchConfigTaintsFromCS extracts taints from a Cluster Service node pool
// into the dispatch canonical form.
func NodePoolUpdateDispatchConfigTaintsFromCS(csNodePool *arohcpv1alpha1.NodePool) []NodePoolUpdateDispatchConfigTaint {
	csTaints, ok := csNodePool.GetTaints()
	if !ok || len(csTaints) == 0 {
		return nil
	}

	out := make([]NodePoolUpdateDispatchConfigTaint, 0, len(csTaints))
	for _, t := range csTaints {
		out = append(out, NodePoolUpdateDispatchConfigTaint{
			Effect: t.Effect(),
			Key:    t.Key(),
			Value:  t.Value(),
		})
	}
	return out
}

// nodePoolUpdateDispatchConfigHash returns a SHA-256 hex digest of the dispatch config
// projected from RP desired state. The digest is computed from canonical JSON (sorted object
// keys at every level), not from a raw json.Marshal of the struct.
func nodePoolUpdateDispatchConfigHash(nodePool *api.HCPOpenShiftClusterNodePool) (string, error) {
	return nodePoolUpdateDispatchConfigFromRP(nodePool).hash()
}

// hash returns a SHA-256 hex digest of c's canonical JSON.
func (c *nodePoolUpdateDispatchConfig) hash() (string, error) {
	return hashUpdateDispatchConfig(c)
}

// canonicalJSON returns the deterministic JSON encoding of c used for hashing and comparison.
// Keys are sorted at every object level and the payload is indented with two spaces; see
// canonicalJSONForUpdateDispatchConfig.
func (c *nodePoolUpdateDispatchConfig) canonicalJSON() ([]byte, error) {
	return canonicalJSONForUpdateDispatchConfig(c)
}

// applyToCSBuilder maps the dispatch config onto a Cluster Service node pool builder.
func (c *nodePoolUpdateDispatchConfig) applyToCSBuilder(nodePoolBuilder *arohcpv1alpha1.NodePoolBuilder) {
	nodePoolBuilder.Labels(c.Labels)

	if c.AutoScaling != nil {
		nodePoolBuilder.Autoscaling(arohcpv1alpha1.NewNodePoolAutoscaling().
			MinReplica(int(c.AutoScaling.Min)).
			MaxReplica(int(c.AutoScaling.Max)))
	} else {
		nodePoolBuilder.Replicas(int(c.Replicas))
	}

	if c.Taints != nil {
		taintBuilders := make([]*arohcpv1alpha1.TaintBuilder, 0, len(c.Taints))
		for _, t := range c.Taints {
			taintBuilders = append(taintBuilders, arohcpv1alpha1.NewTaint().
				Effect(t.Effect).
				Key(t.Key).
				Value(t.Value))
		}
		nodePoolBuilder.Taints(taintBuilders...)
	}

	if c.NodeDrainTimeoutMinutes != nil {
		nodePoolBuilder.NodeDrainGracePeriod(arohcpv1alpha1.NewValue().
			Unit(csNodeDrainGracePeriodUnit).
			Value(float64(*c.NodeDrainTimeoutMinutes)))
	}
}
