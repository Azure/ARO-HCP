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

	"k8s.io/utils/ptr"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api"
)

// containerRegistryPullMIClearingSignal is a sentinel used by BuildCSCluster to tell
// applyToCSBuilders to send an empty-string ResourceID to CS, which CS interprets as
// "unset the container registry". Distinct from "" (field not configured).
const containerRegistryPullMIClearingSignal = "\x00clear"

// clusterUpdateDispatchConfig is a dispatch-specific canonical model of the Cluster's
// Cluster Service fields that are considered by the cluster's cluster service update dispatch controller. Its shape
// intentionally does not mirror RP resources or the Cluster Service API. Conversion functions
// project external state into this form and back out when dispatching to Cluster Service.
//
// The same struct is built from either RP desired state (from one or more RP resources,
// currently HCPOpenShiftCluster and ServiceProviderCluster) or from the live Cluster Service
// Cluster. This applies only to Cluster CS updates. Node pool and external auth updates
// use separate dispatch paths and update dispatch config structs. Drift between the two projections
// may trigger the cluster's cluster service update dispatch controller to
// PATCH Cluster Service.
//
// The cluster's cluster service update dispatch controller compares desired and
// actual configs in this canonical form and sends a CS PATCH only when they differ.
//
// Note: This does not include all fields updatable via the Cluster's Cluster Service API, only
// the subset that the cluster's cluster service update dispatch controller considers.
//
// Note: Do not embed internal/api struct types (for example api.ClusterAutoscalingProfile,
// api.ImageDigestMirror, or api.ExperimentalFeatures) in this struct or its nested field types. We want to make
// those internal/api struct types independent of this so they can evolve independently. For example, if a field here
// referenced an internal/api struct type directly, any new field added to that struct would be automatically considered
// as updatable automatically, but we might not want that field to be updatable and/or CS side doesn't really support
// updating it. Instead, define curated local structs with only the fields that dispatch should
// hash and sync, and copy values explicitly from api types at the conversion boundaries. Using api/internal enum or
// scalar types for individual curated fields is fine (for example api.ControlPlaneAvailability). This is because
// adding an enum/scalar field they do not pull extra fields, but adding struct types does.
//
// IMPORTANT: how to add a new dispatch-managed config field:
//
// Dispatch and operation state are related but wired separately. You need both for a correct
// cluster update experience:
//   - Dispatch (this file): detects drift and PATCHes Cluster Service.
//   - Operation state (operation_cluster_update_state_calculation.go): decides when the ARM
//     cluster update operation can leave Updating and report Succeeded.
//
// If you wire dispatch but skip operation state calculation, the update may be sent to CS but the operation
// can succeed too early (or stay Updating forever if dispatch is missing).
//
// Before you start:
//
//   - Confirm this is a cluster-level Cluster Service update (not node pool or external auth).
//   - Confirm Cluster Service supports updating the field on an existing cluster.
//   - Identify where RP desired state lives (HCPOpenShiftCluster, ServiceProviderCluster, etc.).
//
// 1. Dispatch wiring (this file)
//
//   - Add the field to clusterUpdateDispatchConfig or a curated nested struct.
//     Do not embed internal/api struct types.
//   - Populate it in clusterUpdateDispatchConfigFromRP. This ensures RP projection works correctly
//   - Populate it in clusterUpdateDispatchConfigFromCS. This ensures CS projection works correctly
//   - Apply it in applyToCSBuilders. This ensures the CS builders work correctly.
//
// 2. Operation state wiring (backend/pkg/controllers/operationcontrollers/operation_cluster_update.go)
//
//	determineOperationState aggregates several sources and picks the worst state. Your new
//	check must succeed along with version resolution, CS status, CS spec, and Hypershift checks.
//
//	Choose where to observe the change and implement it:
//	  - Prefer observing directly from the Management Cluster side when the field is visible there after propagation.
//	    Most dispatch fields today are checked here: autoscaling, image mirrors, experimental features, etc.
//	  - Observe from Cluster Service when the Management Cluster side is not a reliable source of
//	    truth yet or simply can't be calculated from there
//	  - Sometimes you might want to observe it on both sides for extra validation
//
//	Return (false, message) with a clear message when observed != desired so Updating state
//	is actionable in logs.
//
// 3. Tests
//
//   - cluster_update_dispatch_config_test.go: hash, FromCS, round-trip, apply payload.
//   - operation_cluster_update_state_calculation_test.go: match/mismatch cases for your new
//     helper and, if useful, an end-to-end clusterServiceClusterSpecOperationState or
//     hypershiftClusterOperationState case.
//   - Consider both "not applied yet" (Updating) and "applied" (Succeeded) scenarios.
//
// 4. Sanity checks
//
//   - Create path: applyToCSBuilders is also used by BuildCSCluster in internal/ocm/convert.go
//     when the frontend first creates a Cluster Service cluster (not only when the update
//     dispatch controller PATCHes an existing one). Verify the new field is present on create.
//   - Desired state must exist in Cosmos before dispatch can sync it. If customers set this
//     field via ARM, also wire the full ingest path: ARM API, frontend validation/conversion,
//     and persistence onto HCPOpenShiftCluster or ServiceProviderCluster. Internal-only
//     fields still need whatever backend path writes the value Cosmos holds.
type clusterUpdateDispatchConfig struct {
	NodeDrainTimeoutMinutes                        int32                                                     `json:"nodeDrainTimeoutMinutes,omitempty"`
	K8sAPIServerAuthorizedCIDRs                    []string                                                  `json:"k8sAPIServerAuthorizedCIDRs,omitempty"`
	ImageDigestMirrors                             []clusterUpdateDispatchConfigImageDigestMirror            `json:"imageDigestMirrors,omitempty"`
	Autoscaling                                    clusterUpdateDispatchConfigAutoscaling                    `json:"autoscaling,omitempty"`
	ExperimentalFeatures                           clusterUpdateDispatchConfigExperimentalFeatures           `json:"experimentalFeatures,omitempty"`
	ServiceProviderClusterDispatch                 clusterUpdateDispatchConfigServiceProviderClusterDispatch `json:"serviceProviderClusterDispatch,omitempty"`
	Etcd                                           clusterUpdateDispatchConfigEtcd                           `json:"etcd,omitempty"`
	ContainerRegistryPullManagedIdentityResourceID string                                                    `json:"containerRegistryPullManagedIdentityResourceID,omitempty"`
}

// clusterUpdateDispatchConfigImageDigestMirror is the curated image mirror subset used for
// dispatch hash and sync. See clusterUpdateDispatchConfig: do not embed api.ImageDigestMirror.
type clusterUpdateDispatchConfigImageDigestMirror struct {
	Source  string   `json:"source,omitempty"`
	Mirrors []string `json:"mirrors,omitempty"`
}

// clusterUpdateDispatchConfigAutoscaling is the curated autoscaling subset used for dispatch
// hash and sync. See clusterUpdateDispatchConfig: do not embed api.ClusterAutoscalingProfile.
type clusterUpdateDispatchConfigAutoscaling struct {
	MaxNodesTotal               int32 `json:"maxNodesTotal,omitempty"`
	MaxPodGracePeriodSeconds    int32 `json:"maxPodGracePeriodSeconds,omitempty"`
	MaxNodeProvisionTimeSeconds int32 `json:"maxNodeProvisionTimeSeconds,omitempty"`
	PodPriorityThreshold        int32 `json:"podPriorityThreshold,omitempty"`
}

// clusterUpdateDispatchConfigExperimentalFeatures is the curated experimental subset used for
// dispatch hash and sync. See clusterUpdateDispatchConfig: do not embed api.ExperimentalFeatures.
type clusterUpdateDispatchConfigExperimentalFeatures struct {
	ControlPlaneAvailability  api.ControlPlaneAvailability `json:"controlPlaneAvailability,omitempty"`
	ControlPlanePodSizing     api.ControlPlanePodSizing    `json:"controlPlanePodSizing,omitempty"`
	ControlPlaneOperatorImage string                       `json:"controlPlaneOperatorImage,omitempty"`
}

// clusterUpdateDispatchConfigServiceProviderClusterDispatch holds the dispatch-managed
// subset of api.ServiceProviderCluster fields included in cluster update dispatch.
type clusterUpdateDispatchConfigServiceProviderClusterDispatch struct {
	DesiredHostedClusterControlPlaneSize *string `json:"desiredHostedClusterControlPlaneSize,omitempty"`
}

type clusterUpdateDispatchConfigEtcd struct {
	DataEncryption clusterUpdateDispatchConfigEtcdDataEncryption `json:"dataEncryption,omitempty"`
}

type clusterUpdateDispatchConfigEtcdDataEncryption struct {
	CustomerManaged *clusterUpdateDispatchConfigEtcdDataEncryptionCustomerManaged `json:"customerManaged,omitempty"`
}

type clusterUpdateDispatchConfigEtcdDataEncryptionCustomerManaged struct {
	Kms *clusterUpdateDispatchConfigEtcdDataEncryptionCustomerManagedKms `json:"kms,omitempty"`
}

type clusterUpdateDispatchConfigEtcdDataEncryptionCustomerManagedKms struct {
	ActiveKey clusterUpdateDispatchConfigEtcdDataEncryptionCustomerManagedKmsActiveKey `json:"activeKey,omitempty"`
}

type clusterUpdateDispatchConfigEtcdDataEncryptionCustomerManagedKmsActiveKey struct {
	Version string `json:"version,omitempty"`
}

// ClusterUpdateDispatchConfigJSONFromRP returns the canonical JSON of the dispatch config
// projected from RP desired state.
func ClusterUpdateDispatchConfigJSONFromRP(cluster *api.HCPOpenShiftCluster, serviceProviderCluster *api.ServiceProviderCluster) (string, error) {
	raw, err := clusterUpdateDispatchConfigFromRP(cluster, serviceProviderCluster).canonicalJSON()
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// ClusterUpdateDispatchConfigJSONFromCS returns the canonical JSON of the dispatch config
// projected from a Cluster Service cluster.
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

// clusterUpdateDispatchConfigFromRP projects RP desired state into the dispatch canonical form.
func clusterUpdateDispatchConfigFromRP(cluster *api.HCPOpenShiftCluster, serviceProviderCluster *api.ServiceProviderCluster) *clusterUpdateDispatchConfig {
	var containerRegistryPullMIResourceID string
	if cluster.CustomerProperties.Platform.ContainerRegistryPullManagedIdentity != nil {
		containerRegistryPullMIResourceID = cluster.CustomerProperties.Platform.ContainerRegistryPullManagedIdentity.String()
	}

	res := &clusterUpdateDispatchConfig{
		NodeDrainTimeoutMinutes:     cluster.CustomerProperties.NodeDrainTimeoutMinutes,
		K8sAPIServerAuthorizedCIDRs: cluster.CustomerProperties.API.AuthorizedCIDRs,
		ImageDigestMirrors:          clusterUpdateDispatchConfigImageDigestMirrorsFromRP(cluster.CustomerProperties.ImageDigestMirrors),
		Autoscaling:                 clusterUpdateDispatchConfigAutoscalingFromRP(cluster.CustomerProperties.Autoscaling),
		ExperimentalFeatures: clusterUpdateDispatchConfigExperimentalFeatures{
			ControlPlaneAvailability:  cluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneAvailability,
			ControlPlanePodSizing:     cluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlanePodSizing,
			ControlPlaneOperatorImage: cluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneOperatorImage,
		},
		ServiceProviderClusterDispatch: clusterUpdateDispatchConfigServiceProviderClusterDispatch{},
		Etcd:                           clusterUpdateDispatchEtcdFromRP(cluster.CustomerProperties.Etcd),
		ContainerRegistryPullManagedIdentityResourceID: containerRegistryPullMIResourceID,
	}

	if serviceProviderCluster != nil {
		res.ServiceProviderClusterDispatch.DesiredHostedClusterControlPlaneSize = serviceProviderCluster.Spec.DesiredHostedClusterControlPlaneSize
	}

	return res
}

// clusterUpdateDispatchEtcdFromRP copies kms active key version from RP etcd configuration into the
// dispatch canonical form. Returns zero value when the cluster does not use customer-managed KMS.
func clusterUpdateDispatchEtcdFromRP(etcd api.EtcdProfile) clusterUpdateDispatchConfigEtcd {
	if etcd.DataEncryption.KeyManagementMode != api.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged {
		return clusterUpdateDispatchConfigEtcd{}
	}

	res := clusterUpdateDispatchConfigEtcd{}
	if etcd.DataEncryption.CustomerManaged.EncryptionType == api.CustomerManagedEncryptionTypeKMS {
		res.DataEncryption.CustomerManaged = &clusterUpdateDispatchConfigEtcdDataEncryptionCustomerManaged{
			Kms: &clusterUpdateDispatchConfigEtcdDataEncryptionCustomerManagedKms{
				ActiveKey: clusterUpdateDispatchConfigEtcdDataEncryptionCustomerManagedKmsActiveKey{
					Version: etcd.DataEncryption.CustomerManaged.Kms.ActiveKey.Version,
				},
			},
		}
	}

	return res
}

// clusterUpdateDispatchConfigImageDigestMirrorsFromRP copies image mirrors from RP into the
// dispatch canonical form.
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

// clusterUpdateDispatchConfigAutoscalingFromRP copies autoscaling settings from RP into the
// dispatch canonical form.
func clusterUpdateDispatchConfigAutoscalingFromRP(profile api.ClusterAutoscalingProfile) clusterUpdateDispatchConfigAutoscaling {
	return clusterUpdateDispatchConfigAutoscaling{
		MaxNodesTotal:               profile.MaxNodesTotal,
		MaxPodGracePeriodSeconds:    profile.MaxPodGracePeriodSeconds,
		MaxNodeProvisionTimeSeconds: profile.MaxNodeProvisionTimeSeconds,
		PodPriorityThreshold:        profile.PodPriorityThreshold,
	}
}

// clusterUpdateDispatchConfigFromCS projects a Cluster Service cluster into the dispatch
// canonical form.
func clusterUpdateDispatchConfigFromCS(csCluster *arohcpv1alpha1.Cluster) (*clusterUpdateDispatchConfig, error) {
	config := &clusterUpdateDispatchConfig{}

	config.NodeDrainTimeoutMinutes = ClusterUpdateDispatchConfigNodeDrainTimeoutFromCS(csCluster)
	config.K8sAPIServerAuthorizedCIDRs = ClusterUpdateDispatchConfigAuthorizedCIDRsFromCS(csCluster.API())
	config.ImageDigestMirrors = clusterUpdateDispatchConfigImageDigestMirrorsFromCS(csCluster.RegistryConfig())
	config.Etcd = clusterUpdateDispatchConfigEtcdFromCS(csCluster.Azure())
	config.ExperimentalFeatures = clusterUpdateDispatchConfigExperimentalFeaturesFromCS(csCluster)
	config.ServiceProviderClusterDispatch.DesiredHostedClusterControlPlaneSize = clusterUpdateDispatchConfigServiceProviderClusterDispatchDesiredHostedClusterControlPlaneSizeFromCS(csCluster)
	config.ContainerRegistryPullManagedIdentityResourceID = ClusterUpdateDispatchConfigContainerRegistryPullMIFromCS(csCluster.Azure())
	autoscaling, err := clusterUpdateDispatchConfigAutoscalingFromCS(csCluster.Autoscaler())
	if err != nil {
		return nil, err
	}
	config.Autoscaling = autoscaling

	return config, nil
}

// ClusterUpdateDispatchConfigNodeDrainTimeoutFromCS extracts the node drain timeout in
// minutes from a Cluster Service cluster. Returns 0 when unset or not expressed in minutes.
func ClusterUpdateDispatchConfigNodeDrainTimeoutFromCS(in *arohcpv1alpha1.Cluster) int32 {
	if nodeDrainGracePeriod, ok := in.GetNodeDrainGracePeriod(); ok {
		if unit, ok := nodeDrainGracePeriod.GetUnit(); ok && unit == csNodeDrainGracePeriodUnit {
			return int32(nodeDrainGracePeriod.Value())
		}
	}
	return 0
}

// ClusterUpdateDispatchConfigAuthorizedCIDRsFromCS extracts customer authorized CIDRs from
// a Cluster Service cluster API configuration. Returns nil when access is not allow-list mode.
func ClusterUpdateDispatchConfigAuthorizedCIDRsFromCS(in *arohcpv1alpha1.ClusterAPI) []string {
	cidrAccess := in.CIDRBlockAccess()
	if cidrAccess.Empty() {
		return nil
	}

	allow := cidrAccess.Allow()
	if allow == nil {
		return nil
	}

	allowMode, _ := allow.GetMode()
	// We assume if it's not allow_list mode then it's allow_all mode so there are no CIDRs
	if allowMode != CSCIDRBlockAllowAccessModeAllowList {
		return nil
	}

	allowValues := allow.Values()
	return allowValues
}

// clusterUpdateDispatchConfigImageDigestMirrorsFromCS extracts image mirrors from a Cluster
// Service cluster registry config into the dispatch canonical form.
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

// clusterUpdateDispatchConfigExperimentalFeaturesFromCS extracts experimental feature flags from
// Cluster Service cluster properties into the dispatch canonical form.
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
			// We only set the cluster level ControlPlanePodSizing attribute when
			// the returned value from CS is one of the ones defined as a api.ControlPlanePodSizing.
			// If it were to have a non empty value that is not among those, we consider it's a
			// size specified via the ServiceProviderCluster's spec.
			if value == CSPropertyE2EMinimalControlPlaneSize {
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

// clusterUpdateDispatchConfigServiceProviderClusterDispatchDesiredHostedClusterControlPlaneSizeFromCS extracts
// the ServiceProviderCluster-hosted control plane size override from Cluster Service properties.
// Returns nil when the size override is absent or encoded as a cluster-level ControlPlanePodSizing.
func clusterUpdateDispatchConfigServiceProviderClusterDispatchDesiredHostedClusterControlPlaneSizeFromCS(in *arohcpv1alpha1.Cluster) *string {
	if in == nil {
		return nil
	}

	property, found := in.Properties()[CSPropertySizeOverride]
	if !found {
		return nil
	}

	if property == "" {
		return nil
	}

	// We do not set this attribute if the CS value matches any of the ones that match to a corresponding
	// api.ControlPlanePodSizing.
	if property == CSPropertyE2EMinimalControlPlaneSize {
		return nil
	}

	// When the property value does not match any of the ones any of the ones that match to a corresponding
	// api.ControlPlanePodSizing then we assume that its value comes from having it being set beforehand through
	// ServiceProviderCluster's spec.
	return ptr.To(property)
}

// clusterUpdateDispatchConfigAutoscalingFromCS extracts autoscaling settings from a Cluster
// Service autoscaler into the dispatch canonical form.
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

// clusterUpdateDispatchConfigActiveKeyVersionFromCS extracts the dispatch-managed KMS active key version
// from a Cluster Service cluster. Returns zero value when the cluster does not use customer-managed KMS.
func clusterUpdateDispatchConfigEtcdFromCS(in *arohcpv1alpha1.Azure) clusterUpdateDispatchConfigEtcd {

	etcdEncryption, ok := in.GetEtcdEncryption()
	if !ok || etcdEncryption == nil {
		// platform managed
		return clusterUpdateDispatchConfigEtcd{}
	}
	dataEncryption, ok := etcdEncryption.GetDataEncryption()
	if !ok || dataEncryption == nil {
		// platform managed
		return clusterUpdateDispatchConfigEtcd{}
	}

	keyManagementMode, ok := dataEncryption.GetKeyManagementMode()
	if !ok || keyManagementMode == csKeyManagementModePlatformManaged {
		// platform managed
		return clusterUpdateDispatchConfigEtcd{}
	}

	customerManaged := dataEncryption.CustomerManaged()

	encryptionType := customerManaged.EncryptionType()
	if encryptionType == "" && encryptionType != csCustomerManagedEncryptionTypeKms {
		// No KMS encryption type
		return clusterUpdateDispatchConfigEtcd{}
	}

	kms := customerManaged.Kms()

	activeKey := kms.ActiveKey()

	activeKeyKeyVersion := activeKey.KeyVersion()

	return clusterUpdateDispatchConfigEtcd{
		DataEncryption: clusterUpdateDispatchConfigEtcdDataEncryption{
			CustomerManaged: &clusterUpdateDispatchConfigEtcdDataEncryptionCustomerManaged{
				Kms: &clusterUpdateDispatchConfigEtcdDataEncryptionCustomerManagedKms{
					ActiveKey: clusterUpdateDispatchConfigEtcdDataEncryptionCustomerManagedKmsActiveKey{
						Version: activeKeyKeyVersion,
					},
				},
			},
		},
	}
}

// ClusterUpdateDispatchConfigContainerRegistryPullMIFromCS extracts the container registry
// pull managed identity resource ID from a Cluster Service cluster's Azure config.
// Returns empty string when unset.
func ClusterUpdateDispatchConfigContainerRegistryPullMIFromCS(in *arohcpv1alpha1.Azure) string {
	if in == nil {
		return ""
	}
	cr := in.ContainerRegistry()
	if cr == nil {
		return ""
	}
	creds := cr.Credentials()
	if creds == nil {
		return ""
	}
	mi := creds.ManagedIdentity()
	if mi == nil {
		return ""
	}
	return mi.ResourceID()
}

// clusterUpdateDispatchConfigHash returns a SHA-256 hex digest of the dispatch config
// projected from RP desired state. The digest is computed from canonical JSON (sorted object
// keys at every level), not from a raw json.Marshal of the struct.
func clusterUpdateDispatchConfigHash(cluster *api.HCPOpenShiftCluster, serviceProviderCluster *api.ServiceProviderCluster) (string, error) {
	return clusterUpdateDispatchConfigFromRP(cluster, serviceProviderCluster).hash()
}

// hash returns a SHA-256 hex digest of c's canonical JSON.
func (c *clusterUpdateDispatchConfig) hash() (string, error) {
	return hashUpdateDispatchConfig(c)
}

// canonicalJSON returns the deterministic JSON encoding of c used for hashing and comparison.
// Keys are sorted at every object level and the payload is indented with two spaces; see
// canonicalJSONForUpdateDispatchConfig.
func (c *clusterUpdateDispatchConfig) canonicalJSON() ([]byte, error) {
	return canonicalJSONForUpdateDispatchConfig(c)
}

// applyToCSBuilders maps the dispatch config onto Cluster Service cluster builders.
// baseProperties may contain existing CS properties. Experimental features are overlaid on
// baseProperties depending on how they evaluate. If they evaluate to enabled then the corresponding
// key is set to the value of the Experimental feature. If they evaluate to disabled then the corresponding key
// is deleted from the baseProperties map.
func (c *clusterUpdateDispatchConfig) applyToCSBuilders(clusterBuilder *arohcpv1alpha1.ClusterBuilder, clusterAPIBuilder *arohcpv1alpha1.ClusterAPIBuilder, azureBuilder *arohcpv1alpha1.AzureBuilder, etcdDataEncryptionCustomerManagedActiveKeyBuilder *arohcpv1alpha1.AzureKmsKeyBuilder, baseProperties map[string]string) error {
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

	var requiresAzureBuild = false
	// We support updating the Active KMS key for etcd data encryption in customer managed key encryption mode
	// only when we are in that mode
	if c.Etcd.DataEncryption.CustomerManaged != nil && c.Etcd.DataEncryption.CustomerManaged.Kms != nil {
		if etcdDataEncryptionCustomerManagedActiveKeyBuilder != nil {
			requiresAzureBuild = true
			etcdDataEncryptionCustomerManagedActiveKeyBuilder.KeyVersion(c.Etcd.DataEncryption.CustomerManaged.Kms.ActiveKey.Version)
		}
	}

	experimentalFeatures := c.ExperimentalFeatures
	if experimentalFeatures.ControlPlaneAvailability == api.SingleReplicaControlPlane {
		baseProperties[CSPropertySingleReplica] = CSPropertyEnabled
	} else {
		delete(baseProperties, CSPropertySingleReplica)
	}

	// We calculate the hosted cluster size override updatable configuration dynamically by checking
	// both the corresponding cluster's experimental feature property as well as the ServiceProviderCluster's
	// DesiredHostedClusterControlPlaneSize
	sizeOverride, toSet := ConvertHostedClusterSizeOverrideToCS(c.ExperimentalFeatures.ControlPlanePodSizing, c.ServiceProviderClusterDispatch.DesiredHostedClusterControlPlaneSize)
	if toSet {
		baseProperties[CSPropertySizeOverride] = sizeOverride
	} else {
		delete(baseProperties, CSPropertySizeOverride)
	}

	if experimentalFeatures.ControlPlaneOperatorImage != "" {
		baseProperties[CSPropertyCPOImageOverride] = experimentalFeatures.ControlPlaneOperatorImage
	} else {
		delete(baseProperties, CSPropertyCPOImageOverride)
	}
	clusterBuilder.Properties(baseProperties)

	// If you are changing a builder that is a child of Azure builder you need to add it here in case
	// the azureBuilder is nil
	if azureBuilder == nil && requiresAzureBuild {
		azureBuilder = arohcpv1alpha1.NewAzure()
		azureBuilder.EtcdEncryption(arohcpv1alpha1.NewAzureEtcdEncryption().
			DataEncryption(arohcpv1alpha1.NewAzureEtcdDataEncryption().
				CustomerManaged(arohcpv1alpha1.NewAzureEtcdDataEncryptionCustomerManaged().
					Kms(arohcpv1alpha1.NewAzureKmsEncryption().
						ActiveKey(etcdDataEncryptionCustomerManagedActiveKeyBuilder)))))
	}
	// Container registry can be set/updated/cleared independently of other azure fields.
	if c.ContainerRegistryPullManagedIdentityResourceID != "" {
		if azureBuilder == nil {
			azureBuilder = arohcpv1alpha1.NewAzure()
		}
		resourceID := c.ContainerRegistryPullManagedIdentityResourceID
		if resourceID == containerRegistryPullMIClearingSignal {
			resourceID = ""
		}
		azureBuilder.ContainerRegistry(arohcpv1alpha1.NewAzureContainerRegistry().
			Credentials(arohcpv1alpha1.NewAzureContainerRegistryCredentials().
				Type(arohcpv1alpha1.AzureContainerRegistryCredentialTypeManagedIdentity).
				ManagedIdentity(arohcpv1alpha1.NewAzureUserAssignedManagedIdentity().
					ResourceID(resourceID))))
	}
	if azureBuilder != nil {
		clusterBuilder.Azure(azureBuilder)
	}

	// The autoscaler is nested on the cluster builder.
	autoscalerBuilder, err := convertRpAutoscalarToCSBuilder(ptr.To(clusterUpdateDispatchConfigAutoscalingToRP(c.Autoscaling)))
	if err != nil {
		return err
	}
	clusterBuilder.Autoscaler(autoscalerBuilder)

	return nil
}

// clusterUpdateDispatchConfigImageDigestMirrorsToRP converts dispatch image mirrors into
// api.ImageDigestMirror values for shared CS conversion helpers.
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

// clusterUpdateDispatchConfigAutoscalingToRP converts dispatch autoscaling fields into
// api.ClusterAutoscalingProfile for shared CS conversion helpers.
func clusterUpdateDispatchConfigAutoscalingToRP(profile clusterUpdateDispatchConfigAutoscaling) api.ClusterAutoscalingProfile {
	return api.ClusterAutoscalingProfile{
		MaxNodesTotal:               profile.MaxNodesTotal,
		MaxPodGracePeriodSeconds:    profile.MaxPodGracePeriodSeconds,
		MaxNodeProvisionTimeSeconds: profile.MaxNodeProvisionTimeSeconds,
		PodPriorityThreshold:        profile.PodPriorityThreshold,
	}
}
