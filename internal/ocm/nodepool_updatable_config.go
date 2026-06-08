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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// nodePoolUpdatableConfig is the canonical representation of node pool properties
// hashed by NodePoolUpdatableConfigHash and applied to Cluster Service by
// applyNodePoolUpdatableConfig (via BuildCSNodePool). Add or remove fields here
// and update NodePoolUpdatableConfigFromProperties plus applyNodePoolUpdatableConfig
// in the same change.
//
// The digest is stored on the node pool as ClusterServiceUpdatableConfigHashForUpdateDispatch and
// compared by the nodepool update dispatch controller: a mismatch triggers a CS PATCH and
// hash replacement. It is also stamped during nodepool creation.
//
// Changing this struct has deploy-time effects:
//   - Removing a field (in the top-level or nested) changes the digest for every node pool that had that field marshalled. The marshalling of the
//     field depends on whether omitempty was set for the field and the actual value of the field for the corresponding NodePool.
//   - Adding a field (in the top-level or nested) changes the digest for every node pool that would start marshalling the field. The marshalling of
//     the field depends on whether omitempty is set for it and the actual value of the field for the corresponding NodePool.
//   - Renaming a json tag (in the top-level or nested) changes the digest for every node pool that had the field marshalled. The marshalling of the
//     field depends on whether omitempty was set for the field and the actual value of the field for the corresponding NodePool.
//
// In all of those cases, a change of digest implies a CS PATCH and hash replacement.
// Note: If in the future we consider that the previous behavior is too risky, we can consider having some sort of versioning of the config hash
// where we can control the digest changes and only allow changes that we want to allow.
//
// Note: This does not necessarily include all the fields that can be updated via the CS API, just the ones
// that are considered during an ARM NodePool update call and processed by the CS NodePool update dispatch controller.
type nodePoolUpdatableConfig struct {
	Labels                  map[string]string        `json:"labels,omitempty"`
	AutoScaling             *api.NodePoolAutoScaling `json:"autoScaling,omitempty"`
	Replicas                *int32                   `json:"replicas,omitempty"`
	Taints                  []api.Taint              `json:"taints,omitempty"`
	NodeDrainTimeoutMinutes *int32                   `json:"nodeDrainTimeoutMinutes,omitempty"`
}

// NodePoolUpdatableConfigFromProperties extracts the canonical updatable node pool
// configuration from internal API properties.
func NodePoolUpdatableConfigFromProperties(properties api.HCPOpenShiftClusterNodePoolProperties) *nodePoolUpdatableConfig {
	config := &nodePoolUpdatableConfig{
		Labels:                  properties.Labels,
		Taints:                  properties.Taints,
		NodeDrainTimeoutMinutes: properties.NodeDrainTimeoutMinutes,
	}

	if properties.AutoScaling != nil {
		autoScaling := *properties.AutoScaling
		config.AutoScaling = &autoScaling
	} else {
		replicas := properties.Replicas
		config.Replicas = &replicas
	}

	return config
}

// NodePoolUpdatableConfigHash returns a SHA-256 hex digest of
// nodePoolUpdatableConfig built from the node pool properties marshaled as a json map.
func NodePoolUpdatableConfigHash(nodePool *api.HCPOpenShiftClusterNodePool) (string, error) {
	config := NodePoolUpdatableConfigFromProperties(nodePool.Properties)

	raw, err := nodePoolUpdatableConfigJSONForHash(config)
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// nodePoolUpdatableConfigJSONForHash returns canonical JSON for hashing. The struct
// is marshaled first so json tags and omitempty apply, then round-tripped through
// map[string]any so object keys are emitted in sorted order at every level.
func nodePoolUpdatableConfigJSONForHash(config *nodePoolUpdatableConfig) ([]byte, error) {
	raw, err := json.Marshal(config)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to marshal node pool updatable config: %w", err))
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to unmarshal node pool updatable config: %w", err))
	}

	raw, err = json.Marshal(payload)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to marshal node pool updatable config payload: %w", err))
	}
	return raw, nil
}

func applyNodePoolUpdatableConfig(nodePoolBuilder *arohcpv1alpha1.NodePoolBuilder, config *nodePoolUpdatableConfig) {
	nodePoolBuilder.Labels(config.Labels)

	if config.AutoScaling != nil {
		nodePoolBuilder.Autoscaling(arohcpv1alpha1.NewNodePoolAutoscaling().
			MinReplica(int(config.AutoScaling.Min)).
			MaxReplica(int(config.AutoScaling.Max)))
	} else if config.Replicas != nil {
		nodePoolBuilder.Replicas(int(*config.Replicas))
	}

	if config.Taints != nil {
		taintBuilders := make([]*arohcpv1alpha1.TaintBuilder, 0, len(config.Taints))
		for _, t := range config.Taints {
			taintBuilders = append(taintBuilders, arohcpv1alpha1.NewTaint().
				Effect(string(t.Effect)).
				Key(t.Key).
				Value(t.Value))
		}
		nodePoolBuilder.Taints(taintBuilders...)
	}

	if config.NodeDrainTimeoutMinutes != nil {
		nodePoolBuilder.NodeDrainGracePeriod(arohcpv1alpha1.NewValue().
			Unit(csNodeDrainGracePeriodUnit).
			Value(float64(*config.NodeDrainTimeoutMinutes)))
	}
}
