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
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

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
		"CLUSTER_NAME":    "int-uksouth-mgmt-1",
		"RESOURCE_GROUP":  "hcp-underlay-int-uksouth-mgmt-1",
		"SUBSCRIPTION_ID": "sub-0001",
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
	if c.subscriptionID != "sub-0001" {
		t.Errorf("subscriptionID=%q", c.subscriptionID)
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
	env := envFromMap(map[string]string{"RESOURCE_GROUP": "rg", "SUBSCRIPTION_ID": "sub"})
	_, err := parseEnvConfig(env)
	if err == nil || !strings.Contains(err.Error(), "CLUSTER_NAME") {
		t.Errorf("expected CLUSTER_NAME error, got %v", err)
	}
}

func TestParseEnvConfig_MissingResourceGroup(t *testing.T) {
	env := envFromMap(map[string]string{"CLUSTER_NAME": "c", "SUBSCRIPTION_ID": "sub"})
	_, err := parseEnvConfig(env)
	if err == nil || !strings.Contains(err.Error(), "RESOURCE_GROUP") {
		t.Errorf("expected RESOURCE_GROUP error, got %v", err)
	}
}

func TestParseEnvConfig_MissingSubscriptionID(t *testing.T) {
	env := envFromMap(map[string]string{"CLUSTER_NAME": "c", "RESOURCE_GROUP": "rg"})
	_, err := parseEnvConfig(env)
	if err == nil || !strings.Contains(err.Error(), "SUBSCRIPTION_ID") {
		t.Errorf("expected SUBSCRIPTION_ID error, got %v", err)
	}
}

func TestParseEnvConfig_CustomThresholdAndWindow(t *testing.T) {
	env := envFromMap(map[string]string{
		"CLUSTER_NAME":        "c",
		"RESOURCE_GROUP":      "rg",
		"SUBSCRIPTION_ID":     "sub",
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
				"SUBSCRIPTION_ID":    "sub",
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
				"SUBSCRIPTION_ID":     "sub",
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
				"CLUSTER_NAME":    "c",
				"RESOURCE_GROUP":  "rg",
				"SUBSCRIPTION_ID": "sub",
				"DRY_RUN":         tc.v,
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
			pass, reason := evalGuard1(tc.failures, tc.threshold)
			if pass != tc.wantPass {
				t.Errorf("failures=%d threshold=%d: pass=%t want %t (%s)", tc.failures, tc.threshold, pass, tc.wantPass, reason)
			}
			if !pass && reason == "" {
				t.Errorf("non-pass result must have a reason")
			}
		})
	}
}

func TestEvalGuard2(t *testing.T) {
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
			pass, reason := evalGuard2(tc.state)
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

func TestEvalGuard3_AllHealthy(t *testing.T) {
	pools := []*armcs.AgentPool{
		mkPoolWithState("system", ptr(int32(2)), ptr(int32(2)), "Succeeded"),
		mkPool("userswft3", ptr(int32(4)), ptr(int32(4))),
	}
	pass, sysMin, sysState, reason := evalGuard3(pools)
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

func TestEvalGuard3_NonSystemPoolEmpty(t *testing.T) {
	pools := []*armcs.AgentPool{
		mkPool("system", ptr(int32(2)), ptr(int32(2))),
		mkPool("userswft3", ptr(int32(0)), ptr(int32(0))),
	}
	pass, _, _, reason := evalGuard3(pools)
	if pass {
		t.Fatal("expected fail when non-system pool has count=0")
	}
	if !strings.Contains(reason, "userswft3") {
		t.Errorf("reason should mention failing pool, got %q", reason)
	}
}

func TestEvalGuard3_NoSystemPool(t *testing.T) {
	pools := []*armcs.AgentPool{
		mkPool("userswft3", ptr(int32(4)), ptr(int32(4))),
	}
	pass, _, _, reason := evalGuard3(pools)
	if pass {
		t.Fatal("expected fail when no system pool")
	}
	if !strings.Contains(reason, "system pool") {
		t.Errorf("reason should mention missing system pool, got %q", reason)
	}
}

func TestEvalGuard3_SystemPoolWithZeroCount_OK(t *testing.T) {
	// System pool itself can have count=0 (that's the whole point of this script).
	pools := []*armcs.AgentPool{
		mkPool("system", ptr(int32(0)), ptr(int32(2))),
		mkPool("userswft3", ptr(int32(4)), ptr(int32(4))),
	}
	pass, sysMin, _, reason := evalGuard3(pools)
	if !pass {
		t.Fatalf("system pool with count=0 should not fail guard 4: %s", reason)
	}
	if sysMin != 2 {
		t.Errorf("systemMin=%d want 2", sysMin)
	}
}

func TestEvalGuard3_NilPoolsSkipped(t *testing.T) {
	pools := []*armcs.AgentPool{
		nil,
		mkPool("system", ptr(int32(2)), ptr(int32(2))),
		nil,
		mkPool("userswft3", ptr(int32(4)), ptr(int32(4))),
	}
	pass, _, _, reason := evalGuard3(pools)
	if !pass {
		t.Fatalf("nil pools should be skipped: %s", reason)
	}
}

func TestEvalGuard3_PoolMissingFieldsSkipped(t *testing.T) {
	pools := []*armcs.AgentPool{
		{Name: ptr("orphan")}, // no properties
		{Properties: &armcs.ManagedClusterAgentPoolProfileProperties{Count: ptr(int32(3))}}, // no name
		mkPool("system", ptr(int32(1)), ptr(int32(2))),
		mkPool("userswft3", ptr(int32(4)), ptr(int32(4))),
	}
	pass, _, _, reason := evalGuard3(pools)
	if !pass {
		t.Fatalf("malformed pool entries should be skipped: %s", reason)
	}
}

func TestEvalGuard3_SystemMissingMinCount_DefaultsZero(t *testing.T) {
	pools := []*armcs.AgentPool{
		mkPool("system", ptr(int32(1)), nil),
		mkPool("userswft3", ptr(int32(4)), ptr(int32(4))),
	}
	pass, sysMin, _, _ := evalGuard3(pools)
	if !pass || sysMin != 0 {
		t.Errorf("systemMin=%d pass=%t (want 0/true)", sysMin, pass)
	}
}

func TestEvalGuard3_EmptyList(t *testing.T) {
	pass, _, _, reason := evalGuard3(nil)
	if pass {
		t.Fatal("empty pool list must fail")
	}
	if reason == "" {
		t.Errorf("empty list fail must have a reason")
	}
}

func TestEvalGuard3_ExtractsSystemProvState(t *testing.T) {
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
			pass, _, gotState, _ := evalGuard3(pools)
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
	etag := "etag-value"
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
			ETag:                       &etag,
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
			AvailabilityZones:          []*string{ptr("1"), ptr("2")},
			NodeTaints:                 []*string{ptr("CriticalAddonsOnly=true:NoSchedule")},
			NodeLabels:                 map[string]*string{"aro-hcp.azure.com/role": ptr("system"), "existing-label": ptr("keep-label")},
			Tags: map[string]*string{
				"user-tag": ptr("keep-me"),
				"delegate-ip-allocation-for-nics-without-subnet": ptr("true"),
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
	if p.ETag != nil {
		t.Errorf("ETag not stripped")
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
	if live.Properties.ETag == nil || *live.Properties.ETag != "etag-value" {
		t.Errorf("live.ETag was mutated to %v", live.Properties.ETag)
	}
	if _, ok := live.Properties.Tags["aks-managed-foo"]; !ok {
		t.Errorf("live.Tags was mutated")
	}
}

func TestIsActiveClusterState(t *testing.T) {
	cases := []struct {
		state string
		want  bool
	}{
		{"Updating", true},
		{"Upgrading", true},
		{"Succeeded", false},
		{"Canceled", false},
		{"Failed", false},
		{"Creating", false},
		{"Deleting", false},
		{"", false},
		{"updating", false},
	}
	for _, tc := range cases {
		t.Run(tc.state, func(t *testing.T) {
			if got := isActiveClusterState(tc.state); got != tc.want {
				t.Errorf("isActiveClusterState(%q)=%t want %t", tc.state, got, tc.want)
			}
		})
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
	if len(p.AvailabilityZones) != 2 || *p.AvailabilityZones[0] != "1" || *p.AvailabilityZones[1] != "2" {
		t.Errorf("AvailabilityZones not inherited: %v", p.AvailabilityZones)
	}
	if p.MaxPods == nil || *p.MaxPods != 100 {
		t.Errorf("MaxPods not inherited: %v", p.MaxPods)
	}
	if p.NodeLabels["existing-label"] == nil || *p.NodeLabels["existing-label"] != "keep-label" {
		t.Errorf("existing label not inherited: %v", p.NodeLabels)
	}
	if p.Tags["delegate-ip-allocation-for-nics-without-subnet"] == nil || *p.Tags["delegate-ip-allocation-for-nics-without-subnet"] != "true" {
		t.Errorf("Swift tag not inherited: %v", p.Tags)
	}
	if p.Tags["purpose"] == nil || *p.Tags["purpose"] != "temp-system-aroslsre-924" {
		t.Errorf("temporary purpose tag missing: %v", p.Tags)
	}
	if _, ok := p.Tags["aks-managed-foo"]; ok {
		t.Errorf("AKS-managed tag should not be copied to systmp: %v", p.Tags)
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

func TestBuildSystmpAgentPool_DoesNotShareInheritedMapsOrSlices(t *testing.T) {
	live := mkLiveSystemPool()
	body, err := buildSystmpAgentPool(live, "1.35.4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	*live.Properties.AvailabilityZones[0] = "9"
	*live.Properties.NodeLabels["existing-label"] = "changed"
	*live.Properties.Tags["delegate-ip-allocation-for-nics-without-subnet"] = "false"
	if *body.Properties.AvailabilityZones[0] != "1" {
		t.Errorf("AvailabilityZones share state with live: %v", body.Properties.AvailabilityZones)
	}
	if *body.Properties.NodeLabels["existing-label"] != "keep-label" {
		t.Errorf("NodeLabels share state with live: %v", body.Properties.NodeLabels)
	}
	if *body.Properties.Tags["delegate-ip-allocation-for-nics-without-subnet"] != "true" {
		t.Errorf("Tags share state with live: %v", body.Properties.Tags)
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

// mkActivityEvent builds a synthetic activity-log event. The optional
// errorCode argument (defaults to nrpKVSErrorCode) is injected into
// `properties.statusMessage` as the inner ARM error body so the event
// matches the NRP-KVS signature required by countNRPFailures and
// nrpResourceIDs. Pass an explicit code to simulate other failure
// modes (quota, capacity, policy, ...).
func mkActivityEvent(status, op, resID string, errorCode ...string) map[string]any {
	code := nrpKVSErrorCode
	if len(errorCode) > 0 {
		code = errorCode[0]
	}
	statusMessage := ""
	if code != "" {
		statusMessage = fmt.Sprintf(
			`{"error":{"code":%q,"message":"An error occurred.","details":[]}}`,
			code,
		)
	}
	return map[string]any{
		"status":        map[string]string{"value": status},
		"operationName": map[string]string{"value": op},
		"resourceId":    resID,
		"properties":    map[string]string{"statusMessage": statusMessage},
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

func mustCountNRPFailures(t *testing.T, raw []byte, vmssPrefix string) int {
	t.Helper()
	n, err := countNRPFailures(raw, vmssPrefix)
	if err != nil {
		t.Fatalf("countNRPFailures: %v", err)
	}
	return n
}

func mustNRPResourceIDs(t *testing.T, raw []byte) []string {
	t.Helper()
	ids, err := nrpResourceIDs(raw)
	if err != nil {
		t.Fatalf("nrpResourceIDs: %v", err)
	}
	return ids
}

func TestCountNRPFailures_EmptyJSONArray(t *testing.T) {
	if n := mustCountNRPFailures(t, []byte("[]"), "aks-system-"); n != 0 {
		t.Errorf("got %d want 0", n)
	}
}

func TestCountNRPFailures_InvalidJSONReturnsError(t *testing.T) {
	if _, err := countNRPFailures([]byte("not json"), "aks-system-"); err == nil {
		t.Fatal("expected invalid JSON to return an error")
	}
	if _, err := countNRPFailures(nil, "aks-system-"); err == nil {
		t.Fatal("expected nil JSON to return an error")
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
	got := mustCountNRPFailures(t, mustMarshal(t, events), "aks-system-")
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
	got := mustCountNRPFailures(t, mustMarshal(t, events), "aks-system-")
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
	if got := mustCountNRPFailures(t, mustMarshal(t, events), "aks-system-"); got != 1 {
		t.Errorf("prefix filter: got %d want 1", got)
	}
	if got := mustCountNRPFailures(t, mustMarshal(t, events), ""); got != 2 {
		t.Errorf("empty prefix: got %d want 2", got)
	}
	if got := mustCountNRPFailures(t, mustMarshal(t, events), "aks-other-"); got != 0 {
		t.Errorf("non-matching prefix: got %d want 0", got)
	}
}

func TestCountNRPFailures_CaseInsensitiveOperationAndPrefix(t *testing.T) {
	events := []map[string]any{
		mkActivityEvent("Failed", "MICROSOFT.COMPUTE/VIRTUALMACHINESCALESETS/WRITE",
			"/SUBSCRIPTIONS/X/.../VIRTUALMACHINESCALESETS/AKS-SYSTEM-1"),
	}
	got := mustCountNRPFailures(t, mustMarshal(t, events), "aks-system-")
	if got != 1 {
		t.Errorf("case-insensitive match failed: got %d want 1", got)
	}
}

func TestCountNRPFailures_RequiresNRPKVSSignature(t *testing.T) {
	resID := "/subscriptions/x/.../virtualMachineScaleSets/aks-system-1"
	op := "Microsoft.Compute/virtualMachineScaleSets/write"
	events := []map[string]any{
		// NRP-KVS coded — counts
		mkActivityEvent("Failed", op, resID, nrpKVSErrorCode),
		// Quota / capacity / policy — must NOT count
		mkActivityEvent("Failed", op, resID, "OperationNotAllowed"),
		mkActivityEvent("Failed", op, resID, "AllocationFailed"),
		mkActivityEvent("Failed", op, resID, "RequestDisallowedByPolicy"),
		// Another NRP-KVS — counts (total = 2)
		mkActivityEvent("Failed", op, resID, nrpKVSErrorCode),
	}
	got := mustCountNRPFailures(t, mustMarshal(t, events), "aks-system-")
	if got != 2 {
		t.Errorf("got %d want 2 (only NRP-KVS signed failures count)", got)
	}
}

func TestCountNRPFailures_MissingPropertiesNotCounted(t *testing.T) {
	// Build an event with no `properties` block at all.
	events := []map[string]any{
		{
			"status":        map[string]string{"value": "Failed"},
			"operationName": map[string]string{"value": "Microsoft.Compute/virtualMachineScaleSets/write"},
			"resourceId":    "/subscriptions/x/.../virtualMachineScaleSets/aks-system-1",
		},
	}
	if got := mustCountNRPFailures(t, mustMarshal(t, events), "aks-system-"); got != 0 {
		t.Errorf("got %d want 0 (event without properties.statusMessage must not count)", got)
	}
}

func TestCountNRPFailures_MalformedStatusMessageNotCounted(t *testing.T) {
	events := []map[string]any{
		{
			"status":        map[string]string{"value": "Failed"},
			"operationName": map[string]string{"value": "Microsoft.Compute/virtualMachineScaleSets/write"},
			"resourceId":    "/subscriptions/x/.../virtualMachineScaleSets/aks-system-1",
			"properties":    map[string]string{"statusMessage": "not-valid-json"},
		},
		{
			"status":        map[string]string{"value": "Failed"},
			"operationName": map[string]string{"value": "Microsoft.Compute/virtualMachineScaleSets/write"},
			"resourceId":    "/subscriptions/x/.../virtualMachineScaleSets/aks-system-2",
			// Valid JSON but no error.code field.
			"properties": map[string]string{"statusMessage": `{"foo":"bar"}`},
		},
	}
	if got := mustCountNRPFailures(t, mustMarshal(t, events), "aks-system-"); got != 0 {
		t.Errorf("got %d want 0 (malformed/empty inner error body must not count)", got)
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
	got := mustNRPResourceIDs(t, mustMarshal(t, events))
	want := []string{"vmss-A", "vmss-B"}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestNRPResourceIDs_EmptyAndInvalid(t *testing.T) {
	if got := mustNRPResourceIDs(t, []byte("[]")); got != nil {
		t.Errorf("empty list got %v want nil", got)
	}
	if _, err := nrpResourceIDs([]byte("garbage")); err == nil {
		t.Fatal("expected invalid JSON to return an error")
	}
}

func TestNRPResourceIDs_RequiresNRPKVSSignature(t *testing.T) {
	op := "Microsoft.Compute/virtualMachineScaleSets/write"
	events := []map[string]any{
		// NRP-KVS coded — listed
		mkActivityEvent("Failed", op, "vmss-A", nrpKVSErrorCode),
		// Quota / capacity — must be filtered out even though same op
		mkActivityEvent("Failed", op, "vmss-B", "OperationNotAllowed"),
		mkActivityEvent("Failed", op, "vmss-C", "AllocationFailed"),
		// Another NRP-KVS on a third resource — listed
		mkActivityEvent("Failed", op, "vmss-D", nrpKVSErrorCode),
	}
	got := mustNRPResourceIDs(t, mustMarshal(t, events))
	want := []string{"vmss-A", "vmss-D"}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v (only NRP-KVS-signed failures listed)", got, want)
	}
}

func mkClusterWriteEvent(status, resourceID, timestamp, correlationID string) map[string]any {
	return map[string]any{
		"status":         map[string]string{"value": status},
		"operationName":  map[string]string{"value": "Microsoft.ContainerService/managedClusters/write"},
		"resourceId":     resourceID,
		"eventTimestamp": timestamp,
		"correlationId":  correlationID,
	}
}

func TestLatestClusterWriteStart_PicksNewestStartedEvent(t *testing.T) {
	clusterID := "/subscriptions/s/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/c"
	oldTime := "2026-05-25T10:00:00Z"
	newTime := "2026-05-26T10:00:00Z"
	events := []map[string]any{
		mkClusterWriteEvent("Started", clusterID, oldTime, "old"),
		mkClusterWriteEvent("Succeeded", clusterID, "2026-05-26T10:30:00Z", "terminal"),
		mkClusterWriteEvent("Started", clusterID, newTime, "new"),
		mkClusterWriteEvent("Started", "/subscriptions/s/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/other", "2026-05-27T10:00:00Z", "other"),
	}
	got, corr, err := latestClusterWriteStart(mustMarshal(t, events), clusterID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want, _ := time.Parse(time.RFC3339, newTime)
	if !got.Equal(want) || corr != "new" {
		t.Fatalf("got time=%s corr=%s, want %s/new", got.Format(time.RFC3339), corr, want.Format(time.RFC3339))
	}
}

func TestLatestClusterWriteStart_NoStartedEvent(t *testing.T) {
	clusterID := "/subscriptions/s/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/c"
	events := []map[string]any{
		mkClusterWriteEvent("Succeeded", clusterID, "2026-05-26T10:30:00Z", "terminal"),
	}
	if _, _, err := latestClusterWriteStart(mustMarshal(t, events), clusterID); err == nil {
		t.Fatal("expected error when no Started managedClusters/write event exists")
	}
}

func TestLatestClusterWriteStart_MalformedJSON(t *testing.T) {
	if _, _, err := latestClusterWriteStart([]byte("not-json"), "/subscriptions/s/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/c"); err == nil {
		t.Fatal("expected malformed activity-log JSON to return an error")
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
// evalGuard4  (system pool provisioningState == "Failed")
// =============================================================================

func TestEvalGuard4(t *testing.T) {
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
			pass, reason := evalGuard4(tc.state)
			if pass != tc.want {
				t.Errorf("state=%q: pass=%t want %t (%s)", tc.state, pass, tc.want, reason)
			}
			if !pass && reason == "" {
				t.Errorf("non-pass result must have a reason")
			}
		})
	}
}
