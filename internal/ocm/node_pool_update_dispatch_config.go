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

// nodePoolUpdateDispatchConfig is the set of properties that are updatable by RP Backend's
// node pool cluster service update dispatch controller, against Cluster Service.
// The dispatch controller compares desired and actual configs and sends a CS PATCH
// only when they differ.
//
// This does not necessarily include all the fields that can be updated via the CS API,
// just the ones that are considered during an ARM NodePool update call and processed by
// RP Backend's node pool cluster service update dispatch controller.
//
// Do not embed internal/api struct types in this struct or its nested field types.
// See clusterUpdateDispatchConfig for the full rationale.
type nodePoolUpdateDispatchConfig struct {
	Labels                  map[string]string                        `json:"labels,omitempty"`
	Replicas                int32                                    `json:"replicas,omitempty"`
	AutoScaling             *nodePoolUpdateDispatchConfigAutoScaling `json:"autoScaling,omitempty"`
	Taints                  []nodePoolUpdateDispatchConfigTaint      `json:"taints,omitempty"`
	NodeDrainTimeoutMinutes *int32                                   `json:"nodeDrainTimeoutMinutes,omitempty"`
}

// nodePoolUpdateDispatchConfigAutoScaling is the curated autoscaling subset hashed
// and applied to CS. See nodePoolUpdateDispatchConfig: do not embed api.NodePoolAutoScaling.
type nodePoolUpdateDispatchConfigAutoScaling struct {
	Min int32 `json:"min,omitempty"`
	Max int32 `json:"max,omitempty"`
}

// nodePoolUpdateDispatchConfigTaint is the curated taint subset hashed
// and applied to CS. See nodePoolUpdateDispatchConfig: do not embed api.Taint.
type nodePoolUpdateDispatchConfigTaint struct {
	Effect string `json:"effect,omitempty"`
	Key    string `json:"key,omitempty"`
	Value  string `json:"value,omitempty"`
}

// NodePoolUpdateDispatchConfigDiffers reports whether the dispatch-managed configuration
// derived from the RP node pool differs from the live Cluster Service node pool. The
// comparison uses a SHA-256 hash of each side's canonical JSON representation.
// Note: When the RP node pool node drain timeout minutes is nil, CS PATCH omit/null cannot clear
// or re-inherit cluster default. The CS value is normalized out of the diff so dispatch does not
// endlessly PATCH fields CS cannot change.
func NodePoolUpdateDispatchConfigDiffers(nodePool *api.HCPOpenShiftClusterNodePool, csNodePool *arohcpv1alpha1.NodePool) (bool, error) {
	desiredConfig, actualConfig := nodePoolUpdateDispatchConfigsForDiff(nodePool, csNodePool)
	desiredHash, err := desiredConfig.hash()
	if err != nil {
		return false, err
	}

	actualHash, err := actualConfig.hash()
	if err != nil {
		return false, err
	}

	return desiredHash != actualHash, nil
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

// NodePoolUpdateDispatchConfigJSONFromRP returns the canonical JSON representation of the
// dispatch-managed configuration derived from the RP node pool.
func NodePoolUpdateDispatchConfigJSONFromRP(nodePool *api.HCPOpenShiftClusterNodePool) (string, error) {
	raw, err := nodePoolUpdateDispatchConfigFromRP(nodePool).canonicalJSON()
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// NodePoolUpdateDispatchConfigJSONFromCS returns the canonical JSON representation of the
// dispatch-managed configuration derived from a Cluster Service node pool.
func NodePoolUpdateDispatchConfigJSONFromCS(csNodePool *arohcpv1alpha1.NodePool) (string, error) {
	raw, err := nodePoolUpdateDispatchConfigFromCS(csNodePool).canonicalJSON()
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func nodePoolUpdateDispatchConfigFromRP(nodePool *api.HCPOpenShiftClusterNodePool) *nodePoolUpdateDispatchConfig {
	config := &nodePoolUpdateDispatchConfig{
		Labels:                  nodePool.Properties.Labels,
		Taints:                  nodePoolUpdateDispatchConfigTaintsFromRP(nodePool.Properties.Taints),
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

func nodePoolUpdateDispatchConfigTaintsFromRP(taints []api.Taint) []nodePoolUpdateDispatchConfigTaint {
	if len(taints) == 0 {
		return nil
	}

	out := make([]nodePoolUpdateDispatchConfigTaint, 0, len(taints))
	for _, t := range taints {
		out = append(out, nodePoolUpdateDispatchConfigTaint{
			Effect: string(t.Effect),
			Key:    t.Key,
			Value:  t.Value,
		})
	}
	return out
}

func nodePoolUpdateDispatchConfigFromCS(csNodePool *arohcpv1alpha1.NodePool) *nodePoolUpdateDispatchConfig {
	config := &nodePoolUpdateDispatchConfig{}

	if labels, ok := csNodePool.GetLabels(); ok {
		config.Labels = labels
	}

	if autoscaling, ok := csNodePool.GetAutoscaling(); ok && autoscaling != nil {
		config.AutoScaling = &nodePoolUpdateDispatchConfigAutoScaling{
			Min: int32(autoscaling.MinReplica()),
			Max: int32(autoscaling.MaxReplica()),
		}
	} else {
		config.Replicas = int32(csNodePool.Replicas())
	}

	config.Taints = nodePoolUpdateDispatchConfigTaintsFromCS(csNodePool)
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

func nodePoolUpdateDispatchConfigTaintsFromCS(csNodePool *arohcpv1alpha1.NodePool) []nodePoolUpdateDispatchConfigTaint {
	csTaints, ok := csNodePool.GetTaints()
	if !ok || len(csTaints) == 0 {
		return nil
	}

	out := make([]nodePoolUpdateDispatchConfigTaint, 0, len(csTaints))
	for _, t := range csTaints {
		out = append(out, nodePoolUpdateDispatchConfigTaint{
			Effect: t.Effect(),
			Key:    t.Key(),
			Value:  t.Value(),
		})
	}
	return out
}

func nodePoolUpdateDispatchConfigHash(nodePool *api.HCPOpenShiftClusterNodePool) (string, error) {
	return nodePoolUpdateDispatchConfigFromRP(nodePool).hash()
}

func (c *nodePoolUpdateDispatchConfig) hash() (string, error) {
	return hashUpdateDispatchConfig(c)
}

func (c *nodePoolUpdateDispatchConfig) canonicalJSON() ([]byte, error) {
	return canonicalJSONForUpdateDispatchConfig(c)
}

// applyToCSBuilder applies the updatable dispatch-managed fields onto a NodePoolBuilder.
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
