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
	"strings"

	armcs "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"
)

// agentPoolFieldJSONKey maps every Go field on
// armcs.ManagedClusterAgentPoolProfileProperties to its ARM JSON key. The
// SDK type has no json struct tags (it uses a hand-generated MarshalJSON in
// models_serde.go instead), so this table is maintained by hand from that
// generated method. TestAgentPoolFieldRegistryComplete cross-checks it
// against the live struct via reflection, so a Go-field/JSON-key mismatch
// (e.g. after an SDK bump renames or adds a field) fails the build.
var agentPoolFieldJSONKey = map[string]string{
	"AvailabilityZones":          "availabilityZones",
	"CapacityReservationGroupID": "capacityReservationGroupID",
	"Count":                      "count",
	"CreationData":               "creationData",
	"CurrentOrchestratorVersion": "currentOrchestratorVersion",
	"ETag":                       "eTag",
	"EnableAutoScaling":          "enableAutoScaling",
	"EnableEncryptionAtHost":     "enableEncryptionAtHost",
	"EnableFIPS":                 "enableFIPS",
	"EnableNodePublicIP":         "enableNodePublicIP",
	"EnableUltraSSD":             "enableUltraSSD",
	"GpuInstanceProfile":         "gpuInstanceProfile",
	"GpuProfile":                 "gpuProfile",
	"HostGroupID":                "hostGroupID",
	"KubeletConfig":              "kubeletConfig",
	"KubeletDiskType":            "kubeletDiskType",
	"LinuxOSConfig":              "linuxOSConfig",
	"MaxCount":                   "maxCount",
	"MaxPods":                    "maxPods",
	"MessageOfTheDay":            "messageOfTheDay",
	"MinCount":                   "minCount",
	"Mode":                       "mode",
	"NetworkProfile":             "networkProfile",
	"NodeImageVersion":           "nodeImageVersion",
	"NodeLabels":                 "nodeLabels",
	"NodePublicIPPrefixID":       "nodePublicIPPrefixID",
	"NodeTaints":                 "nodeTaints",
	"OSDiskSizeGB":               "osDiskSizeGB",
	"OSDiskType":                 "osDiskType",
	"OSSKU":                      "osSKU",
	"OSType":                     "osType",
	"OrchestratorVersion":        "orchestratorVersion",
	"PodSubnetID":                "podSubnetID",
	"PowerState":                 "powerState",
	"ProvisioningState":          "provisioningState",
	"ProximityPlacementGroupID":  "proximityPlacementGroupID",
	"ScaleDownMode":              "scaleDownMode",
	"ScaleSetEvictionPolicy":     "scaleSetEvictionPolicy",
	"ScaleSetPriority":           "scaleSetPriority",
	"SecurityProfile":            "securityProfile",
	"SpotMaxPrice":               "spotMaxPrice",
	"Tags":                       "tags",
	"Type":                       "type",
	"UpgradeSettings":            "upgradeSettings",
	"VMSize":                     "vmSize",
	"VnetSubnetID":               "vnetSubnetID",
	"WindowsProfile":             "windowsProfile",
	"WorkloadRuntime":            "workloadRuntime",
}

// agentPoolFieldReasons categorizes every field on
// armcs.ManagedClusterAgentPoolProfileProperties, so that a new field added
// by a future Azure SDK bump is caught at review/CI time instead of being
// silently dropped by desiredAgentPool/diffAgentPool. Every field must be
// tagged with one of three prefixes:
//
//   - "compared: ..."  the field is explicitly set in desiredAgentPool and
//     explicitly diffed in diffAgentPool (see the referenced helper).
//   - "ignored: ..."   the field must NEVER be diffed (read-only/status
//     field, or a deliberate exclusion), with the reason spelled out.
//   - "generic: ..."   the field is not explicitly handled anywhere, but is
//     still safe to structurally diff: genericFieldDiff compares it
//     automatically, so an unexpected non-nil value appearing on the live
//     pool still surfaces as a diff requiring human review instead of being
//     silently dropped when the pool is recreated.
//
// TestAgentPoolFieldRegistryComplete enforces 1:1 coverage against the live
// struct (in both directions: every struct field is registered, and every
// registry entry still exists on the struct).
var agentPoolFieldReasons = map[string]string{
	"AvailabilityZones":          "compared: desiredAvailabilityZones + cmpStrSlice",
	"CapacityReservationGroupID": "generic: not used by the system pool today; genericFieldDiff catches an unexpected live value",
	"Count":                      "compared: cmpInt32 (only when EnableAutoScaling=false)",
	"CreationData":               "ignored: read-only, only meaningful at pool creation time",
	"CurrentOrchestratorVersion": "ignored: read-only ARM status field",
	"ETag":                       "ignored: read-only ARM concurrency token",
	"EnableAutoScaling":          "compared: cmpBool",
	"EnableEncryptionAtHost":     "compared: cmpBool",
	"EnableFIPS":                 "compared: cmpBool",
	"EnableNodePublicIP":         "compared: cmpBool",
	"EnableUltraSSD":             "generic: not used by the system pool today; genericFieldDiff catches an unexpected live value",
	"GpuInstanceProfile":         "generic: N/A (system pool is CPU-only); genericFieldDiff catches an unexpected live value",
	"GpuProfile":                 "generic: N/A (system pool is CPU-only); genericFieldDiff catches an unexpected live value",
	"HostGroupID":                "generic: not used by the system pool today; genericFieldDiff catches an unexpected live value",
	"KubeletConfig":              "generic: not config-driven today; genericFieldDiff catches an unexpected live value",
	"KubeletDiskType":            "compared: cmpStr",
	"LinuxOSConfig":              "generic: not config-driven today; genericFieldDiff catches an unexpected live value",
	"MaxCount":                   "compared: cmpInt32 (only when EnableAutoScaling=true)",
	"MaxPods":                    "compared: cmpInt32",
	"MessageOfTheDay":            "generic: not config-driven today; genericFieldDiff catches an unexpected live value",
	"MinCount":                   "compared: cmpInt32 (only when EnableAutoScaling=true)",
	"Mode":                       "compared: cmpStr",
	"NetworkProfile":             "generic: not config-driven today; genericFieldDiff catches an unexpected live value",
	"NodeImageVersion":           "ignored: read-only ARM status field",
	"NodeLabels":                 "compared: cmpStrMap",
	"NodePublicIPPrefixID":       "generic: not used by the system pool today; genericFieldDiff catches an unexpected live value",
	"NodeTaints":                 "compared: cmpStrSlice",
	"OSDiskSizeGB":               "compared: cmpInt32",
	"OSDiskType":                 "compared: cmpStr",
	"OSSKU":                      "compared: cmpStr",
	"OSType":                     "compared: cmpStr",
	"OrchestratorVersion":        "ignored: tracks the live control-plane version through normal AKS node image/k8s upgrades; not system pool config drift",
	"PodSubnetID":                "compared: cmpStr",
	"PowerState":                 "ignored: read-only ARM status field",
	"ProvisioningState":          "ignored: read-only ARM status field",
	"ProximityPlacementGroupID":  "generic: not used by the system pool today; genericFieldDiff catches an unexpected live value",
	"ScaleDownMode":              "generic: not config-driven today; genericFieldDiff catches an unexpected live value",
	"ScaleSetEvictionPolicy":     "generic: N/A (system pool is not a spot pool); genericFieldDiff catches an unexpected live value",
	"ScaleSetPriority":           "generic: N/A (system pool is not a spot pool); genericFieldDiff catches an unexpected live value",
	"SecurityProfile":            "compared: cmpBool x2 (enableSecureBoot, enableVTPM)",
	"SpotMaxPrice":               "generic: N/A (system pool is not a spot pool); genericFieldDiff catches an unexpected live value",
	"Tags":                       "compared: cmpStrMap (aks-managed-* tags stripped first)",
	"Type":                       "compared: cmpStr",
	"UpgradeSettings":            "compared: cmpStr (maxSurge)",
	"VMSize":                     "compared: cmpStr",
	"VnetSubnetID":               "compared: cmpStr",
	"WindowsProfile":             "generic: N/A (system pool is Linux); genericFieldDiff catches an unexpected live value",
	"WorkloadRuntime":            "generic: not config-driven today; genericFieldDiff catches an unexpected live value",
}

// jsonKeyForField returns the ARM JSON key for a Go field name on
// armcs.ManagedClusterAgentPoolProfileProperties, falling back to the field
// name itself if it's (unexpectedly) missing from agentPoolFieldJSONKey —
// this only matters for brand new, not-yet-triaged fields, and keeps
// genericFieldDiff fail-open (compare it) rather than silently skipping it.
func jsonKeyForField(goName string) string {
	if key, ok := agentPoolFieldJSONKey[goName]; ok {
		return key
	}
	return goName
}

// genericFieldDiff is a structural safety net alongside diffAgentPool's
// explicit field-by-field comparisons above. It JSON round-trips both
// Properties structs (via the SDK's own MarshalJSON) and deep-compares
// every field that is NOT already explicitly "compared" or "ignored" per
// agentPoolFieldReasons — including any field not yet triaged at all, which
// fails open to being compared rather than silently skipped. This means a
// field the Azure SDK adds tomorrow, one this tool has never heard of,
// still surfaces as a diff instead of being dropped when the pool is
// recreated.
func genericFieldDiff(desired, live *armcs.ManagedClusterAgentPoolProfileProperties) []string {
	dMap, err := toJSONMap(desired)
	if err != nil {
		return []string{fmt.Sprintf("genericFieldDiff: marshal desired properties: %v", err)}
	}
	lMap, err := toJSONMap(live)
	if err != nil {
		return []string{fmt.Sprintf("genericFieldDiff: marshal live properties: %v", err)}
	}

	t := reflect.TypeOf(armcs.ManagedClusterAgentPoolProfileProperties{})
	var diffs []string
	for i := 0; i < t.NumField(); i++ {
		goName := t.Field(i).Name
		reason := agentPoolFieldReasons[goName] // "" (unregistered) is treated the same as "generic:"
		if strings.HasPrefix(reason, "compared:") || strings.HasPrefix(reason, "ignored:") {
			continue
		}
		key := jsonKeyForField(goName)
		dv, dok := dMap[key]
		lv, lok := lMap[key]
		if !dok && !lok {
			continue
		}
		if !reflect.DeepEqual(dv, lv) {
			diffs = append(diffs, fmt.Sprintf("%s: want %v, got %v (unhandled field - update spec.go/agentPoolFieldReasons if intentional)", key, dv, lv))
		}
	}
	return diffs
}

func toJSONMap(v any) (map[string]any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}
