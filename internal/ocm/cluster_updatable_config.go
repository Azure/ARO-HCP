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
	"time"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// clusterUpdatableConfig is the canonical representation of cluster properties
// extracted from RP or Cluster Service and applied to Cluster Service by
// applyClusterUpdatableConfig and applyClusterUpdatableAutoscalerConfig (via
// BuildCSCluster). Add or remove fields here and update ClusterUpdatableConfigFromCluster,
// ClusterUpdatableConfigFromClusterServiceCluster, plus the apply helpers in the same change.
//
// The cluster update dispatch controller compares desired and actual configs and
// sends a CS PATCH only when they differ.
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
	ControlPlaneAvailability api.ControlPlaneAvailability `json:"singleReplica,omitempty"`
	ControlPlanePodSizing    api.ControlPlanePodSizing    `json:"sizeOverride,omitempty"`
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
			ControlPlaneAvailability: cluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneAvailability,
			ControlPlanePodSizing:    cluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlanePodSizing,
		},
	}
}

// ClusterUpdatableConfigFromClusterServiceCluster extracts the canonical updatable
// cluster configuration from a Cluster Service cluster object.
func ClusterUpdatableConfigFromClusterServiceCluster(csCluster *arohcpv1alpha1.Cluster) (*clusterUpdatableConfig, error) {
	config := &clusterUpdatableConfig{}

	if nodeDrainGracePeriod := csCluster.NodeDrainGracePeriod(); nodeDrainGracePeriod != nil {
		value, ok := nodeDrainGracePeriod.GetValue()
		if !ok {
			return nil, utils.TrackError(fmt.Errorf("node drain grace period value is missing"))
		}
		config.NodeDrainTimeoutMinutes = int32(value)
	}

	if clusterAPI := csCluster.API(); clusterAPI != nil {
		authorizedCIDRs, err := authorizedCIDRsFromClusterServiceAPI(clusterAPI)
		if err != nil {
			return nil, err
		}
		config.AuthorizedCIDRs = authorizedCIDRs
	}

	if registryConfig := csCluster.RegistryConfig(); registryConfig != nil {
		imageDigestMirrors, ok := registryConfig.GetImageDigestMirrors()
		if ok && len(imageDigestMirrors) > 0 {
			config.ImageDigestMirrors = make([]api.ImageDigestMirror, 0, len(imageDigestMirrors))
			for _, mirror := range imageDigestMirrors {
				source, sourceOK := mirror.GetSource()
				mirrors, mirrorsOK := mirror.GetMirrors()
				if !sourceOK {
					continue
				}
				item := api.ImageDigestMirror{Source: source}
				if mirrorsOK {
					item.Mirrors = append([]string(nil), mirrors...)
				}
				config.ImageDigestMirrors = append(config.ImageDigestMirrors, item)
			}
		}
	}

	for key, value := range csCluster.Properties() {
		switch key {
		case CSPropertySingleReplica:
			if value == CSPropertyEnabled {
				config.ExperimentalFeatures.ControlPlaneAvailability = api.SingleReplicaControlPlane
			}
		case CSPropertySizeOverride:
			if value == CSPropertyEnabled {
				config.ExperimentalFeatures.ControlPlanePodSizing = api.MinimalControlPlanePodSizing
			}
		}
	}

	if autoscaler := csCluster.Autoscaler(); autoscaler != nil {
		autoscaling, err := convertCSAutoscalerToRP(autoscaler)
		if err != nil {
			return nil, err
		}
		config.Autoscaling = autoscaling
	}

	return config, nil
}

// ClusterUpdatableConfigDiffersFromClusterService reports whether the updatable
// configuration derived from the RP cluster differs from the live Cluster Service cluster.
func ClusterUpdatableConfigDiffersFromClusterService(cluster *api.HCPOpenShiftCluster, csCluster *arohcpv1alpha1.Cluster) (bool, error) {
	desiredHash, err := clusterUpdatableConfigHash(ClusterUpdatableConfigFromCluster(cluster))
	if err != nil {
		return false, err
	}

	actualConfig, err := ClusterUpdatableConfigFromClusterServiceCluster(csCluster)
	if err != nil {
		return false, err
	}
	actualHash, err := clusterUpdatableConfigHash(actualConfig)
	if err != nil {
		return false, err
	}

	return desiredHash != actualHash, nil
}

// ClusterUpdatableConfigHash returns a SHA-256 hex digest of
// clusterUpdatableConfig built from the cluster properties marshaled as a json map.
func ClusterUpdatableConfigHash(cluster *api.HCPOpenShiftCluster) (string, error) {
	return clusterUpdatableConfigHash(ClusterUpdatableConfigFromCluster(cluster))
}

func clusterUpdatableConfigHash(config *clusterUpdatableConfig) (string, error) {
	raw, err := clusterUpdatableConfigJSONForHash(config)
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func authorizedCIDRsFromClusterServiceAPI(clusterAPI *arohcpv1alpha1.ClusterAPI) ([]string, error) {
	cidrBlockAccess, ok := clusterAPI.GetCIDRBlockAccess()
	if !ok || cidrBlockAccess == nil {
		return nil, nil
	}

	allow, ok := cidrBlockAccess.GetAllow()
	if !ok || allow == nil {
		return nil, nil
	}

	mode, ok := allow.GetMode()
	if !ok {
		return nil, nil
	}

	switch mode {
	case csCIDRBlockAllowAccessModeAllowAll:
		return nil, nil
	case csCIDRBlockAllowAccessModeAllowList:
		values, ok := allow.GetValues()
		if !ok {
			return nil, utils.TrackError(fmt.Errorf("CIDR block allow list mode is missing values"))
		}
		return append([]string(nil), values...), nil
	default:
		return nil, utils.TrackError(fmt.Errorf("unknown CIDR block allow access mode %q", mode))
	}
}

func convertCSAutoscalerToRP(autoscaler *arohcpv1alpha1.ClusterAutoscaler) (api.ClusterAutoscalingProfile, error) {
	profile := api.ClusterAutoscalingProfile{}

	if maxNodeProvisionTime, ok := autoscaler.GetMaxNodeProvisionTime(); ok && maxNodeProvisionTime != "" {
		duration, err := time.ParseDuration(maxNodeProvisionTime)
		if err != nil {
			return profile, utils.TrackError(fmt.Errorf("failed to parse max node provision time %q: %w", maxNodeProvisionTime, err))
		}
		profile.MaxNodeProvisionTimeSeconds = int32(duration.Seconds())
	}

	if maxPodGracePeriod, ok := autoscaler.GetMaxPodGracePeriod(); ok {
		profile.MaxPodGracePeriodSeconds = int32(maxPodGracePeriod)
	}

	if podPriorityThreshold, ok := autoscaler.GetPodPriorityThreshold(); ok {
		profile.PodPriorityThreshold = int32(podPriorityThreshold)
	}

	if resourceLimits, ok := autoscaler.GetResourceLimits(); ok && resourceLimits != nil {
		if maxNodesTotal, ok := resourceLimits.GetMaxNodesTotal(); ok {
			profile.MaxNodesTotal = int32(maxNodesTotal)
		}
	}

	return profile, nil
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
	clusterBuilder.Properties(baseProperties)

	return nil
}

func applyClusterUpdatableAutoscalerConfig(config *clusterUpdatableConfig) (*arohcpv1alpha1.ClusterAutoscalerBuilder, error) {
	return convertRpAutoscalarToCSBuilder(&config.Autoscaling)
}
