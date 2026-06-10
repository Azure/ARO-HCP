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

// clusterUpdatableConfig is the canonical representation of cluster properties
// hashed by ClusterUpdatableConfigHash and applied to Cluster Service by
// applyClusterUpdatableConfig and applyClusterUpdatableAutoscalerConfig (via
// BuildCSCluster). Add or remove fields here and update ClusterUpdatableConfigFromCluster
// plus the apply helpers in the same change.
//
// The digest is stored on the ServiceProviderCluster as ClusterServiceUpdatableConfigHashForUpdateDispatch and
// compared by the cluster update dispatch controller: a mismatch triggers a CS PATCH and
// hash replacement. It is also stamped during cluster creation.
//
// Changing this struct has deploy-time effects:
//   - Removing a field (in the top-level or nested) changes the digest for every cluster that had that field marshalled. The marshalling of the
//     field depends on whether omitempty was set for the field and the actual value of the field for the corresponding Cluster.
//   - Adding a field (in the top-level or nested) changes the digest for every cluster that would start marshalling the field. The marshalling of
//     the field depends on whether omitempty is set for it and the actual value of the field for the corresponding Cluster.
//   - Renaming a json tag (in the top-level or nested) changes the digest for every cluster that had the field marshalled. The marshalling of the
//     field depends on whether omitempty was set for the field and the actual value of the field for the corresponding Cluster.
//
// In all of those cases, a change of digest implies a CS PATCH and hash replacement.
// Note: If in the future we consider that the previous behavior is too risky, we can consider having some sort of versioning of the config hash
// where we can control the digest changes and only allow changes that we want to allow.
//
// Note: This does not necessarily include all the fields that can be updated via the CS API, just the ones
// that are considered during an ARM Cluster update call and processed by the CS Cluster update dispatch controller.
type clusterUpdatableConfig struct {
	NodeDrainTimeoutMinutes int32                                `json:"nodeDrainTimeoutMinutes,omitempty"`
	AuthorizedCIDRs         []string                             `json:"authorizedCidrs,omitempty"`
	ImageDigestMirrors      []api.ImageDigestMirror              `json:"imageDigestMirrors,omitempty"`
	Autoscaling             api.ClusterAutoscalingProfile        `json:"autoscaling,omitzero"`
	ExperimentalFeatures    clusterUpdatableExperimentalFeatures `json:"experimentalFeatures,omitzero"`
}

// clusterUpdatableExperimentalFeatures is the curated experimental subset hashed
// and applied to CS. Do not embed api.ExperimentalFeatures: new fields added to
// that type for Cosmos or admission would otherwise enter the hash automatically.
type clusterUpdatableExperimentalFeatures struct {
	ControlPlaneAvailability  api.ControlPlaneAvailability `json:"singleReplica,omitempty"`
	ControlPlanePodSizing     api.ControlPlanePodSizing    `json:"sizeOverride,omitempty"`
	ControlPlaneOperatorImage string                       `json:"controlPlaneOperatorImage,omitempty"`
}

// ClusterUpdatableConfigFromCluster extracts the canonical updatable cluster
// configuration from the cluster's customer and service provider properties.
func ClusterUpdatableConfigFromCluster(cluster *api.HCPOpenShiftCluster) *clusterUpdatableConfig {
	return &clusterUpdatableConfig{
		NodeDrainTimeoutMinutes: cluster.CustomerProperties.NodeDrainTimeoutMinutes,
		AuthorizedCIDRs:         cluster.CustomerProperties.API.AuthorizedCIDRs,
		ImageDigestMirrors:      cluster.CustomerProperties.ImageDigestMirrors,
		Autoscaling:             cluster.CustomerProperties.Autoscaling,
		ExperimentalFeatures: clusterUpdatableExperimentalFeatures{
			ControlPlaneAvailability:  cluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneAvailability,
			ControlPlanePodSizing:     cluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlanePodSizing,
			ControlPlaneOperatorImage: cluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneOperatorImage,
		},
	}
}

// ClusterUpdatableConfigHash returns a SHA-256 hex digest of
// clusterUpdatableConfig built from the cluster properties marshaled as a json map.
func ClusterUpdatableConfigHash(cluster *api.HCPOpenShiftCluster) (string, error) {
	config := ClusterUpdatableConfigFromCluster(cluster)

	raw, err := clusterUpdatableConfigJSONForHash(config)
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// clusterUpdatableConfigJSONForHash returns canonical JSON for hashing. The struct
// is marshaled first so json tags and omitempty apply, then round-tripped through
// map[string]any so object keys are emitted in sorted order at every level.
func clusterUpdatableConfigJSONForHash(config *clusterUpdatableConfig) ([]byte, error) {
	raw, err := json.Marshal(config)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to marshal cluster updatable config: %w", err))
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to unmarshal cluster updatable config: %w", err))
	}

	raw, err = json.Marshal(payload)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to marshal cluster updatable config payload: %w", err))
	}
	return raw, nil
}

// applyClusterUpdatableConfig applies clusterUpdatableConfig to Cluster Service.
// baseProperties may be nil or contain arbitrary existing entries (for example base
// layers from old CS properties and caller requiredProperties). This function
// overlays updatable experimental feature flags onto that map and registers it
// on the builder. Keys are set to CSPropertyEnabled when enabled and deleted
// when disabled so tag removal clears previously set values.
func applyClusterUpdatableConfig(clusterBuilder *arohcpv1alpha1.ClusterBuilder, clusterAPIBuilder *arohcpv1alpha1.ClusterAPIBuilder, baseProperties map[string]string, config *clusterUpdatableConfig) error {
	if baseProperties == nil {
		baseProperties = map[string]string{}
	}

	clusterBuilder.NodeDrainGracePeriod(arohcpv1alpha1.NewValue().
		Unit(csNodeDrainGracePeriodUnit).
		Value(float64(config.NodeDrainTimeoutMinutes)))

	cidrBlockAccess, err := convertCIDRBlockAllowAccessRPToCS(api.CustomerAPIProfile{
		AuthorizedCIDRs: config.AuthorizedCIDRs,
	})
	if err != nil {
		return err
	}
	clusterBuilder.API(clusterAPIBuilder.CIDRBlockAccess(cidrBlockAccess))

	clusterBuilder.RegistryConfig(arohcpv1alpha1.NewClusterRegistryConfig().
		ImageDigestMirrors(convertImageDigestMirrorsToCSBuilder(config.ImageDigestMirrors)...))

	experimentalFeatures := config.ExperimentalFeatures
	if experimentalFeatures.ControlPlaneAvailability == api.SingleReplicaControlPlane {
		baseProperties[CSPropertySingleReplica] = CSPropertyEnabled
	} else {
		delete(baseProperties, CSPropertySingleReplica)
	}
	if experimentalFeatures.ControlPlanePodSizing == api.MinimalControlPlanePodSizing {
		baseProperties[CSPropertySizeOverride] = CSPropertyEnabled
	} else {
		delete(baseProperties, CSPropertySizeOverride)
	}
	if experimentalFeatures.ControlPlaneOperatorImage != "" {
		baseProperties[CSPropertyCPOImageOverride] = experimentalFeatures.ControlPlaneOperatorImage
	} else {
		delete(baseProperties, CSPropertyCPOImageOverride)
	}
	clusterBuilder.Properties(baseProperties)

	return nil
}

func applyClusterUpdatableAutoscalerConfig(config *clusterUpdatableConfig) (*arohcpv1alpha1.ClusterAutoscalerBuilder, error) {
	return convertRpAutoscalarToCSBuilder(&config.Autoscaling)
}
