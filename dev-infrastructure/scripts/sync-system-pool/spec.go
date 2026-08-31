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
	"fmt"
	"sort"
	"strings"

	armcs "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"
)

// The management-cluster `system` AgentPool is fully described by
// dev-infrastructure/modules/aks-cluster-base.bicep's agentPoolProfiles[0]
// entry. Most of that spec is a fixed template constant (osSKU, mode,
// taints, labels, subnet names, ...); only a handful of fields are sourced
// from config.yaml's mgmt.aks.systemAgentPool block (vmSize, min/maxCount,
// osDiskSizeGB, zones, zoneRedundantMode). desiredSystemPool reproduces the
// ENTIRE bicep-computed spec (config-driven fields + template constants),
// so drift in either source is caught — not just drift in the yaml-sourced
// subset. Keep this in sync with aks-cluster-base.bicep / mgmt-cluster.bicep
// if either template changes the system pool shape.
const (
	// nodeSubnetName mirrors dev-infrastructure/templates/mgmt-cluster.bicep's
	// `nodeSubnetName` local var. Not config-driven.
	nodeSubnetName = "ClusterSubnet-001"
	// podSubnetName mirrors aks-cluster-base.bicep's `aksPodSubnet` resource
	// name. Not config-driven.
	podSubnetName = "PodSubnet-001"

	// swiftMultiTenancyTagKey/Value mirror aks-cluster-base.bicep's
	// swiftNodepoolTags var. mgmt-cluster.bicep always passes
	// enableSwiftV2Nodepools=true, so this tag is unconditional for mgmt.
	swiftMultiTenancyTagKey   = "aks-nic-enable-multi-tenancy"
	swiftMultiTenancyTagValue = "true"

	// nodeLabelRoleKey/Value and nodeTaint mirror the hardcoded
	// nodeLabels/nodeTaints on the system agentPoolProfile entry.
	nodeLabelRoleKey   = "aro-hcp.azure.com/role"
	nodeLabelRoleValue = "system"
	nodeTaint          = "CriticalAddonsOnly=true:NoSchedule"

	// systemPoolUpgradeMaxSurge mirrors the system agentPoolProfile's
	// hardcoded upgradeSettings.maxSurge (distinct from the cluster-level
	// upgradeSettingsMaxSurge/maxUnavailable params, which apply to
	// user/infra pools instead).
	systemPoolUpgradeMaxSurge = "10%"

	systemPoolMaxPods = 100

	tempPoolTagKey = "purpose"
)

// systemPoolConfig holds the resolved config.yaml values (plus identifying
// env vars) needed to reconstruct the desired system AgentPool spec. All
// fields are populated from environment variables set by the mgmt-pipeline
// Shell step (see loadConfig), so this struct can be built and exercised in
// tests without any Azure/network calls.
type systemPoolConfig struct {
	subscriptionID string
	resourceGroup  string
	clusterName    string
	vnetName       string

	poolName          string
	vmSize            string
	minCount          int32
	maxCount          int32
	osDiskSizeGB      int32
	zones             []string
	zoneRedundantMode string

	tempPoolName string

	dryRun bool
}

// nodeSubnetID / podSubnetID reproduce the ARM resource IDs that
// aks-cluster-base.bicep computes for the fixed subnet names above.
func (c *systemPoolConfig) nodeSubnetID() string {
	return subnetResourceID(c.subscriptionID, c.resourceGroup, c.vnetName, nodeSubnetName)
}

func (c *systemPoolConfig) podSubnetID() string {
	return subnetResourceID(c.subscriptionID, c.resourceGroup, c.vnetName, podSubnetName)
}

func subnetResourceID(subscriptionID, resourceGroup, vnetName, subnetName string) string {
	return fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/virtualNetworks/%s/subnets/%s",
		subscriptionID, resourceGroup, vnetName, subnetName,
	)
}

// desiredAvailabilityZones reproduces aks-cluster-base.bicep's
// systemPoolZonesArray ternary:
//
//	systemZoneRedundantMode == 'Enabled'
//	  || (systemZoneRedundantMode == 'Auto' && length(systemAgentPoolZones) > 0)
//	  ? systemAgentPoolZones : null
func desiredAvailabilityZones(mode string, zones []string) []*string {
	if mode == "Enabled" || (mode == "Auto" && len(zones) > 0) {
		return toPtrSlice(zones)
	}
	return nil
}

// desiredAgentPool builds the full AgentPool spec that mgmt-cluster's ARM
// deployment would put for the system pool, given the resolved config and
// the live cluster's current Kubernetes version (orchestratorVersion is
// pinned to the control-plane version, matching the bicep template's
// implicit behavior of never requesting a version different from the
// cluster).
func desiredAgentPool(cfg *systemPoolConfig, poolName string, cpVersion string) *armcs.AgentPool {
	mode := armcs.AgentPoolModeSystem
	osType := armcs.OSTypeLinux
	osSKU := armcs.OSSKUAzureLinux
	osDiskType := armcs.OSDiskTypeEphemeral
	kubeletDiskType := armcs.KubeletDiskTypeOS
	poolType := armcs.AgentPoolTypeVirtualMachineScaleSets
	autoScale := true
	falseVal := false
	vmSize := cfg.vmSize
	osDiskSizeGB := cfg.osDiskSizeGB
	minCount := cfg.minCount
	maxCount := cfg.maxCount
	maxSurge := systemPoolUpgradeMaxSurge
	maxPods := int32(systemPoolMaxPods)
	nodeSubnetID := cfg.nodeSubnetID()
	podSubnetID := cfg.podSubnetID()
	orchestratorVersion := cpVersion

	return &armcs.AgentPool{
		Name: &poolName,
		Properties: &armcs.ManagedClusterAgentPoolProfileProperties{
			OSType:                 &osType,
			OSSKU:                  &osSKU,
			Mode:                   &mode,
			EnableAutoScaling:      &autoScale,
			EnableEncryptionAtHost: boolPtr(true),
			EnableFIPS:             boolPtr(true),
			EnableNodePublicIP:     &falseVal,
			KubeletDiskType:        &kubeletDiskType,
			OSDiskType:             &osDiskType,
			OSDiskSizeGB:           &osDiskSizeGB,
			MinCount:               &minCount,
			MaxCount:               &maxCount,
			VMSize:                 &vmSize,
			Type:                   &poolType,
			UpgradeSettings: &armcs.AgentPoolUpgradeSettings{
				MaxSurge: &maxSurge,
			},
			VnetSubnetID:        &nodeSubnetID,
			PodSubnetID:         &podSubnetID,
			MaxPods:             &maxPods,
			AvailabilityZones:   desiredAvailabilityZones(cfg.zoneRedundantMode, cfg.zones),
			OrchestratorVersion: &orchestratorVersion,
			SecurityProfile: &armcs.AgentPoolSecurityProfile{
				EnableSecureBoot: &falseVal,
				EnableVTPM:       &falseVal,
			},
			NodeLabels: map[string]*string{
				nodeLabelRoleKey: strPtr(nodeLabelRoleValue),
			},
			NodeTaints: []*string{strPtr(nodeTaint)},
			Tags: map[string]*string{
				swiftMultiTenancyTagKey: strPtr(swiftMultiTenancyTagValue),
			},
		},
	}
}

// desiredTempAgentPool derives the throwaway pool spec from the desired
// system spec: same compute/network/label/taint shape (so it validates the
// exact same config we're about to roll out to `system`), but fixed at a
// single non-autoscaled node so it doesn't hold unnecessary capacity while
// it's alive.
func desiredTempAgentPool(cfg *systemPoolConfig, cpVersion string) *armcs.AgentPool {
	pool := desiredAgentPool(cfg, cfg.tempPoolName, cpVersion)
	count := int32(1)
	pool.Properties.EnableAutoScaling = boolPtr(false)
	pool.Properties.Count = &count
	pool.Properties.MinCount = nil
	pool.Properties.MaxCount = nil
	if pool.Properties.Tags == nil {
		pool.Properties.Tags = map[string]*string{}
	}
	pool.Properties.Tags[tempPoolTagKey] = strPtr("temp-system-sync-system-pool")
	return pool
}

// diffAgentPool compares the properties that matter for the system pool
// (ignoring read-only/status fields such as provisioningState,
// currentOrchestratorVersion, nodeImageVersion, powerState, creationData,
// ETag, and any aks-managed-* tag that AKS injects on its own) and returns
// a human-readable reason per mismatch. An empty result means the two
// specs are equivalent.
//
// The `name` field is intentionally NOT compared here: callers that need to
// assert "identical except for name" (the temp-pool validation step)
// achieve that by building both sides with the same desired-spec function
// and only varying the name/scaling fields, then calling diffAgentPool on
// the properties they expect to be identical.
func diffAgentPool(desired, live *armcs.AgentPool) []string {
	var diffs []string
	add := func(field string, want, got any) {
		diffs = append(diffs, fmt.Sprintf("%s: want %v, got %v", field, want, got))
	}

	if desired == nil || desired.Properties == nil {
		return []string{"desired spec is nil"}
	}
	if live == nil || live.Properties == nil {
		return []string{"live spec is nil"}
	}
	d, l := desired.Properties, live.Properties

	cmpStr("vmSize", strDeref(d.VMSize), strDeref(l.VMSize), add)
	cmpInt32("osDiskSizeGB", int32Deref(d.OSDiskSizeGB), int32Deref(l.OSDiskSizeGB), add)
	cmpBool("enableAutoScaling", boolDeref(d.EnableAutoScaling), boolDeref(l.EnableAutoScaling), add)
	if boolDeref(d.EnableAutoScaling) {
		cmpInt32("minCount", int32Deref(d.MinCount), int32Deref(l.MinCount), add)
		cmpInt32("maxCount", int32Deref(d.MaxCount), int32Deref(l.MaxCount), add)
	} else {
		cmpInt32("count", int32Deref(d.Count), int32Deref(l.Count), add)
	}
	cmpStr("osType", string(strDerefOSType(d.OSType)), string(strDerefOSType(l.OSType)), add)
	cmpStr("osSKU", string(strDerefOSSKU(d.OSSKU)), string(strDerefOSSKU(l.OSSKU)), add)
	cmpStr("mode", string(strDerefMode(d.Mode)), string(strDerefMode(l.Mode)), add)
	cmpBool("enableEncryptionAtHost", boolDeref(d.EnableEncryptionAtHost), boolDeref(l.EnableEncryptionAtHost), add)
	cmpBool("enableFIPS", boolDeref(d.EnableFIPS), boolDeref(l.EnableFIPS), add)
	cmpBool("enableNodePublicIP", boolDeref(d.EnableNodePublicIP), boolDeref(l.EnableNodePublicIP), add)
	cmpStr("kubeletDiskType", string(strDerefKubeletDiskType(d.KubeletDiskType)), string(strDerefKubeletDiskType(l.KubeletDiskType)), add)
	cmpStr("osDiskType", string(strDerefOSDiskType(d.OSDiskType)), string(strDerefOSDiskType(l.OSDiskType)), add)
	cmpStr("type", string(strDerefPoolType(d.Type)), string(strDerefPoolType(l.Type)), add)
	cmpStr("upgradeSettings.maxSurge", upgradeMaxSurge(d), upgradeMaxSurge(l), add)
	cmpStr("vnetSubnetID", strDeref(d.VnetSubnetID), strDeref(l.VnetSubnetID), add)
	cmpStr("podSubnetID", strDeref(d.PodSubnetID), strDeref(l.PodSubnetID), add)
	cmpInt32("maxPods", int32Deref(d.MaxPods), int32Deref(l.MaxPods), add)
	cmpStrSlice("availabilityZones", ptrSliceToStrings(d.AvailabilityZones), ptrSliceToStrings(l.AvailabilityZones), add)
	cmpBool("securityProfile.enableSecureBoot", securityBool(d, func(p *armcs.AgentPoolSecurityProfile) *bool { return p.EnableSecureBoot }),
		securityBool(l, func(p *armcs.AgentPoolSecurityProfile) *bool { return p.EnableSecureBoot }), add)
	cmpBool("securityProfile.enableVTPM", securityBool(d, func(p *armcs.AgentPoolSecurityProfile) *bool { return p.EnableVTPM }),
		securityBool(l, func(p *armcs.AgentPoolSecurityProfile) *bool { return p.EnableVTPM }), add)
	cmpStrMap("nodeLabels", strPtrMapToStrings(d.NodeLabels), strPtrMapToStrings(l.NodeLabels), add)
	cmpStrSlice("nodeTaints", ptrSliceToStrings(d.NodeTaints), ptrSliceToStrings(l.NodeTaints), add)
	cmpStrMap("tags", stripAKSManagedTags(strPtrMapToStrings(d.Tags)), stripAKSManagedTags(strPtrMapToStrings(l.Tags)), add)

	return diffs
}

func upgradeMaxSurge(p *armcs.ManagedClusterAgentPoolProfileProperties) string {
	if p == nil || p.UpgradeSettings == nil {
		return ""
	}
	return strDeref(p.UpgradeSettings.MaxSurge)
}

func securityBool(p *armcs.ManagedClusterAgentPoolProfileProperties, get func(*armcs.AgentPoolSecurityProfile) *bool) bool {
	if p == nil || p.SecurityProfile == nil {
		return false
	}
	return boolDeref(get(p.SecurityProfile))
}

// stripAKSManagedTags drops any tag AKS itself injects (aks-managed-*
// prefix); those are never present in our desired spec and would otherwise
// show up as a spurious "extra tag on live" diff on every run.
func stripAKSManagedTags(tags map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range tags {
		if strings.HasPrefix(k, "aks-managed-") {
			continue
		}
		out[k] = v
	}
	return out
}

func cmpStr(field, want, got string, add func(string, any, any)) {
	if want != got {
		add(field, want, got)
	}
}

func cmpInt32(field string, want, got int32, add func(string, any, any)) {
	if want != got {
		add(field, want, got)
	}
}

func cmpBool(field string, want, got bool, add func(string, any, any)) {
	if want != got {
		add(field, want, got)
	}
}

func cmpStrSlice(field string, want, got []string, add func(string, any, any)) {
	w := append([]string{}, want...)
	g := append([]string{}, got...)
	sort.Strings(w)
	sort.Strings(g)
	if len(w) == 0 && len(g) == 0 {
		return
	}
	if strings.Join(w, ",") != strings.Join(g, ",") {
		add(field, w, g)
	}
}

func cmpStrMap(field string, want, got map[string]string, add func(string, any, any)) {
	if len(want) == 0 && len(got) == 0 {
		return
	}
	if !mapsEqual(want, got) {
		add(field, want, got)
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}
