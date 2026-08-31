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
	"testing"

	armcs "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"
)

func testConfig() *systemPoolConfig {
	return &systemPoolConfig{
		subscriptionID:    "00000000-0000-0000-0000-000000000000",
		resourceGroup:     "mgmt-int-1",
		clusterName:       "int-uksouth-mgmt-1",
		vnetName:          "aks-net",
		poolName:          "system",
		tempPoolName:      "systmp",
		vmSize:            "Standard_E8s_v3",
		minCount:          1,
		maxCount:          4,
		osDiskSizeGB:      32,
		zones:             nil,
		zoneRedundantMode: "Auto",
	}
}

// roundTrip mimics what an ARM Get response looks like: a JSON round-trip
// of what we PUT, since that's exactly what the real diffAgentPool calls
// compare against.
func roundTrip(t *testing.T, p *armcs.AgentPool) *armcs.AgentPool {
	t.Helper()
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out armcs.AgentPool
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return &out
}

func TestDesiredAgentPool_MatchesItselfAfterRoundTrip(t *testing.T) {
	cfg := testConfig()
	desired := desiredAgentPool(cfg, cfg.poolName, "1.35.2")
	live := roundTrip(t, desired)

	if diffs := diffAgentPool(desired, live); len(diffs) != 0 {
		t.Fatalf("expected no diffs, got: %v", diffs)
	}
}

func TestDesiredAgentPool_DetectsVMSizeDrift(t *testing.T) {
	cfg := testConfig()
	desired := desiredAgentPool(cfg, cfg.poolName, "1.35.2")
	live := roundTrip(t, desired)
	live.Properties.VMSize = strPtr("Standard_D2s_v3")

	diffs := diffAgentPool(desired, live)
	if len(diffs) == 0 {
		t.Fatal("expected a vmSize diff, got none")
	}
	found := false
	for _, d := range diffs {
		if containsAll(d, "vmSize") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected vmSize diff, got: %v", diffs)
	}
}

func TestDesiredAgentPool_DetectsOSDiskSizeDrift(t *testing.T) {
	cfg := testConfig()
	desired := desiredAgentPool(cfg, cfg.poolName, "1.35.2")
	live := roundTrip(t, desired)
	smaller := int32(16)
	live.Properties.OSDiskSizeGB = &smaller

	diffs := diffAgentPool(desired, live)
	if len(diffs) == 0 {
		t.Fatal("expected an osDiskSizeGB diff, got none")
	}
}

func TestDesiredAgentPool_DetectsMinMaxCountDrift(t *testing.T) {
	cfg := testConfig()
	desired := desiredAgentPool(cfg, cfg.poolName, "1.35.2")
	live := roundTrip(t, desired)
	newMax := int32(8)
	live.Properties.MaxCount = &newMax

	diffs := diffAgentPool(desired, live)
	if len(diffs) == 0 {
		t.Fatal("expected a maxCount diff, got none")
	}
}

func TestDesiredAgentPool_IgnoresReadOnlyAndAKSManagedFields(t *testing.T) {
	cfg := testConfig()
	desired := desiredAgentPool(cfg, cfg.poolName, "1.35.2")
	live := roundTrip(t, desired)

	// Simulate what a real ARM GET response looks like: extra
	// read-only/status fields and AKS-injected tags that are absent from
	// our PUT body. None of these should trigger a diff.
	provisioning := "Succeeded"
	live.Properties.ProvisioningState = &provisioning
	currentVer := "1.35.2"
	live.Properties.CurrentOrchestratorVersion = &currentVer
	nodeImage := "AKSAzureLinux-V2gen2-202601.01.0"
	live.Properties.NodeImageVersion = &nodeImage
	if live.Properties.Tags == nil {
		live.Properties.Tags = map[string]*string{}
	}
	live.Properties.Tags["aks-managed-poolName"] = strPtr("system-12345678")
	live.Properties.Tags["aks-managed-createOperationID"] = strPtr("abc-123")

	if diffs := diffAgentPool(desired, live); len(diffs) != 0 {
		t.Fatalf("expected no diffs from read-only/aks-managed fields, got: %v", diffs)
	}
}

func TestDesiredAvailabilityZones(t *testing.T) {
	tests := []struct {
		name  string
		mode  string
		zones []string
		want  []string
	}{
		{"enabled with zones", "Enabled", []string{"1", "2", "3"}, []string{"1", "2", "3"}},
		{"enabled without zones", "Enabled", nil, nil},
		{"auto with zones", "Auto", []string{"1", "2"}, []string{"1", "2"}},
		{"auto without zones", "Auto", nil, nil},
		{"disabled with zones", "Disabled", []string{"1", "2"}, nil},
		{"disabled without zones", "Disabled", nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ptrSliceToStrings(desiredAvailabilityZones(tt.mode, tt.zones))
			if !stringSlicesEqual(got, tt.want) {
				t.Fatalf("mode=%s zones=%v: want %v, got %v", tt.mode, tt.zones, tt.want, got)
			}
		})
	}
}

func TestDesiredTempAgentPool_DiffersOnlyByNameAndScaling(t *testing.T) {
	cfg := testConfig()
	system := desiredAgentPool(cfg, cfg.poolName, "1.35.2")
	temp := desiredTempAgentPool(cfg, "1.35.2")

	if *temp.Name != cfg.tempPoolName {
		t.Fatalf("expected temp pool name %q, got %q", cfg.tempPoolName, *temp.Name)
	}
	if boolDeref(temp.Properties.EnableAutoScaling) {
		t.Fatal("expected temp pool to have autoscaling disabled")
	}
	if int32Deref(temp.Properties.Count) != 1 {
		t.Fatalf("expected temp pool count=1, got %d", int32Deref(temp.Properties.Count))
	}

	// Everything else (vmSize, disk, subnet, labels, taints, tags minus
	// purpose) must be identical to the system spec.
	tempLive := roundTrip(t, temp)
	tempLive.Name = system.Name
	tempLive.Properties.EnableAutoScaling = system.Properties.EnableAutoScaling
	tempLive.Properties.Count = nil
	minCount, maxCount := cfg.minCount, cfg.maxCount
	tempLive.Properties.MinCount = &minCount
	tempLive.Properties.MaxCount = &maxCount
	delete(tempLive.Properties.Tags, tempPoolTagKey)

	if diffs := diffAgentPool(system, tempLive); len(diffs) != 0 {
		t.Fatalf("expected temp/system specs to be identical apart from name+scaling, got: %v", diffs)
	}
}

func TestLoadConfig_RequiresAllFields(t *testing.T) {
	full := map[string]string{
		"SUBSCRIPTION_ID":   "sub",
		"RESOURCE_GROUP":    "rg",
		"CLUSTER_NAME":      "cluster",
		"VNET_NAME":         "aks-net",
		"SYSTEM_POOL_NAME":  "system",
		"SYSTEM_VM_SIZE":    "Standard_E8s_v3",
		"SYSTEM_MIN_COUNT":  "1",
		"SYSTEM_MAX_COUNT":  "4",
		"SYSTEM_OS_DISK_GB": "32",
		"SYSTEM_ZONE_MODE":  "Auto",
	}
	// SYSTEM_ZONES is intentionally omitted from `full`: it's optional
	// (an empty/absent value means "no zone pinning"), unlike every other
	// field below.
	for missing := range full {
		t.Run("missing_"+missing, func(t *testing.T) {
			env := map[string]string{}
			for k, v := range full {
				if k != missing {
					env[k] = v
				}
			}
			_, err := loadConfig(func(k string) string { return env[k] })
			if err == nil {
				t.Fatalf("expected error when %s is missing", missing)
			}
		})
	}

	cfg, err := loadConfig(func(k string) string { return full[k] })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.tempPoolName != "systmp" {
		t.Fatalf("expected temp pool name systmp, got %q", cfg.tempPoolName)
	}
	if cfg.minCount != 1 || cfg.maxCount != 4 || cfg.osDiskSizeGB != 32 {
		t.Fatalf("unexpected numeric fields: %+v", cfg)
	}
}

func TestLoadConfig_DryRun(t *testing.T) {
	env := map[string]string{
		"SUBSCRIPTION_ID":   "sub",
		"RESOURCE_GROUP":    "rg",
		"CLUSTER_NAME":      "cluster",
		"VNET_NAME":         "aks-net",
		"SYSTEM_POOL_NAME":  "system",
		"SYSTEM_VM_SIZE":    "Standard_E8s_v3",
		"SYSTEM_MIN_COUNT":  "1",
		"SYSTEM_MAX_COUNT":  "4",
		"SYSTEM_OS_DISK_GB": "32",
		"SYSTEM_ZONES":      "1,2,3",
		"SYSTEM_ZONE_MODE":  "Enabled",
		"DRY_RUN":           "true",
	}
	cfg, err := loadConfig(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.dryRun {
		t.Fatal("expected dryRun=true")
	}
	if !stringSlicesEqual(cfg.zones, []string{"1", "2", "3"}) {
		t.Fatalf("expected zones [1 2 3], got %v", cfg.zones)
	}
}

func TestCSVToSlice(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"  ", nil},
		{"1", []string{"1"}},
		{"1,2,3", []string{"1", "2", "3"}},
		{" 1 , 2 ,3 ", []string{"1", "2", "3"}},
	}
	for _, tt := range tests {
		if got := csvToSlice(tt.in); !stringSlicesEqual(got, tt.want) {
			t.Fatalf("csvToSlice(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func containsAll(s, substr string) bool {
	return len(s) >= len(substr) && (indexOf(s, substr) >= 0)
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
