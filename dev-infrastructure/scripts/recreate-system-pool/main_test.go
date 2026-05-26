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
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	armcs "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"
)

// =============================================================================
// parseEnvConfig
// =============================================================================

func envFromMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestParseEnvConfig_MinimalRequiredFields(t *testing.T) {
	env := envFromMap(map[string]string{
		"CLUSTER_NAME":   "int-uksouth-mgmt-1",
		"RESOURCE_GROUP": "hcp-underlay-int-uksouth-mgmt-1",
	})
	c, err := parseEnvConfig(env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.clusterName != "int-uksouth-mgmt-1" {
		t.Errorf("clusterName=%q", c.clusterName)
	}
	if c.resourceGroup != "hcp-underlay-int-uksouth-mgmt-1" {
		t.Errorf("resourceGroup=%q", c.resourceGroup)
	}
	if c.threshold != defaultThreshold {
		t.Errorf("threshold=%d want default %d", c.threshold, defaultThreshold)
	}
	if c.windowMin != defaultWindowMin {
		t.Errorf("windowMin=%d want default %d", c.windowMin, defaultWindowMin)
	}
	if c.dryRun {
		t.Errorf("dryRun=true, want false by default")
	}
}

func TestParseEnvConfig_MissingClusterName(t *testing.T) {
	env := envFromMap(map[string]string{"RESOURCE_GROUP": "rg"})
	_, err := parseEnvConfig(env)
	if err == nil || !strings.Contains(err.Error(), "CLUSTER_NAME") {
		t.Errorf("expected CLUSTER_NAME error, got %v", err)
	}
}

func TestParseEnvConfig_MissingResourceGroup(t *testing.T) {
	env := envFromMap(map[string]string{"CLUSTER_NAME": "c"})
	_, err := parseEnvConfig(env)
	if err == nil || !strings.Contains(err.Error(), "RESOURCE_GROUP") {
		t.Errorf("expected RESOURCE_GROUP error, got %v", err)
	}
}

func TestParseEnvConfig_CustomThresholdAndWindow(t *testing.T) {
	env := envFromMap(map[string]string{
		"CLUSTER_NAME":        "c",
		"RESOURCE_GROUP":      "rg",
		"NRP_FAIL_THRESHOLD":  "25",
		"NRP_FAIL_WINDOW_MIN": "30",
	})
	c, err := parseEnvConfig(env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.threshold != 25 || c.windowMin != 30 {
		t.Errorf("threshold=%d windowMin=%d", c.threshold, c.windowMin)
	}
}

func TestParseEnvConfig_InvalidThreshold(t *testing.T) {
	cases := []struct {
		name string
		v    string
	}{
		{"non-numeric", "abc"},
		{"zero", "0"},
		{"negative", "-5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := envFromMap(map[string]string{
				"CLUSTER_NAME":       "c",
				"RESOURCE_GROUP":     "rg",
				"NRP_FAIL_THRESHOLD": tc.v,
			})
			if _, err := parseEnvConfig(env); err == nil {
				t.Errorf("expected error for threshold=%q", tc.v)
			}
		})
	}
}

func TestParseEnvConfig_InvalidWindow(t *testing.T) {
	cases := []struct {
		name string
		v    string
	}{
		{"non-numeric", "xyz"},
		{"zero", "0"},
		{"negative", "-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := envFromMap(map[string]string{
				"CLUSTER_NAME":        "c",
				"RESOURCE_GROUP":      "rg",
				"NRP_FAIL_WINDOW_MIN": tc.v,
			})
			if _, err := parseEnvConfig(env); err == nil {
				t.Errorf("expected error for window=%q", tc.v)
			}
		})
	}
}

func TestParseEnvConfig_DryRunFlag(t *testing.T) {
	cases := []struct {
		v    string
		want bool
	}{
		{"true", true},
		{"True", true},
		{"TRUE", true},
		{"1", true},
		{"yes", true},
		{" true ", true},
		{"false", false},
		{"0", false},
		{"", false},
		{"no", false},
		{"random", false},
	}
	for _, tc := range cases {
		t.Run(tc.v, func(t *testing.T) {
			env := envFromMap(map[string]string{
				"CLUSTER_NAME":   "c",
				"RESOURCE_GROUP": "rg",
				"DRY_RUN":        tc.v,
			})
			c, err := parseEnvConfig(env)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.dryRun != tc.want {
				t.Errorf("DRY_RUN=%q: dryRun=%t want %t", tc.v, c.dryRun, tc.want)
			}
		})
	}
}

// =============================================================================
// evalGuard1..4
// =============================================================================

func TestEvalGuard1(t *testing.T) {
	cases := []struct {
		name       string
		ready, min int32
		wantPass   bool
	}{
		{"degraded_ready_zero", 0, 2, true},
		{"degraded_ready_one_of_two", 1, 2, true},
		{"healthy_ready_equals_min", 2, 2, false},
		{"healthy_ready_above_min", 3, 2, false},
		{"invalid_min_zero", 0, 0, false},
		{"invalid_min_negative", 0, -1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pass, reason := evalGuard1(tc.ready, tc.min)
			if pass != tc.wantPass {
				t.Errorf("ready=%d min=%d: pass=%t want %t (%s)", tc.ready, tc.min, pass, tc.wantPass, reason)
			}
			if !pass && reason == "" {
				t.Errorf("non-pass result must have a reason")
			}
		})
	}
}

func TestEvalGuard2(t *testing.T) {
	cases := []struct {
		name                string
		failures, threshold int
		wantPass            bool
	}{
		{"below_threshold", 9, 10, false},
		{"at_threshold", 10, 10, true},
		{"above_threshold", 50, 10, true},
		{"zero_failures", 0, 10, false},
		{"invalid_threshold_zero", 100, 0, false},
		{"invalid_threshold_negative", 100, -1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pass, reason := evalGuard2(tc.failures, tc.threshold)
			if pass != tc.wantPass {
				t.Errorf("failures=%d threshold=%d: pass=%t want %t (%s)", tc.failures, tc.threshold, pass, tc.wantPass, reason)
			}
			if !pass && reason == "" {
				t.Errorf("non-pass result must have a reason")
			}
		})
	}
}

func TestEvalGuard3(t *testing.T) {
	cases := []struct {
		name  string
		state string
		want  bool
	}{
		// Settled states: action allowed (no LRO to race).
		{"succeeded", "Succeeded", true},
		{"canceled", "Canceled", true},
		{"failed", "Failed", true}, // NRP-KVS wedge often lands here
		// Active LROs that ARE the wedge symptom: action allowed at the
		// guard level. Step 2 (maybeAbortLRO) will decide whether to
		// abort (>= 30 min old) or no-op exit (younger LRO).
		// AROSLSRE-880 / INT 2026-05-16..18: cluster stuck in Updating
		// for days while the system pool upgrade LRO retried forever.
		{"updating", "Updating", true},
		{"upgrading", "Upgrading", true},
		// Rejected: cluster either isn't fully there yet or is being
		// torn down. We never want to act in those states.
		{"creating", "Creating", false},
		{"deleting", "Deleting", false},
		// Empty is treated as malformed input -> reject conservatively.
		{"empty", "", false},
		// Unknown future states: reject conservatively. Better to no-op
		// and surface to humans than to act on a state we have not
		// reasoned about.
		{"unknown_state", "SomethingNew", false},
		// Case-sensitive on purpose: AKS uses TitleCase.
		{"lowercase_succeeded", "succeeded", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pass, reason := evalGuard3(tc.state)
			if pass != tc.want {
				t.Errorf("state=%q: pass=%t want %t (%s)", tc.state, pass, tc.want, reason)
			}
			if !pass && reason == "" {
				t.Errorf("non-pass result must have a reason")
			}
		})
	}
}

func mkPool(name string, count, minCount *int32) *armcs.AgentPool {
	return &armcs.AgentPool{
		Name: ptr(name),
		Properties: &armcs.ManagedClusterAgentPoolProfileProperties{
			Count:    count,
			MinCount: minCount,
		},
	}
}

// mkPoolWithState builds a pool with provisioning state set, used for
// guard 4/5 interactions.
func mkPoolWithState(name string, count, minCount *int32, provState string) *armcs.AgentPool {
	p := mkPool(name, count, minCount)
	if provState != "" {
		ps := provState
		p.Properties.ProvisioningState = &ps
	}
	return p
}

func TestEvalGuard4_AllHealthy(t *testing.T) {
	pools := []*armcs.AgentPool{
		mkPoolWithState("system", ptr(int32(2)), ptr(int32(2)), "Succeeded"),
		mkPool("userswft3", ptr(int32(4)), ptr(int32(4))),
	}
	pass, sysMin, sysState, reason := evalGuard4(pools)
	if !pass {
		t.Fatalf("expected pass, got fail: %s", reason)
	}
	if sysMin != 2 {
		t.Errorf("systemMin=%d want 2", sysMin)
	}
	if sysState != "Succeeded" {
		t.Errorf("sysState=%q want Succeeded", sysState)
	}
}

func TestEvalGuard4_NonSystemPoolEmpty(t *testing.T) {
	pools := []*armcs.AgentPool{
		mkPool("system", ptr(int32(2)), ptr(int32(2))),
		mkPool("userswft3", ptr(int32(0)), ptr(int32(0))),
	}
	pass, _, _, reason := evalGuard4(pools)
	if pass {
		t.Fatal("expected fail when non-system pool has count=0")
	}
	if !strings.Contains(reason, "userswft3") {
		t.Errorf("reason should mention failing pool, got %q", reason)
	}
}

func TestEvalGuard4_NoSystemPool(t *testing.T) {
	pools := []*armcs.AgentPool{
		mkPool("userswft3", ptr(int32(4)), ptr(int32(4))),
	}
	pass, _, _, reason := evalGuard4(pools)
	if pass {
		t.Fatal("expected fail when no system pool")
	}
	if !strings.Contains(reason, "system pool") {
		t.Errorf("reason should mention missing system pool, got %q", reason)
	}
}

func TestEvalGuard4_SystemPoolWithZeroCount_OK(t *testing.T) {
	// System pool itself can have count=0 (that's the whole point of this script).
	pools := []*armcs.AgentPool{
		mkPool("system", ptr(int32(0)), ptr(int32(2))),
		mkPool("userswft3", ptr(int32(4)), ptr(int32(4))),
	}
	pass, sysMin, _, reason := evalGuard4(pools)
	if !pass {
		t.Fatalf("system pool with count=0 should not fail guard 4: %s", reason)
	}
	if sysMin != 2 {
		t.Errorf("systemMin=%d want 2", sysMin)
	}
}

func TestEvalGuard4_NilPoolsSkipped(t *testing.T) {
	pools := []*armcs.AgentPool{
		nil,
		mkPool("system", ptr(int32(2)), ptr(int32(2))),
		nil,
		mkPool("userswft3", ptr(int32(4)), ptr(int32(4))),
	}
	pass, _, _, reason := evalGuard4(pools)
	if !pass {
		t.Fatalf("nil pools should be skipped: %s", reason)
	}
}

func TestEvalGuard4_PoolMissingFieldsSkipped(t *testing.T) {
	pools := []*armcs.AgentPool{
		{Name: ptr("orphan")}, // no properties
		{Properties: &armcs.ManagedClusterAgentPoolProfileProperties{Count: ptr(int32(3))}}, // no name
		mkPool("system", ptr(int32(1)), ptr(int32(2))),
		mkPool("userswft3", ptr(int32(4)), ptr(int32(4))),
	}
	pass, _, _, reason := evalGuard4(pools)
	if !pass {
		t.Fatalf("malformed pool entries should be skipped: %s", reason)
	}
}

func TestEvalGuard4_SystemMissingMinCount_DefaultsZero(t *testing.T) {
	pools := []*armcs.AgentPool{
		mkPool("system", ptr(int32(1)), nil),
		mkPool("userswft3", ptr(int32(4)), ptr(int32(4))),
	}
	pass, sysMin, _, _ := evalGuard4(pools)
	if !pass || sysMin != 0 {
		t.Errorf("systemMin=%d pass=%t (want 0/true)", sysMin, pass)
	}
}

func TestEvalGuard4_EmptyList(t *testing.T) {
	pass, _, _, reason := evalGuard4(nil)
	if pass {
		t.Fatal("empty pool list must fail")
	}
	if reason == "" {
		t.Errorf("empty list fail must have a reason")
	}
}

func TestEvalGuard4_ExtractsSystemProvState(t *testing.T) {
	cases := []struct {
		name  string
		state string
	}{
		{"Succeeded", "Succeeded"},
		{"Failed", "Failed"},
		{"Updating", "Updating"},
		{"Canceled", "Canceled"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pools := []*armcs.AgentPool{
				mkPoolWithState("system", ptr(int32(0)), ptr(int32(2)), tc.state),
				mkPool("userswft3", ptr(int32(4)), ptr(int32(4))),
			}
			pass, _, gotState, _ := evalGuard4(pools)
			if !pass {
				t.Fatal("expected pass")
			}
			if gotState != tc.state {
				t.Errorf("systemProvState=%q want %q", gotState, tc.state)
			}
		})
	}
}

// =============================================================================
// sanitizeForRecreate
// =============================================================================

func mkLiveSystemPool() *armcs.AgentPool {
	mode := armcs.AgentPoolModeSystem
	osType := armcs.OSTypeLinux
	osSKU := armcs.OSSKUAzureLinux
	osDiskType := armcs.OSDiskTypeEphemeral
	powerCode := armcs.Code("Running")
	provState := "Succeeded"
	curOrch := "1.35.4"
	nodeImg := "AKSAzureLinux-V3gen2-202605.20.0"
	mainV := "1.35.4"
	count := int32(2)
	minc := int32(2)
	maxc := int32(4)
	maxPods := int32(100)
	diskGB := int32(128)
	tru := true
	return &armcs.AgentPool{
		ID:   ptr("/subscriptions/x/resourceGroups/y/providers/Microsoft.ContainerService/managedClusters/c/agentPools/system"),
		Name: ptr("system"),
		Type: ptr("Microsoft.ContainerService/managedClusters/agentPools"),
		Properties: &armcs.ManagedClusterAgentPoolProfileProperties{
			ProvisioningState:          &provState,
			CurrentOrchestratorVersion: &curOrch,
			NodeImageVersion:           &nodeImg,
			PowerState:                 &armcs.PowerState{Code: &powerCode},
			OrchestratorVersion:        &mainV,
			Mode:                       &mode,
			VMSize:                     ptr("Standard_E8ds_v5"),
			OSType:                     &osType,
			OSSKU:                      &osSKU,
			OSDiskType:                 &osDiskType,
			OSDiskSizeGB:               &diskGB,
			Count:                      &count,
			MinCount:                   &minc,
			MaxCount:                   &maxc,
			MaxPods:                    &maxPods,
			EnableEncryptionAtHost:     &tru,
			EnableFIPS:                 &tru,
			VnetSubnetID:               ptr("/subscriptions/x/.../subnets/system"),
			PodSubnetID:                ptr("/subscriptions/x/.../subnets/pods"),
			NodeTaints:                 []*string{ptr("CriticalAddonsOnly=true:NoSchedule")},
			NodeLabels:                 map[string]*string{"aro-hcp.azure.com/role": ptr("system")},
			Tags: map[string]*string{
				"user-tag":                 ptr("keep-me"),
				"aks-managed-foo":          ptr("drop-me"),
				"aks-managed-orchestrator": ptr("drop-me-too"),
			},
		},
	}
}

func TestSanitizeForRecreate_NilInput(t *testing.T) {
	if _, err := sanitizeForRecreate(nil, "1.35.4"); err == nil {
		t.Fatal("expected error for nil input")
	}
}

func TestSanitizeForRecreate_StripsTopLevelReadOnly(t *testing.T) {
	live := mkLiveSystemPool()
	out, err := sanitizeForRecreate(live, "1.35.4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ID != nil {
		t.Errorf("ID not stripped")
	}
	if out.Name != nil {
		t.Errorf("Name not stripped")
	}
	if out.Type != nil {
		t.Errorf("Type not stripped")
	}
}

func TestSanitizeForRecreate_StripsPropertyReadOnly(t *testing.T) {
	live := mkLiveSystemPool()
	out, _ := sanitizeForRecreate(live, "1.35.4")
	p := out.Properties
	if p.ProvisioningState != nil {
		t.Errorf("ProvisioningState not stripped")
	}
	if p.CurrentOrchestratorVersion != nil {
		t.Errorf("CurrentOrchestratorVersion not stripped")
	}
	if p.NodeImageVersion != nil {
		t.Errorf("NodeImageVersion not stripped")
	}
	if p.PowerState != nil {
		t.Errorf("PowerState not stripped")
	}
	if p.CreationData != nil {
		t.Errorf("CreationData not stripped")
	}
}

func TestSanitizeForRecreate_PreservesWriteableFields(t *testing.T) {
	live := mkLiveSystemPool()
	out, _ := sanitizeForRecreate(live, "1.35.4")
	p := out.Properties
	if p.VMSize == nil || *p.VMSize != "Standard_E8ds_v5" {
		t.Errorf("VMSize not preserved: %v", p.VMSize)
	}
	if p.Count == nil || *p.Count != 2 {
		t.Errorf("Count not preserved: %v", p.Count)
	}
	if p.MinCount == nil || *p.MinCount != 2 {
		t.Errorf("MinCount not preserved: %v", p.MinCount)
	}
	if p.MaxCount == nil || *p.MaxCount != 4 {
		t.Errorf("MaxCount not preserved: %v", p.MaxCount)
	}
	if p.OSDiskSizeGB == nil || *p.OSDiskSizeGB != 128 {
		t.Errorf("OSDiskSizeGB not preserved")
	}
	if p.EnableEncryptionAtHost == nil || !*p.EnableEncryptionAtHost {
		t.Errorf("EnableEncryptionAtHost not preserved")
	}
	if p.EnableFIPS == nil || !*p.EnableFIPS {
		t.Errorf("EnableFIPS not preserved")
	}
	if p.VnetSubnetID == nil || *p.VnetSubnetID != "/subscriptions/x/.../subnets/system" {
		t.Errorf("VnetSubnetID not preserved")
	}
	if p.PodSubnetID == nil || *p.PodSubnetID != "/subscriptions/x/.../subnets/pods" {
		t.Errorf("PodSubnetID not preserved")
	}
	if p.Mode == nil || *p.Mode != armcs.AgentPoolModeSystem {
		t.Errorf("Mode not preserved")
	}
}

func TestSanitizeForRecreate_OverridesOrchestratorVersion(t *testing.T) {
	live := mkLiveSystemPool()
	out, _ := sanitizeForRecreate(live, "1.36.2")
	if out.Properties.OrchestratorVersion == nil || *out.Properties.OrchestratorVersion != "1.36.2" {
		t.Errorf("OrchestratorVersion=%v want 1.36.2", out.Properties.OrchestratorVersion)
	}
}

func TestSanitizeForRecreate_StripsAKSManagedTags(t *testing.T) {
	live := mkLiveSystemPool()
	out, _ := sanitizeForRecreate(live, "1.35.4")
	tags := out.Properties.Tags
	if _, ok := tags["aks-managed-foo"]; ok {
		t.Errorf("aks-managed-foo not stripped")
	}
	if _, ok := tags["aks-managed-orchestrator"]; ok {
		t.Errorf("aks-managed-orchestrator not stripped")
	}
	if v, ok := tags["user-tag"]; !ok || v == nil || *v != "keep-me" {
		t.Errorf("user-tag not preserved: %v", tags)
	}
}

func TestSanitizeForRecreate_DoesNotMutateInput(t *testing.T) {
	live := mkLiveSystemPool()
	beforeRaw, _ := json.Marshal(live)

	_, err := sanitizeForRecreate(live, "1.99.99")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	afterRaw, _ := json.Marshal(live)
	if !reflect.DeepEqual(beforeRaw, afterRaw) {
		t.Errorf("input was mutated\nbefore=%s\nafter =%s", string(beforeRaw), string(afterRaw))
	}

	// Spot-check key fields explicitly so a future serializer change still trips this.
	if live.ID == nil || *live.ID == "" {
		t.Errorf("live.ID was mutated to %v", live.ID)
	}
	if live.Properties.ProvisioningState == nil || *live.Properties.ProvisioningState != "Succeeded" {
		t.Errorf("live.ProvisioningState was mutated")
	}
	if live.Properties.OrchestratorVersion == nil || *live.Properties.OrchestratorVersion != "1.35.4" {
		t.Errorf("live.OrchestratorVersion was mutated to %v", live.Properties.OrchestratorVersion)
	}
	if _, ok := live.Properties.Tags["aks-managed-foo"]; !ok {
		t.Errorf("live.Tags was mutated")
	}
}

func TestSanitizeForRecreate_NilTagsOK(t *testing.T) {
	live := mkLiveSystemPool()
	live.Properties.Tags = nil
	out, err := sanitizeForRecreate(live, "1.35.4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Properties.Tags) != 0 {
		t.Errorf("expected nil or empty tags, got %v", out.Properties.Tags)
	}
}

func TestSanitizeForRecreate_NilProperties(t *testing.T) {
	live := &armcs.AgentPool{Name: ptr("system")}
	_, err := sanitizeForRecreate(live, "1.35.4")
	if err == nil {
		t.Fatal("expected error when Properties is nil")
	}
}

// =============================================================================
// buildSystmpAgentPool
// =============================================================================

func TestBuildSystmpAgentPool_ValidInputs(t *testing.T) {
	live := mkLiveSystemPool()
	body, err := buildSystmpAgentPool(live, "1.35.4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	p := body.Properties
	if p.VMSize == nil || *p.VMSize != "Standard_E8ds_v5" {
		t.Errorf("systmp VMSize should match live: %v", p.VMSize)
	}
	if p.Count == nil || *p.Count != 1 {
		t.Errorf("systmp Count should be 1, got %v", p.Count)
	}
	if p.OrchestratorVersion == nil || *p.OrchestratorVersion != "1.35.4" {
		t.Errorf("OrchestratorVersion: %v", p.OrchestratorVersion)
	}
	if p.Mode == nil || *p.Mode != armcs.AgentPoolModeSystem {
		t.Errorf("Mode should be System")
	}
	if p.EnableFIPS == nil || !*p.EnableFIPS {
		t.Errorf("FIPS should be enabled")
	}
	if p.EnableEncryptionAtHost == nil || !*p.EnableEncryptionAtHost {
		t.Errorf("EncryptionAtHost should be enabled")
	}
	if len(p.NodeTaints) != 1 || *p.NodeTaints[0] != "CriticalAddonsOnly=true:NoSchedule" {
		t.Errorf("CriticalAddonsOnly taint missing: %v", p.NodeTaints)
	}
	if p.VnetSubnetID == nil || *p.VnetSubnetID != "/subscriptions/x/.../subnets/system" {
		t.Errorf("VnetSubnetID should be inherited")
	}
}

func TestBuildSystmpAgentPool_NilLive(t *testing.T) {
	if _, err := buildSystmpAgentPool(nil, "1.35.4"); err == nil {
		t.Fatal("expected error for nil live")
	}
}

func TestBuildSystmpAgentPool_NilProperties(t *testing.T) {
	if _, err := buildSystmpAgentPool(&armcs.AgentPool{}, "1.35.4"); err == nil {
		t.Fatal("expected error for nil properties")
	}
}

func TestBuildSystmpAgentPool_MissingVMSize(t *testing.T) {
	live := mkLiveSystemPool()
	live.Properties.VMSize = nil
	if _, err := buildSystmpAgentPool(live, "1.35.4"); err == nil {
		t.Fatal("expected error for missing VMSize")
	}
	live.Properties.VMSize = ptr("")
	if _, err := buildSystmpAgentPool(live, "1.35.4"); err == nil {
		t.Fatal("expected error for empty VMSize")
	}
}

func TestBuildSystmpAgentPool_MissingCPVersion(t *testing.T) {
	live := mkLiveSystemPool()
	if _, err := buildSystmpAgentPool(live, ""); err == nil {
		t.Fatal("expected error for empty cpVersion")
	}
}

func TestBuildSystmpAgentPool_NoPodSubnet(t *testing.T) {
	live := mkLiveSystemPool()
	live.Properties.PodSubnetID = nil
	body, err := buildSystmpAgentPool(live, "1.35.4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if body.Properties.PodSubnetID != nil {
		t.Errorf("PodSubnetID should remain nil when live has none")
	}
}

func TestBuildSystmpAgentPool_DoesNotShareTaintPointer(t *testing.T) {
	// Mutating the live snapshot's taints must not affect the systmp body.
	live := mkLiveSystemPool()
	body, _ := buildSystmpAgentPool(live, "1.35.4")
	*live.Properties.NodeTaints[0] = "hacked"
	if *body.Properties.NodeTaints[0] != "CriticalAddonsOnly=true:NoSchedule" {
		t.Errorf("systmp NodeTaints share state with live: %v", body.Properties.NodeTaints)
	}
}

func TestBuildSystmpAgentPool_MissingOSDiskSizeGB(t *testing.T) {
	live := mkLiveSystemPool()
	live.Properties.OSDiskSizeGB = nil
	if _, err := buildSystmpAgentPool(live, "1.35.4"); err == nil {
		t.Fatal("expected error for missing OSDiskSizeGB")
	}
	zero := int32(0)
	live.Properties.OSDiskSizeGB = &zero
	if _, err := buildSystmpAgentPool(live, "1.35.4"); err == nil {
		t.Fatal("expected error for OSDiskSizeGB == 0")
	}
}

func TestBuildSystmpAgentPool_InheritsDiskSizeAndOSType(t *testing.T) {
	live := mkLiveSystemPool()
	custom := int32(64)
	live.Properties.OSDiskSizeGB = &custom
	body, err := buildSystmpAgentPool(live, "1.35.4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if body.Properties.OSDiskSizeGB == nil || *body.Properties.OSDiskSizeGB != 64 {
		t.Errorf("OSDiskSizeGB not inherited from live: %v", body.Properties.OSDiskSizeGB)
	}
	if body.Properties.OSDiskType == nil || *body.Properties.OSDiskType != armcs.OSDiskTypeEphemeral {
		t.Errorf("OSDiskType not inherited from live: %v", body.Properties.OSDiskType)
	}
	if body.Properties.OSType == nil || *body.Properties.OSType != armcs.OSTypeLinux {
		t.Errorf("OSType not inherited: %v", body.Properties.OSType)
	}
	if body.Properties.OSSKU == nil || *body.Properties.OSSKU != armcs.OSSKUAzureLinux {
		t.Errorf("OSSKU not inherited: %v", body.Properties.OSSKU)
	}
}

// =============================================================================
// countNRPFailures
// =============================================================================

func mkActivityEvent(status, op, resID string) map[string]any {
	return map[string]any{
		"status":        map[string]string{"value": status},
		"operationName": map[string]string{"value": op},
		"resourceId":    resID,
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestCountNRPFailures_EmptyJSONArray(t *testing.T) {
	if n := countNRPFailures([]byte("[]"), "aks-system-"); n != 0 {
		t.Errorf("got %d want 0", n)
	}
}

func TestCountNRPFailures_InvalidJSONReturnsZero(t *testing.T) {
	if n := countNRPFailures([]byte("not json"), "aks-system-"); n != 0 {
		t.Errorf("got %d want 0 on invalid input", n)
	}
	if n := countNRPFailures(nil, "aks-system-"); n != 0 {
		t.Errorf("got %d want 0 on nil input", n)
	}
}

func TestCountNRPFailures_OnlyFailedCounted(t *testing.T) {
	events := []map[string]any{
		mkActivityEvent("Succeeded", "Microsoft.Compute/virtualMachineScaleSets/write",
			"/subscriptions/x/resourceGroups/rg/providers/Microsoft.Compute/virtualMachineScaleSets/aks-system-1"),
		mkActivityEvent("Failed", "Microsoft.Compute/virtualMachineScaleSets/write",
			"/subscriptions/x/resourceGroups/rg/providers/Microsoft.Compute/virtualMachineScaleSets/aks-system-1"),
		mkActivityEvent("Failed", "Microsoft.Compute/virtualMachineScaleSets/write",
			"/subscriptions/x/resourceGroups/rg/providers/Microsoft.Compute/virtualMachineScaleSets/aks-system-2"),
	}
	got := countNRPFailures(mustMarshal(t, events), "aks-system-")
	if got != 2 {
		t.Errorf("got %d want 2", got)
	}
}

func TestCountNRPFailures_FiltersByVMSSOperation(t *testing.T) {
	events := []map[string]any{
		mkActivityEvent("Failed", "Microsoft.Network/networkInterfaces/write",
			"/subscriptions/x/.../networkInterfaces/foo"),
		mkActivityEvent("Failed", "Microsoft.Compute/virtualMachineScaleSets/write",
			"/subscriptions/x/.../virtualMachineScaleSets/aks-system-1"),
	}
	got := countNRPFailures(mustMarshal(t, events), "aks-system-")
	if got != 1 {
		t.Errorf("got %d want 1 (only VMSS-write failed)", got)
	}
}

func TestCountNRPFailures_PrefixFilter(t *testing.T) {
	events := []map[string]any{
		mkActivityEvent("Failed", "Microsoft.Compute/virtualMachineScaleSets/write",
			"/subscriptions/x/.../virtualMachineScaleSets/aks-system-1"),
		mkActivityEvent("Failed", "Microsoft.Compute/virtualMachineScaleSets/write",
			"/subscriptions/x/.../virtualMachineScaleSets/aks-userswft3-9"),
	}
	if got := countNRPFailures(mustMarshal(t, events), "aks-system-"); got != 1 {
		t.Errorf("prefix filter: got %d want 1", got)
	}
	if got := countNRPFailures(mustMarshal(t, events), ""); got != 2 {
		t.Errorf("empty prefix: got %d want 2", got)
	}
	if got := countNRPFailures(mustMarshal(t, events), "aks-other-"); got != 0 {
		t.Errorf("non-matching prefix: got %d want 0", got)
	}
}

func TestCountNRPFailures_CaseInsensitiveOperationAndPrefix(t *testing.T) {
	events := []map[string]any{
		mkActivityEvent("Failed", "MICROSOFT.COMPUTE/VIRTUALMACHINESCALESETS/WRITE",
			"/SUBSCRIPTIONS/X/.../VIRTUALMACHINESCALESETS/AKS-SYSTEM-1"),
	}
	got := countNRPFailures(mustMarshal(t, events), "aks-system-")
	if got != 1 {
		t.Errorf("case-insensitive match failed: got %d want 1", got)
	}
}

// =============================================================================
// nrpResourceIDs
// =============================================================================

func TestNRPResourceIDs_DedupAndOrder(t *testing.T) {
	events := []map[string]any{
		mkActivityEvent("Failed", "Microsoft.Compute/virtualMachineScaleSets/write", "vmss-A"),
		mkActivityEvent("Failed", "Microsoft.Compute/virtualMachineScaleSets/write", "vmss-B"),
		mkActivityEvent("Failed", "Microsoft.Compute/virtualMachineScaleSets/write", "vmss-A"),
		mkActivityEvent("Succeeded", "Microsoft.Compute/virtualMachineScaleSets/write", "vmss-C"),
		mkActivityEvent("Failed", "Microsoft.Network/networkInterfaces/write", "nic-D"),
	}
	got := nrpResourceIDs(mustMarshal(t, events))
	want := []string{"vmss-A", "vmss-B"}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestNRPResourceIDs_EmptyAndInvalid(t *testing.T) {
	if got := nrpResourceIDs([]byte("[]")); got != nil {
		t.Errorf("empty list got %v want nil", got)
	}
	if got := nrpResourceIDs([]byte("garbage")); got != nil {
		t.Errorf("invalid input got %v want nil", got)
	}
}

// =============================================================================
// isNodeReady
// =============================================================================

func mkNode(name string, conds ...corev1.NodeCondition) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status:     corev1.NodeStatus{Conditions: conds},
	}
}

func TestIsNodeReady(t *testing.T) {
	cases := []struct {
		name string
		node *corev1.Node
		want bool
	}{
		{"nil", nil, false},
		{"no_conditions", mkNode("n1"), false},
		{
			"ready_true",
			mkNode("n1", corev1.NodeCondition{Type: corev1.NodeReady, Status: corev1.ConditionTrue}),
			true,
		},
		{
			"ready_false",
			mkNode("n1", corev1.NodeCondition{Type: corev1.NodeReady, Status: corev1.ConditionFalse}),
			false,
		},
		{
			"ready_unknown",
			mkNode("n1", corev1.NodeCondition{Type: corev1.NodeReady, Status: corev1.ConditionUnknown}),
			false,
		},
		{
			"only_memory_pressure",
			mkNode("n1", corev1.NodeCondition{Type: corev1.NodeMemoryPressure, Status: corev1.ConditionFalse}),
			false,
		},
		{
			"ready_true_with_other_conditions",
			mkNode("n1",
				corev1.NodeCondition{Type: corev1.NodeMemoryPressure, Status: corev1.ConditionFalse},
				corev1.NodeCondition{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
				corev1.NodeCondition{Type: corev1.NodeDiskPressure, Status: corev1.ConditionFalse},
			),
			true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isNodeReady(tc.node); got != tc.want {
				t.Errorf("got %t want %t", got, tc.want)
			}
		})
	}
}

// =============================================================================
// ptr / strDeref helpers
// =============================================================================

func TestPtr(t *testing.T) {
	s := "hello"
	p := ptr(s)
	if p == nil || *p != s {
		t.Errorf("ptr broken")
	}
	i := int32(42)
	pi := ptr(i)
	if pi == nil || *pi != i {
		t.Errorf("ptr generic broken")
	}
}

func TestStrDeref(t *testing.T) {
	if strDeref(nil) != "" {
		t.Errorf("nil should yield empty string")
	}
	s := "hello"
	if strDeref(&s) != "hello" {
		t.Errorf("got %q want hello", strDeref(&s))
	}
}

// =============================================================================
// isNotFoundErr
// =============================================================================

func TestIsNotFoundErr_Nil(t *testing.T) {
	if isNotFoundErr(nil) {
		t.Errorf("nil err must not be considered NotFound")
	}
}

func TestIsNotFoundErr_PlainError(t *testing.T) {
	if isNotFoundErr(errors.New("some random failure")) {
		t.Errorf("plain error must not be considered NotFound")
	}
}

func TestIsNotFoundErr_AzcoreResponse404(t *testing.T) {
	re := &azcore.ResponseError{
		StatusCode: http.StatusNotFound,
		ErrorCode:  "ResourceNotFound",
	}
	if !isNotFoundErr(re) {
		t.Errorf("404 ResponseError must be considered NotFound")
	}
	wrapped := fmt.Errorf("cluster get: %w", re)
	if !isNotFoundErr(wrapped) {
		t.Errorf("wrapped 404 must be considered NotFound")
	}
}

func TestIsNotFoundErr_AzcoreResponseOther(t *testing.T) {
	for _, code := range []int{http.StatusInternalServerError, http.StatusForbidden, http.StatusTooManyRequests, http.StatusUnauthorized, http.StatusConflict} {
		re := &azcore.ResponseError{StatusCode: code}
		if isNotFoundErr(re) {
			t.Errorf("status %d must not be NotFound", code)
		}
	}
}

// =============================================================================
// extractAPIServerAndCA
// =============================================================================

func encodedCA() string {
	return base64.StdEncoding.EncodeToString([]byte("-----BEGIN CERTIFICATE-----\nMIIBfakecert\n-----END CERTIFICATE-----\n"))
}

func sampleKubeconfig(server, caB64 string) []byte {
	return []byte(fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: testcluster
  cluster:
    server: %s
    certificate-authority-data: %s
users:
- name: clusterUser_testcluster
  user:
    token: dummy-token-should-be-ignored
contexts:
- name: testcluster
  context:
    cluster: testcluster
    user: clusterUser_testcluster
current-context: testcluster
`, server, caB64))
}

func TestExtractAPIServerAndCA_HappyPath(t *testing.T) {
	server, ca, err := extractAPIServerAndCA(sampleKubeconfig("https://test.hcp.uksouth.azmk8s.io:443", encodedCA()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if server != "https://test.hcp.uksouth.azmk8s.io:443" {
		t.Errorf("server=%q want https://test.hcp.uksouth.azmk8s.io:443", server)
	}
	if !strings.Contains(string(ca), "BEGIN CERTIFICATE") {
		t.Errorf("CA data not preserved: %q", string(ca))
	}
}

func TestExtractAPIServerAndCA_Empty(t *testing.T) {
	if _, _, err := extractAPIServerAndCA(nil); err == nil {
		t.Error("nil input should error")
	}
	if _, _, err := extractAPIServerAndCA([]byte{}); err == nil {
		t.Error("empty input should error")
	}
}

func TestExtractAPIServerAndCA_InvalidYAML(t *testing.T) {
	if _, _, err := extractAPIServerAndCA([]byte("not: a [valid kubeconfig")); err == nil {
		t.Error("invalid yaml should error")
	}
}

func TestExtractAPIServerAndCA_EmptyServer(t *testing.T) {
	cfg := sampleKubeconfig("", encodedCA())
	if _, _, err := extractAPIServerAndCA(cfg); err == nil {
		t.Error("empty server should error")
	}
}

func TestExtractAPIServerAndCA_EmptyCA(t *testing.T) {
	cfg := sampleKubeconfig("https://x", "")
	if _, _, err := extractAPIServerAndCA(cfg); err == nil {
		t.Error("empty CA should error")
	}
}

func TestExtractAPIServerAndCA_NoClusters(t *testing.T) {
	cfg := []byte("apiVersion: v1\nkind: Config\nclusters: []\nusers: []\ncontexts: []\n")
	if _, _, err := extractAPIServerAndCA(cfg); err == nil {
		t.Error("no clusters should error")
	}
}

// =============================================================================
// kubeconfigWithBearerToken
// =============================================================================

func TestKubeconfigWithBearerToken_AllFields(t *testing.T) {
	cfg := kubeconfigWithBearerToken("mycluster", "https://api.example", []byte("CADATA"), "BEARER-XYZ")
	if cfg.CurrentContext != "mycluster" {
		t.Errorf("CurrentContext=%q", cfg.CurrentContext)
	}
	cl, ok := cfg.Clusters["mycluster"]
	if !ok || cl == nil {
		t.Fatal("cluster entry missing")
	}
	if cl.Server != "https://api.example" {
		t.Errorf("Server=%q", cl.Server)
	}
	if string(cl.CertificateAuthorityData) != "CADATA" {
		t.Errorf("CA data not preserved")
	}
	auth, ok := cfg.AuthInfos["mycluster"]
	if !ok || auth == nil {
		t.Fatal("auth entry missing")
	}
	if auth.Token != "BEARER-XYZ" {
		t.Errorf("Token=%q", auth.Token)
	}
	ctxEntry, ok := cfg.Contexts["mycluster"]
	if !ok || ctxEntry == nil {
		t.Fatal("context entry missing")
	}
	if ctxEntry.Cluster != "mycluster" || ctxEntry.AuthInfo != "mycluster" {
		t.Errorf("context wires: cluster=%q auth=%q", ctxEntry.Cluster, ctxEntry.AuthInfo)
	}
}

func TestKubeconfigWithBearerToken_NoExecPluginsRequired(t *testing.T) {
	// Critical: the generated kubeconfig must NOT contain any exec
	// auth provider or external dependency. Other Shell steps in EV2
	// would fail if our kubeconfig required kubelogin/etc.
	cfg := kubeconfigWithBearerToken("c", "https://x", []byte("ca"), "tok")
	for name, ai := range cfg.AuthInfos {
		if ai.Exec != nil {
			t.Errorf("AuthInfo %q has Exec plugin (forbidden)", name)
		}
		if ai.AuthProvider != nil {
			t.Errorf("AuthInfo %q has AuthProvider (forbidden)", name)
		}
	}
}

// =============================================================================
// evalGuard5  (system pool provisioningState == "Failed")
// =============================================================================

func TestEvalGuard5(t *testing.T) {
	cases := []struct {
		name  string
		state string
		want  bool
	}{
		// Wedge-compatible states.
		{"failed", "Failed", true},
		{"canceled", "Canceled", true},
		{"updating", "Updating", true}, // AROSLSRE-880 wedge signature
		{"upgrading", "Upgrading", true},
		// Healthy: explicitly reject.
		{"succeeded", "Succeeded", false},
		// Transitional: explicitly reject.
		{"creating", "Creating", false},
		{"deleting", "Deleting", false},
		// Defensive rejects.
		{"empty", "", false},
		{"lowercase_failed", "failed", false}, // case-sensitive on purpose
		{"unknown", "SomethingFuture", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pass, reason := evalGuard5(tc.state)
			if pass != tc.want {
				t.Errorf("state=%q: pass=%t want %t (%s)", tc.state, pass, tc.want, reason)
			}
			if !pass && reason == "" {
				t.Errorf("non-pass result must have a reason")
			}
		})
	}
}
