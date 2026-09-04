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

package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"

	"github.com/Azure/ARO-HCP/fleet/pkg/azure/agentpoolspec"
	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
)

// Cluster service network, mirroring dev-infrastructure/modules/aks-cluster-base.bicep.
const (
	serviceCIDR  = "10.130.0.0/16"
	dnsServiceIP = "10.130.0.10"
)

// buildClusterSpec defines the configuration once for both creation and updates.
// A new cluster requires a bootstrap pool; an existing cluster retains unowned
// fields and tags, but never submits pools or version. The observation is not
// mutated. Changed paths are sorted and contain no configuration values.
func buildClusterSpec(o *validatedOptions, existing *armcontainerservice.ManagedCluster, bootstrap *compute.Pool) (armcontainerservice.ManagedCluster, []string, error) {
	if existing != nil && existing.Properties == nil {
		return armcontainerservice.ManagedCluster{}, nil, fmt.Errorf("cluster properties are missing")
	}
	var advancedNetworking *armcontainerservice.AdvancedNetworking
	if armcontainerservice.NetworkDataplane(o.networkDataplane) == armcontainerservice.NetworkDataplaneCilium {
		advancedNetworking = &armcontainerservice.AdvancedNetworking{
			Enabled:       ptr.To(true),
			Observability: &armcontainerservice.AdvancedNetworkingObservability{Enabled: ptr.To(true)},
		}
	}

	desired := armcontainerservice.ManagedCluster{
		SKU: &armcontainerservice.ManagedClusterSKU{
			Name: ptr.To(armcontainerservice.ManagedClusterSKUNameBase),
			Tier: ptr.To(armcontainerservice.ManagedClusterSKUTierStandard),
		},
		// Tags are owned by tags.go, not cluster configuration reconciliation.
		Identity: &armcontainerservice.ManagedClusterIdentity{
			Type: ptr.To(armcontainerservice.ResourceIdentityTypeUserAssigned),
			UserAssignedIdentities: map[string]*armcontainerservice.ManagedServiceIdentityUserAssignedIdentitiesValue{
				o.managedIdentityID: {},
			},
		},
		Properties: &armcontainerservice.ManagedClusterProperties{
			AADProfile: &armcontainerservice.ManagedClusterAADProfile{
				Managed:         ptr.To(true),
				EnableAzureRBAC: ptr.To(true),
			},
			AddonProfiles: map[string]*armcontainerservice.ManagedClusterAddonProfile{
				"azureKeyvaultSecretsProvider": {
					Enabled: ptr.To(true),
					Config: map[string]*string{
						"enableSecretRotation": ptr.To("true"),
						"rotationPollInterval": ptr.To("1h"),
					},
				},
				"omsagent": {
					Enabled: ptr.To(false),
				},
			},
			AutoScalerProfile: &armcontainerservice.ManagedClusterPropertiesAutoScalerProfile{
				BalanceSimilarNodeGroups:          ptr.To("true"),
				DaemonsetEvictionForOccupiedNodes: ptr.To(true),
				ScanInterval:                      ptr.To("10s"),
				ScaleDownDelayAfterAdd:            ptr.To("30m"),
				ScaleDownDelayAfterDelete:         ptr.To("60s"),
				ScaleDownDelayAfterFailure:        ptr.To("3m"),
				ScaleDownUnneededTime:             ptr.To("30m"),
				ScaleDownUnreadyTime:              ptr.To("20m"),
				ScaleDownUtilizationThreshold:     ptr.To("0.3"),
				SkipNodesWithLocalStorage:         ptr.To("false"),
				MaxGracefulTerminationSec:         ptr.To("600"),
				MaxNodeProvisionTime:              ptr.To("15m"),
			},
			AutoUpgradeProfile: &armcontainerservice.ManagedClusterAutoUpgradeProfile{
				NodeOSUpgradeChannel: ptr.To(armcontainerservice.NodeOSUpgradeChannelNodeImage),
				UpgradeChannel:       ptr.To(armcontainerservice.UpgradeChannelPatch),
			},
			AzureMonitorProfile: &armcontainerservice.ManagedClusterAzureMonitorProfile{
				Metrics: &armcontainerservice.ManagedClusterAzureMonitorProfileMetrics{
					Enabled: ptr.To(true),
					KubeStateMetrics: &armcontainerservice.ManagedClusterAzureMonitorProfileKubeStateMetrics{
						MetricLabelsAllowlist:      ptr.To(o.metricLabelsAllowlist),
						MetricAnnotationsAllowList: ptr.To(o.metricAnnotationsAllowlist),
					},
				},
			},
			DisableLocalAccounts: ptr.To(true),
			EnableRBAC:           ptr.To(true),
			MetricsProfile: &armcontainerservice.ManagedClusterMetricsProfile{
				CostAnalysis: &armcontainerservice.ManagedClusterCostAnalysis{
					Enabled: ptr.To(false),
				},
			},
			NetworkProfile: &armcontainerservice.NetworkProfile{
				AdvancedNetworking: advancedNetworking,
				IPFamilies:         []*armcontainerservice.IPFamily{ptr.To(armcontainerservice.IPFamilyIPv4)},
				LoadBalancerSKU:    ptr.To(armcontainerservice.LoadBalancerSKUStandard),
				LoadBalancerProfile: &armcontainerservice.ManagedClusterLoadBalancerProfile{
					OutboundIPs: &armcontainerservice.ManagedClusterLoadBalancerProfileOutboundIPs{
						PublicIPs: []*armcontainerservice.ResourceReference{
							{ID: ptr.To(o.outboundIPResourceID)},
						},
					},
				},
				NetworkDataplane: ptr.To(armcontainerservice.NetworkDataplane(o.networkDataplane)),
				NetworkPolicy:    ptr.To(armcontainerservice.NetworkPolicy(o.networkPolicy)),
				NetworkPlugin:    ptr.To(armcontainerservice.NetworkPluginAzure),
				ServiceCidr:      ptr.To(serviceCIDR),
				ServiceCidrs:     []*string{ptr.To(serviceCIDR)},
				DNSServiceIP:     ptr.To(dnsServiceIP),
			},
			OidcIssuerProfile: &armcontainerservice.ManagedClusterOIDCIssuerProfile{
				Enabled: ptr.To(true),
			},
			SecurityProfile: &armcontainerservice.ManagedClusterSecurityProfile{
				AzureKeyVaultKms: &armcontainerservice.AzureKeyVaultKms{
					Enabled:               ptr.To(true),
					KeyID:                 ptr.To(o.etcdKMSKeyURI),
					KeyVaultNetworkAccess: ptr.To(armcontainerservice.KeyVaultNetworkAccessTypesPublic),
				},
				ImageCleaner: &armcontainerservice.ManagedClusterSecurityProfileImageCleaner{
					Enabled:       ptr.To(true),
					IntervalHours: ptr.To[int32](24),
				},
				WorkloadIdentity: &armcontainerservice.ManagedClusterSecurityProfileWorkloadIdentity{
					Enabled: ptr.To(true),
				},
			},
			ServicePrincipalProfile: &armcontainerservice.ManagedClusterServicePrincipalProfile{
				ClientID: ptr.To("msi"),
			},
			StorageProfile: &armcontainerservice.ManagedClusterStorageProfile{
				DiskCSIDriver:      &armcontainerservice.ManagedClusterStorageProfileDiskCSIDriver{Enabled: ptr.To(true)},
				FileCSIDriver:      &armcontainerservice.ManagedClusterStorageProfileFileCSIDriver{Enabled: ptr.To(true)},
				SnapshotController: &armcontainerservice.ManagedClusterStorageProfileSnapshotController{Enabled: ptr.To(true)},
			},
			SupportPlan: ptr.To(armcontainerservice.KubernetesSupportPlanKubernetesOfficial),
		},
	}

	if existing == nil {
		if bootstrap == nil {
			return armcontainerservice.ManagedCluster{}, nil, fmt.Errorf("cluster creation requires a system pool")
		}
		desired.Location = ptr.To(o.region)
		desired.Properties.DNSPrefix = ptr.To(o.clusterName)
		desired.Properties.NodeResourceGroup = ptr.To(o.resourceGroup + "-aks1")
		desired.Tags = initialClusterTags(o)
		desired.Properties.KubernetesVersion = ptr.To(o.kubernetesVersion)
		networkConfig := compute.NetworkConfig{VnetSubnetID: o.nodeSubnetID, PodSubnetID: o.podSubnetID}
		desired.Properties.AgentPoolProfiles = []*armcontainerservice.ManagedClusterAgentPoolProfile{
			toClusterAgentPoolProfile(bootstrap.Name, agentpoolspec.Build(*bootstrap, networkConfig)),
		}
		return desired, nil, nil
	}

	// AKS may omit empty allowlists (or the entire kubeStateMetrics object).
	// Omission and empty are equivalent, but nonempty live values must still
	// be cleared when configuration switches back to an empty allowlist.
	var liveMetrics *armcontainerservice.ManagedClusterAzureMonitorProfileKubeStateMetrics
	if monitor := existing.Properties.AzureMonitorProfile; monitor != nil && monitor.Metrics != nil {
		liveMetrics = monitor.Metrics.KubeStateMetrics
	}
	metrics := desired.Properties.AzureMonitorProfile.Metrics.KubeStateMetrics
	if o.metricLabelsAllowlist == "" && (liveMetrics == nil || liveMetrics.MetricLabelsAllowlist == nil) {
		metrics.MetricLabelsAllowlist = nil
	}
	if o.metricAnnotationsAllowlist == "" && (liveMetrics == nil || liveMetrics.MetricAnnotationsAllowList == nil) {
		metrics.MetricAnnotationsAllowList = nil
	}
	if metrics.MetricLabelsAllowlist == nil && metrics.MetricAnnotationsAllowList == nil {
		desired.Properties.AzureMonitorProfile.Metrics.KubeStateMetrics = nil
	}

	live := *existing
	properties := *existing.Properties
	live.Properties = &properties
	properties.AgentPoolProfiles = nil
	properties.KubernetesVersion = nil

	// Merge the wire representation so explicit false/empty values are owned,
	// while nested server defaults and fields owned by other actors survive.
	currentFields, err := clusterConfigurationFields(live)
	if err != nil {
		return armcontainerservice.ManagedCluster{}, nil, err
	}
	desiredFields, err := clusterConfigurationFields(desired)
	if err != nil {
		return armcontainerservice.ManagedCluster{}, nil, err
	}
	changed := mergeClusterConfiguration(currentFields, desiredFields, "")
	if len(changed) == 0 {
		return live, nil, nil
	}
	data, err := json.Marshal(currentFields)
	if err != nil {
		return armcontainerservice.ManagedCluster{}, nil, fmt.Errorf("encoding cluster configuration: %w", err)
	}
	var update armcontainerservice.ManagedCluster
	if err := json.Unmarshal(data, &update); err != nil {
		return armcontainerservice.ManagedCluster{}, nil, fmt.Errorf("decoding cluster configuration: %w", err)
	}
	slices.Sort(changed)
	return update, changed, nil
}

// toClusterAgentPoolProfile adapts pool properties built by agentpoolspec.Build
// into the shape needed for ManagedCluster.Properties.AgentPoolProfiles. The SDK
// generates two nearly identical types — ManagedClusterAgentPoolProfileProperties
// (for the AgentPools API) and ManagedClusterAgentPoolProfile (for inline cluster
// pools) — so fields must be copied manually. When agentpoolspec.Build starts
// setting a new field, this function must be updated in lockstep.
func toClusterAgentPoolProfile(name string, props *armcontainerservice.ManagedClusterAgentPoolProfileProperties) *armcontainerservice.ManagedClusterAgentPoolProfile {
	return &armcontainerservice.ManagedClusterAgentPoolProfile{
		Name:                   ptr.To(name),
		VMSize:                 props.VMSize,
		AvailabilityZones:      props.AvailabilityZones,
		OSDiskSizeGB:           props.OSDiskSizeGB,
		OSDiskType:             props.OSDiskType,
		EnableAutoScaling:      props.EnableAutoScaling,
		MinCount:               props.MinCount,
		MaxCount:               props.MaxCount,
		Mode:                   props.Mode,
		Type:                   props.Type,
		OSSKU:                  props.OSSKU,
		OSType:                 props.OSType,
		KubeletDiskType:        props.KubeletDiskType,
		EnableEncryptionAtHost: props.EnableEncryptionAtHost,
		EnableFIPS:             props.EnableFIPS,
		EnableNodePublicIP:     props.EnableNodePublicIP,
		MaxPods:                props.MaxPods,
		SecurityProfile:        props.SecurityProfile,
		UpgradeSettings:        props.UpgradeSettings,
		NodeLabels:             props.NodeLabels,
		NodeTaints:             props.NodeTaints,
		Tags:                   props.Tags,
		VnetSubnetID:           props.VnetSubnetID,
		PodSubnetID:            props.PodSubnetID,
	}
}

func clusterConfigurationFields(cluster armcontainerservice.ManagedCluster) (map[string]any, error) {
	data, err := json.Marshal(cluster)
	if err != nil {
		return nil, fmt.Errorf("encoding cluster configuration: %w", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, fmt.Errorf("decoding cluster configuration: %w", err)
	}
	return fields, nil
}

// Objects are merged field-by-field; configured lists are authoritative. Absent
// desired fields are unowned, not requests to clear server defaults. Return only
// changed paths, never values, so logs do not disclose configuration contents.
func mergeClusterConfiguration(current, desired map[string]any, prefix string) []string {
	var changed []string
	for key, value := range desired {
		path := prefix + key
		actual := current[key]
		if object, ok := value.(map[string]any); ok {
			if currentObject, ok := actual.(map[string]any); ok {
				changed = append(changed, mergeClusterConfiguration(currentObject, object, path+".")...)
				continue
			}
		}
		if !reflect.DeepEqual(actual, value) {
			current[key] = value
			changed = append(changed, path)
		}
	}
	return changed
}
