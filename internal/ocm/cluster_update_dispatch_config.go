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
	"time"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api"
)

// clusterUpdateDispatchConfig is the set of properties that are updatable by RP Backend's cluster service
// cluster update dispatch controller, against Cluster Service.
// The cluster update dispatch controller compares desired and actual configs and
// sends a CS PATCH only when they differ.
//
// Note: This does not necessarily include all the fields that can be updated via the CS API, just the ones
// that are considered during an ARM Cluster update call and processed by RP Backend's cluster service
// cluster update dispatch controller.
//
// Do not embed internal/api struct types (for example api.ClusterAutoscalingProfile,
// api.ImageDigestMirror, or api.ExperimentalFeatures) in this struct or its nested field types.
// Those api structs evolve for Cosmos persistence, admission, and new API versions. If a field used
// an api struct directly, any new field added to that struct would be marshaled into the config hash
// and treated as updatable automatically, even when the cluster update dispatch controller does not
// read or apply it. Instead, define curated local structs with only the fields that dispatch should
// hash and sync, and copy values explicitly from api types at the conversion boundaries.
//
// Using api enum or scalar types for individual curated fields is fine (for example
// api.ControlPlaneAvailability). Adding a new enum constant does not add a new config field; only
// adding a new field to the curated local struct would.
type clusterUpdateDispatchConfig struct {
	NodeDrainTimeoutMinutes     int32                                           `json:"nodeDrainTimeoutMinutes,omitempty"`
	K8sAPIServerAuthorizedCIDRs []string                                        `json:"k8sAPIServerAuthorizedCIDRs,omitempty"`
	ImageDigestMirrors          []clusterUpdateDispatchConfigImageDigestMirror  `json:"imageDigestMirrors,omitempty"`
	Autoscaling                 clusterUpdateDispatchConfigAutoscaling          `json:"autoscaling,omitempty"`
	ExperimentalFeatures        clusterUpdateDispatchConfigExperimentalFeatures `json:"experimentalFeatures,omitempty"`
}

// clusterUpdateDispatchConfigImageDigestMirror is the curated image mirror subset hashed
// and applied to CS. See clusterUpdateDispatchConfig: do not embed api.ImageDigestMirror.
type clusterUpdateDispatchConfigImageDigestMirror struct {
	Source  string   `json:"source,omitempty"`
	Mirrors []string `json:"mirrors,omitempty"`
}

// clusterUpdateDispatchConfigAutoscaling is the curated autoscaling subset hashed
// and applied to CS. See clusterUpdateDispatchConfig: do not embed api.ClusterAutoscalingProfile.
type clusterUpdateDispatchConfigAutoscaling struct {
	MaxNodesTotal               int32 `json:"maxNodesTotal,omitempty"`
	MaxPodGracePeriodSeconds    int32 `json:"maxPodGracePeriodSeconds,omitempty"`
	MaxNodeProvisionTimeSeconds int32 `json:"maxNodeProvisionTimeSeconds,omitempty"`
	PodPriorityThreshold        int32 `json:"podPriorityThreshold,omitempty"`
}

// clusterUpdateDispatchConfigExperimentalFeatures is the curated experimental subset hashed
// and applied to CS. See clusterUpdateDispatchConfig: do not embed api.ExperimentalFeatures.
// Individual api enum fields (ControlPlaneAvailability, ControlPlanePodSizing) are intentional.
type clusterUpdateDispatchConfigExperimentalFeatures struct {
	ControlPlaneAvailability  api.ControlPlaneAvailability `json:"controlPlaneAvailability,omitempty"`
	ControlPlanePodSizing     api.ControlPlanePodSizing    `json:"controlPlanePodSizing,omitempty"`
	ControlPlaneOperatorImage string                       `json:"controlPlaneOperatorImage,omitempty"`
}

// ClusterUpdateDispatchConfigDiffers reports whether the dispatch-managed configuration
// derived from the RP cluster differs from the live Cluster Service cluster. The comparison
// uses a SHA-256 hash of each side's canonical JSON representation.
func ClusterUpdateDispatchConfigDiffers(cluster *api.HCPOpenShiftCluster, csCluster *arohcpv1alpha1.Cluster) (bool, error) {
	desiredConfig := clusterUpdateDispatchConfigFromRP(cluster)
	desiredHash, err := desiredConfig.hash()
	if err != nil {
		return false, err
	}

	actualConfig, err := clusterUpdateDispatchConfigFromCS(csCluster)
	if err != nil {
		return false, err
	}
	actualHash, err := actualConfig.hash()
	if err != nil {
		return false, err
	}

	return desiredHash != actualHash, nil
}

// ClusterUpdateDispatchConfigJSONFromRP returns the canonical JSON representation of the
// dispatch-managed configuration derived from the RP cluster.
func ClusterUpdateDispatchConfigJSONFromRP(cluster *api.HCPOpenShiftCluster) (string, error) {
	raw, err := clusterUpdateDispatchConfigFromRP(cluster).canonicalJSON()
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// ClusterUpdateDispatchConfigJSONFromCS returns the canonical JSON representation of the
// dispatch-managed configuration derived from a Cluster Service cluster.
func ClusterUpdateDispatchConfigJSONFromCS(csCluster *arohcpv1alpha1.Cluster) (string, error) {
	config, err := clusterUpdateDispatchConfigFromCS(csCluster)
	if err != nil {
		return "", err
	}
	raw, err := config.canonicalJSON()
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func clusterUpdateDispatchConfigFromRP(cluster *api.HCPOpenShiftCluster) *clusterUpdateDispatchConfig {
	return &clusterUpdateDispatchConfig{
		NodeDrainTimeoutMinutes:     cluster.CustomerProperties.NodeDrainTimeoutMinutes,
		K8sAPIServerAuthorizedCIDRs: cluster.CustomerProperties.API.AuthorizedCIDRs,
		ImageDigestMirrors:          clusterUpdateDispatchConfigImageDigestMirrorsFromRP(cluster.CustomerProperties.ImageDigestMirrors),
		Autoscaling:                 clusterUpdateDispatchConfigAutoscalingFromRP(cluster.CustomerProperties.Autoscaling),
		ExperimentalFeatures: clusterUpdateDispatchConfigExperimentalFeatures{
			ControlPlaneAvailability:  cluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneAvailability,
			ControlPlanePodSizing:     cluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlanePodSizing,
			ControlPlaneOperatorImage: cluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneOperatorImage,
		},
	}
}

func clusterUpdateDispatchConfigImageDigestMirrorsFromRP(mirrors []api.ImageDigestMirror) []clusterUpdateDispatchConfigImageDigestMirror {
	if len(mirrors) == 0 {
		return nil
	}

	out := make([]clusterUpdateDispatchConfigImageDigestMirror, 0, len(mirrors))
	for _, mirror := range mirrors {
		out = append(out, clusterUpdateDispatchConfigImageDigestMirror{
			Source:  mirror.Source,
			Mirrors: append([]string(nil), mirror.Mirrors...),
		})
	}
	return out
}

func clusterUpdateDispatchConfigAutoscalingFromRP(profile api.ClusterAutoscalingProfile) clusterUpdateDispatchConfigAutoscaling {
	return clusterUpdateDispatchConfigAutoscaling{
		MaxNodesTotal:               profile.MaxNodesTotal,
		MaxPodGracePeriodSeconds:    profile.MaxPodGracePeriodSeconds,
		MaxNodeProvisionTimeSeconds: profile.MaxNodeProvisionTimeSeconds,
		PodPriorityThreshold:        profile.PodPriorityThreshold,
	}
}

// clusterUpdateDispatchConfigFromCS extracts the canonical dispatch-managed cluster
// configuration from a Cluster Service cluster object.
func clusterUpdateDispatchConfigFromCS(csCluster *arohcpv1alpha1.Cluster) (*clusterUpdateDispatchConfig, error) {
	config := &clusterUpdateDispatchConfig{}

	config.NodeDrainTimeoutMinutes = ClusterUpdateDispatchConfigNodeDrainTimeoutFromCS(csCluster)
	config.K8sAPIServerAuthorizedCIDRs = ClusterUpdateDispatchConfigAuthorizedCIDRsFromCS(csCluster.API())
	config.ImageDigestMirrors = clusterUpdateDispatchConfigImageDigestMirrorsFromCS(csCluster.RegistryConfig())
	config.ExperimentalFeatures = clusterUpdateDispatchConfigExperimentalFeaturesFromCS(csCluster)

	autoscaling, err := clusterUpdateDispatchConfigAutoscalingFromCS(csCluster.Autoscaler())
	if err != nil {
		return nil, err
	}
	config.Autoscaling = autoscaling

	return config, nil
}

// ClusterUpdateDispatchConfigNodeDrainTimeoutFromCS extracts the node drain timeout in
// minutes from a Cluster Service cluster object.
func ClusterUpdateDispatchConfigNodeDrainTimeoutFromCS(in *arohcpv1alpha1.Cluster) int32 {
	if nodeDrainGracePeriod, ok := in.GetNodeDrainGracePeriod(); ok {
		if unit, ok := nodeDrainGracePeriod.GetUnit(); ok && unit == csNodeDrainGracePeriodUnit {
			return int32(nodeDrainGracePeriod.Value())
		}
	}
	return 0
}

// ClusterUpdateDispatchConfigAuthorizedCIDRsFromCS extracts customer authorized CIDRs from
// a Cluster Service cluster API configuration.
func ClusterUpdateDispatchConfigAuthorizedCIDRsFromCS(in *arohcpv1alpha1.ClusterAPI) []string {
	cidrAccess := in.CIDRBlockAccess()
	if cidrAccess.Empty() {
		return nil
	}
	if cidr := cidrAccess.Allow(); cidr != nil {
		return cidr.Values()
	}
	return nil
}

func clusterUpdateDispatchConfigImageDigestMirrorsFromCS(in *arohcpv1alpha1.ClusterRegistryConfig) []clusterUpdateDispatchConfigImageDigestMirror {
	if in == nil {
		return nil
	}
	imageDigestMirrors := in.ImageDigestMirrors()
	if len(imageDigestMirrors) == 0 {
		return nil
	}

	out := make([]clusterUpdateDispatchConfigImageDigestMirror, 0, len(imageDigestMirrors))
	for _, mirror := range imageDigestMirrors {
		source, sourceOK := mirror.GetSource()
		if !sourceOK {
			continue
		}
		item := clusterUpdateDispatchConfigImageDigestMirror{Source: source}

		mirrors, mirrorsOK := mirror.GetMirrors()
		if mirrorsOK {
			item.Mirrors = append([]string(nil), mirrors...)
		}
		out = append(out, item)
	}
	return out
}

func clusterUpdateDispatchConfigExperimentalFeaturesFromCS(in *arohcpv1alpha1.Cluster) clusterUpdateDispatchConfigExperimentalFeatures {
	if in == nil {
		return clusterUpdateDispatchConfigExperimentalFeatures{}
	}

	out := clusterUpdateDispatchConfigExperimentalFeatures{}

	for key, value := range in.Properties() {
		switch key {
		case CSPropertySingleReplica:
			if value == CSPropertyEnabled {
				out.ControlPlaneAvailability = api.SingleReplicaControlPlane
			}
		case CSPropertySizeOverride:
			if value == CSPropertyEnabled {
				out.ControlPlanePodSizing = api.MinimalControlPlanePodSizing
			}
		case CSPropertyCPOImageOverride:
			if value != "" {
				out.ControlPlaneOperatorImage = value
			}
		}
	}

	return out
}

func clusterUpdateDispatchConfigAutoscalingFromCS(in *arohcpv1alpha1.ClusterAutoscaler) (clusterUpdateDispatchConfigAutoscaling, error) {
	if in == nil {
		return clusterUpdateDispatchConfigAutoscaling{}, nil
	}

	var maxNodeProvisionTime int32
	if len(in.MaxNodeProvisionTime()) > 0 {
		// maxNodeProvisionTime (string) - minutes e.g - “15m”
		// https://gitlab.cee.redhat.com/service/uhc-clusters-service/-/blob/master/pkg/api/autoscaler.go?ref_type=heads#L30-42
		maxNodeProvisionTimeDuration, err := time.ParseDuration(in.MaxNodeProvisionTime())
		if err != nil {
			return clusterUpdateDispatchConfigAutoscaling{}, err
		}
		maxNodeProvisionTime = int32(maxNodeProvisionTimeDuration.Seconds())
	}

	return clusterUpdateDispatchConfigAutoscaling{
		MaxNodesTotal: int32(in.ResourceLimits().MaxNodesTotal()),
		// MaxPodGracePeriod (int) - seconds e.g - 300
		// https://gitlab.cee.redhat.com/service/uhc-clusters-service/-/blob/master/pkg/api/autoscaler.go?ref_type=heads#L30-42
		MaxPodGracePeriodSeconds:    int32(in.MaxPodGracePeriod()),
		MaxNodeProvisionTimeSeconds: maxNodeProvisionTime,
		PodPriorityThreshold:        int32(in.PodPriorityThreshold()),
	}, nil
}

func clusterUpdateDispatchConfigHash(cluster *api.HCPOpenShiftCluster) (string, error) {
	return clusterUpdateDispatchConfigFromRP(cluster).hash()
}

func (c *clusterUpdateDispatchConfig) hash() (string, error) {
	return hashUpdateDispatchConfig(c)
}

func (c *clusterUpdateDispatchConfig) canonicalJSON() ([]byte, error) {
	return canonicalJSONForUpdateDispatchConfig(c)
}

// applyToCSBuilders applies the config onto Cluster Service builders. baseProperties may be nil or contain
// arbitrary existing entries (for example base layers from old CS properties and caller
// requiredProperties). This method overlays dispatch-managed experimental feature flags
// onto that map and registers it on the builder. Keys are set to CSPropertyEnabled when
// enabled and deleted when disabled so tag removal clears previously set values.
func (c *clusterUpdateDispatchConfig) applyToCSBuilders(clusterBuilder *arohcpv1alpha1.ClusterBuilder, clusterAPIBuilder *arohcpv1alpha1.ClusterAPIBuilder, baseProperties map[string]string) error {
	if baseProperties == nil {
		baseProperties = map[string]string{}
	}

	clusterBuilder.NodeDrainGracePeriod(arohcpv1alpha1.NewValue().
		Unit(csNodeDrainGracePeriodUnit).
		Value(float64(c.NodeDrainTimeoutMinutes)))

	cidrBlockAccess, err := convertCIDRBlockAllowAccessRPToCS(api.CustomerAPIProfile{
		AuthorizedCIDRs: c.K8sAPIServerAuthorizedCIDRs,
	})
	if err != nil {
		return err
	}
	clusterBuilder.API(clusterAPIBuilder.CIDRBlockAccess(cidrBlockAccess))

	clusterBuilder.RegistryConfig(arohcpv1alpha1.NewClusterRegistryConfig().
		ImageDigestMirrors(convertImageDigestMirrorsToCSBuilder(clusterUpdateDispatchConfigImageDigestMirrorsToRP(c.ImageDigestMirrors))...))

	experimentalFeatures := c.ExperimentalFeatures
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

func (c *clusterUpdateDispatchConfig) autoscalerBuilder() (*arohcpv1alpha1.ClusterAutoscalerBuilder, error) {
	profile := clusterUpdateDispatchConfigAutoscalingToRP(c.Autoscaling)
	return convertRpAutoscalarToCSBuilder(&profile)
}

func clusterUpdateDispatchConfigImageDigestMirrorsToRP(mirrors []clusterUpdateDispatchConfigImageDigestMirror) []api.ImageDigestMirror {
	if len(mirrors) == 0 {
		return nil
	}

	out := make([]api.ImageDigestMirror, 0, len(mirrors))
	for _, mirror := range mirrors {
		out = append(out, api.ImageDigestMirror{
			Source:  mirror.Source,
			Mirrors: append([]string(nil), mirror.Mirrors...),
		})
	}
	return out
}

func clusterUpdateDispatchConfigAutoscalingToRP(profile clusterUpdateDispatchConfigAutoscaling) api.ClusterAutoscalingProfile {
	return api.ClusterAutoscalingProfile{
		MaxNodesTotal:               profile.MaxNodesTotal,
		MaxPodGracePeriodSeconds:    profile.MaxPodGracePeriodSeconds,
		MaxNodeProvisionTimeSeconds: profile.MaxNodeProvisionTimeSeconds,
		PodPriorityThreshold:        profile.PodPriorityThreshold,
	}
}
