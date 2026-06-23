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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	armcompute "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	computefake "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6/fake"
	armcs "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"
	armnetwork "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
	networkfake "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6/fake"
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
		"NODEPOOL_TAG":    "user",
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
				"SUBSCRIPTION_ID": "sub", "NODEPOOL_TAG": "user",
				"DRY_RUN": tc.v,
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
// evalClusterState
// =============================================================================

func TestEvalClusterState(t *testing.T) {
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
			pass, reason := evalClusterState(tc.state)
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

func TestEvalNonTargetPoolsHealthy_AllHealthy(t *testing.T) {
	pools := []*armcs.AgentPool{
		mkPoolWithState("system", ptr(int32(2)), ptr(int32(2)), "Succeeded"),
		mkPool("userswft3", ptr(int32(4)), ptr(int32(4))),
	}
	pass, reason := evalNonTargetPoolsHealthy(pools)
	if !pass {
		t.Fatalf("expected pass, got fail: %s", reason)
	}
}

func TestEvalNonTargetPoolsHealthy_PoolEmpty(t *testing.T) {
	pools := []*armcs.AgentPool{
		mkPool("system", ptr(int32(2)), ptr(int32(2))),
		mkPool("userswft3", ptr(int32(0)), ptr(int32(0))),
	}
	pass, reason := evalNonTargetPoolsHealthy(pools)
	if pass {
		t.Fatal("expected fail when a non-target pool has count=0")
	}
	if !strings.Contains(reason, "userswft3") {
		t.Errorf("reason should mention failing pool, got %q", reason)
	}
}

func TestEvalNonTargetPoolsHealthy_WedgedPoolWithZeroCount_OK(t *testing.T) {
	// A pool that is itself wedge-compatible (Failed/Updating/...) is
	// the target we want to remediate; its count=0 must not fail this
	// sibling-health check.
	pools := []*armcs.AgentPool{
		mkPoolWithState("system", ptr(int32(0)), ptr(int32(2)), "Failed"),
		mkPool("userswft3", ptr(int32(4)), ptr(int32(4))),
	}
	pass, reason := evalNonTargetPoolsHealthy(pools)
	if !pass {
		t.Fatalf("wedged pool with count=0 should not fail sibling-health check: %s", reason)
	}
}

func TestEvalNonTargetPoolsHealthy_HealthyPoolWithZeroCount_Fails(t *testing.T) {
	// A pool in provisioningState=Succeeded but with count=0 is not a
	// wedge and indicates the cluster is not in a safe-to-act state.
	pools := []*armcs.AgentPool{
		mkPoolWithState("system", ptr(int32(0)), ptr(int32(2)), "Succeeded"),
		mkPool("userswft3", ptr(int32(4)), ptr(int32(4))),
	}
	pass, reason := evalNonTargetPoolsHealthy(pools)
	if pass {
		t.Fatal("expected fail when a Succeeded pool has count=0")
	}
	if !strings.Contains(reason, "system") {
		t.Errorf("reason should mention failing pool, got %q", reason)
	}
}

func TestEvalNonTargetPoolsHealthy_NilPoolsSkipped(t *testing.T) {
	pools := []*armcs.AgentPool{
		nil,
		mkPool("system", ptr(int32(2)), ptr(int32(2))),
		nil,
		mkPool("userswft3", ptr(int32(4)), ptr(int32(4))),
	}
	pass, reason := evalNonTargetPoolsHealthy(pools)
	if !pass {
		t.Fatalf("nil pools should be skipped: %s", reason)
	}
}

func TestEvalNonTargetPoolsHealthy_PoolMissingFieldsSkipped(t *testing.T) {
	pools := []*armcs.AgentPool{
		{Name: ptr("orphan")}, // no properties
		{Properties: &armcs.ManagedClusterAgentPoolProfileProperties{Count: ptr(int32(3))}}, // no name
		mkPool("system", ptr(int32(1)), ptr(int32(2))),
		mkPool("userswft3", ptr(int32(4)), ptr(int32(4))),
	}
	pass, reason := evalNonTargetPoolsHealthy(pools)
	if !pass {
		t.Fatalf("malformed pool entries should be skipped: %s", reason)
	}
}

func TestEvalNonTargetPoolsHealthy_EmptyList(t *testing.T) {
	pass, _ := evalNonTargetPoolsHealthy(nil)
	if !pass {
		t.Fatal("empty pool list trivially passes (no pool has count=0)")
	}
}

func TestEvalNonTargetPoolsHealthy_TempPoolSkipped(t *testing.T) {
	// A temp pool created by a previous remediation run (purpose tag) must
	// be skipped by this check; otherwise its count=0 between
	// recreate+drain phases would block a re-entrant invocation.
	temp := mkPool(tempPoolName("system"), ptr(int32(0)), nil)
	temp.Properties.Tags = map[string]*string{tempPoolPurposeTag: ptr(tempPoolPurposeValue)}
	pools := []*armcs.AgentPool{
		mkPool("system", ptr(int32(2)), ptr(int32(2))),
		temp,
		mkPool("userswft3", ptr(int32(4)), ptr(int32(4))),
	}
	pass, reason := evalNonTargetPoolsHealthy(pools)
	if !pass {
		t.Fatalf("temp pool with count=0 should be skipped: %s", reason)
	}
}

// =============================================================================
// guardDecision
// =============================================================================

func TestGuardDecision(t *testing.T) {
	cases := []struct {
		name        string
		guard       string
		pass        bool
		reason      string
		skipGuards  bool
		wantProceed bool
		wantMsg     string
	}{
		{
			name:        "pass_ignores_skip_guards",
			guard:       "cluster state",
			pass:        true,
			reason:      "",
			skipGuards:  false,
			wantProceed: true,
			wantMsg:     "cluster state PASS",
		},
		{
			name:        "pass_with_skip_guards_still_pass",
			guard:       "cluster safety",
			pass:        true,
			reason:      "",
			skipGuards:  true,
			wantProceed: true,
			wantMsg:     "cluster safety PASS",
		},
		{
			name:        "fail_without_override_halts",
			guard:       "cluster state",
			pass:        false,
			reason:      "provisioningState=\"Creating\" rejected",
			skipGuards:  false,
			wantProceed: false,
			wantMsg:     "provisioningState=\"Creating\" rejected",
		},
		{
			name:        "fail_with_override_proceeds",
			guard:       "cluster safety",
			pass:        false,
			reason:      "non-target pool unhealthy",
			skipGuards:  true,
			wantProceed: true,
			wantMsg:     "SKIP_GUARDS=true — overriding cluster safety failure: non-target pool unhealthy",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			proceed, msg := guardDecision(tc.guard, tc.pass, tc.reason, tc.skipGuards)
			if proceed != tc.wantProceed {
				t.Errorf("proceed = %t, want %t", proceed, tc.wantProceed)
			}
			if msg != tc.wantMsg {
				t.Errorf("msg = %q, want %q", msg, tc.wantMsg)
			}
		})
	}
}

// =============================================================================
// agentPoolForCreate
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

func TestAgentPoolForCreate_NilInput(t *testing.T) {
	if _, err := agentPoolForCreate(nil, "1.35.4"); err == nil {
		t.Fatal("expected error for nil input")
	}
}

func TestAgentPoolForCreate_StripsTopLevelReadOnly(t *testing.T) {
	live := mkLiveSystemPool()
	out, err := agentPoolForCreate(live, "1.35.4")
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

func TestAgentPoolForCreate_StripsPropertyReadOnly(t *testing.T) {
	live := mkLiveSystemPool()
	out, _ := agentPoolForCreate(live, "1.35.4")
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

func TestAgentPoolForCreate_PreservesWriteableFields(t *testing.T) {
	live := mkLiveSystemPool()
	out, _ := agentPoolForCreate(live, "1.35.4")
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

func TestAgentPoolForCreate_OverridesOrchestratorVersion(t *testing.T) {
	live := mkLiveSystemPool()
	out, _ := agentPoolForCreate(live, "1.36.2")
	if out.Properties.OrchestratorVersion == nil || *out.Properties.OrchestratorVersion != "1.36.2" {
		t.Errorf("OrchestratorVersion=%v want 1.36.2", out.Properties.OrchestratorVersion)
	}
}

func TestAgentPoolForCreate_StripsAKSManagedTags(t *testing.T) {
	live := mkLiveSystemPool()
	out, _ := agentPoolForCreate(live, "1.35.4")
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

func TestAgentPoolForCreate_DoesNotMutateInput(t *testing.T) {
	live := mkLiveSystemPool()
	beforeRaw, _ := json.Marshal(live)

	_, err := agentPoolForCreate(live, "1.99.99")
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

func TestAgentPoolForCreate_NilTagsOK(t *testing.T) {
	live := mkLiveSystemPool()
	live.Properties.Tags = nil
	out, err := agentPoolForCreate(live, "1.35.4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Properties.Tags) != 0 {
		t.Errorf("expected nil or empty tags, got %v", out.Properties.Tags)
	}
}

func TestAgentPoolForCreate_NilProperties(t *testing.T) {
	live := &armcs.AgentPool{Name: ptr("system")}
	_, err := agentPoolForCreate(live, "1.35.4")
	if err == nil {
		t.Fatal("expected error when Properties is nil")
	}
}

// =============================================================================
// tempPoolName
// =============================================================================

func TestTempPoolName_Format(t *testing.T) {
	cases := []string{
		"a", "user", "system", "userswft3a", "userswft3xyz", "abcdefghijkl",
	}
	for _, src := range cases {
		t.Run(src, func(t *testing.T) {
			got := tempPoolName(src)
			if len(got) < 1 || len(got) > 12 {
				t.Errorf("tempPoolName(%q) = %q (len=%d); must be 1..12", src, got, len(got))
			}
			if got[0] < 'a' || got[0] > 'z' {
				t.Errorf("tempPoolName(%q) = %q; must start with a lowercase letter", src, got)
			}
			for i, r := range got {
				ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
				if !ok {
					t.Errorf("tempPoolName(%q)[%d]=%q not lowercase alphanumeric (full=%q)", src, i, r, got)
				}
			}
		})
	}
}

func TestTempPoolName_NoCollisionsOnSharedPrefix(t *testing.T) {
	// Pairs of source pool names that share a long prefix; the
	// previous truncation-only algorithm produced collisions for
	// every pair below. The hash-based scheme must keep them apart.
	pairs := [][2]string{
		{"userswft3a", "userswft3b"},
		{"userswft3a", "userswft3xyz"},
		{"systemnode1", "systemnode12"},
		{"systempool1", "systempool2"},
	}
	for _, p := range pairs {
		t.Run(p[0]+"_vs_"+p[1], func(t *testing.T) {
			a, b := tempPoolName(p[0]), tempPoolName(p[1])
			if a == b {
				t.Errorf("tempPoolName collision: %q and %q both -> %q", p[0], p[1], a)
			}
		})
	}
}

func TestTempPoolName_Deterministic(t *testing.T) {
	for _, src := range []string{"system", "userswft3a", "abcdefghijkl"} {
		first := tempPoolName(src)
		second := tempPoolName(src)
		if first != second {
			t.Errorf("tempPoolName(%q) is non-deterministic: %q vs %q", src, first, second)
		}
	}
}

// =============================================================================
// buildTempAgentPool
// =============================================================================

func TestBuildTempAgentPool_ValidInputs(t *testing.T) {
	live := mkLiveSystemPool()
	body, err := buildTempAgentPool(live, "1.35.4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	p := body.Properties
	if p.VMSize == nil || *p.VMSize != "Standard_E8ds_v5" {
		t.Errorf("temp VMSize should match live: %v", p.VMSize)
	}
	if p.Count == nil || *p.Count != 2 {
		t.Errorf("temp Count should match live Count=2, got %v", p.Count)
	}
	if p.OrchestratorVersion == nil || *p.OrchestratorVersion != "1.35.4" {
		t.Errorf("OrchestratorVersion: %v", p.OrchestratorVersion)
	}
	// Mode must be inherited from the live snapshot — not hard-coded.
	// AKS requires at least one System-mode pool; a system source must
	// produce a System temp, and a user source must produce a User temp.
	if p.Mode == nil || *p.Mode != armcs.AgentPoolModeSystem {
		t.Errorf("Mode should be inherited from live (System), got %v", p.Mode)
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
	if p.Tags[tempPoolPurposeTag] == nil || *p.Tags[tempPoolPurposeTag] != tempPoolPurposeValue {
		t.Errorf("temporary purpose tag missing: %v", p.Tags)
	}
	if _, ok := p.Tags["aks-managed-foo"]; ok {
		t.Errorf("AKS-managed tag should not be copied to temp pool: %v", p.Tags)
	}
}

func TestBuildTempAgentPool_InheritsUserMode(t *testing.T) {
	live := mkLiveSystemPool()
	user := armcs.AgentPoolModeUser
	live.Properties.Mode = &user
	body, err := buildTempAgentPool(live, "1.35.4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if body.Properties.Mode == nil || *body.Properties.Mode != armcs.AgentPoolModeUser {
		t.Errorf("Mode should be inherited from live (User), got %v", body.Properties.Mode)
	}
}

func TestBuildTempAgentPool_NilLive(t *testing.T) {
	if _, err := buildTempAgentPool(nil, "1.35.4"); err == nil {
		t.Fatal("expected error for nil live")
	}
}

func TestBuildTempAgentPool_MissingLiveID(t *testing.T) {
	live := mkLiveSystemPool()
	live.ID = nil
	if _, err := buildTempAgentPool(live, "1.35.4"); err == nil {
		t.Fatal("expected error when live.ID is nil")
	}
	live.ID = ptr("")
	if _, err := buildTempAgentPool(live, "1.35.4"); err == nil {
		t.Fatal("expected error when live.ID is empty")
	}
}

func TestBuildTempAgentPool_RecordsSourceTag(t *testing.T) {
	// The source tag must record the full Azure resource ID of the
	// source pool (not just the short pool name) so a leftover temp
	// pool can always be matched back to a unique source — important
	// when multiple clusters in the same subscription share pool names
	// like "system".
	live := mkLiveSystemPool()
	wantID := *live.ID
	body, err := buildTempAgentPool(live, "1.35.4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v := body.Properties.Tags[tempPoolSourceTag]; v == nil || *v != wantID {
		t.Errorf("source tag = %v, want %q", v, wantID)
	}
}

// =============================================================================
// adopt-leftover helpers: poolNameFromARMID, tempPoolSourceID
// =============================================================================

func TestPoolNameFromARMID_Valid(t *testing.T) {
	cases := map[string]string{
		"/subscriptions/x/resourceGroups/y/providers/Microsoft.ContainerService/managedClusters/c/agentPools/system": "system",
		"/subscriptions/x/resourceGroups/y/providers/Microsoft.ContainerService/managedClusters/c/agentPools/user01": "user01",
		// LastIndex of /agentPools/ correctly skips earlier segments
		"/subscriptions/x/resourceGroups/agentPools/providers/Microsoft.ContainerService/managedClusters/c/agentPools/sys": "sys",
	}
	for id, want := range cases {
		got, err := poolNameFromARMID(id)
		if err != nil {
			t.Errorf("poolNameFromARMID(%q) unexpected error: %v", id, err)
			continue
		}
		if got != want {
			t.Errorf("poolNameFromARMID(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestPoolNameFromARMID_Invalid(t *testing.T) {
	cases := []string{
		"",                           // empty
		"/some/random/path",          // missing segment
		"foo/agentPools/",            // empty trailing segment
		"foo/agentPools/bar/baz",     // trailing path after pool name
		"foo/agentPools/bar/baz/qux", // multiple trailing segments
	}
	for _, id := range cases {
		if got, err := poolNameFromARMID(id); err == nil {
			t.Errorf("poolNameFromARMID(%q) = %q, expected error", id, got)
		}
	}
}

func TestTempPoolSourceID(t *testing.T) {
	if got := tempPoolSourceID(nil); got != "" {
		t.Errorf("nil pool: got %q, want \"\"", got)
	}
	if got := tempPoolSourceID(&armcs.AgentPool{}); got != "" {
		t.Errorf("pool with nil properties: got %q, want \"\"", got)
	}
	if got := tempPoolSourceID(&armcs.AgentPool{Properties: &armcs.ManagedClusterAgentPoolProfileProperties{}}); got != "" {
		t.Errorf("pool with nil tags: got %q, want \"\"", got)
	}
	noTag := &armcs.AgentPool{Properties: &armcs.ManagedClusterAgentPoolProfileProperties{
		Tags: map[string]*string{"unrelated": ptr("v")},
	}}
	if got := tempPoolSourceID(noTag); got != "" {
		t.Errorf("pool without source tag: got %q, want \"\"", got)
	}
	nilValue := &armcs.AgentPool{Properties: &armcs.ManagedClusterAgentPoolProfileProperties{
		Tags: map[string]*string{tempPoolSourceTag: nil},
	}}
	if got := tempPoolSourceID(nilValue); got != "" {
		t.Errorf("pool with nil tag value: got %q, want \"\"", got)
	}
	want := "/subscriptions/x/resourceGroups/y/providers/Microsoft.ContainerService/managedClusters/c/agentPools/system"
	withTag := &armcs.AgentPool{Properties: &armcs.ManagedClusterAgentPoolProfileProperties{
		Tags: map[string]*string{tempPoolSourceTag: ptr(want)},
	}}
	if got := tempPoolSourceID(withTag); got != want {
		t.Errorf("pool with source tag: got %q, want %q", got, want)
	}
}

func mkLeftoverTempPool(name, source, role string) *armcs.AgentPool {
	return &armcs.AgentPool{
		Name: ptr(name),
		Properties: &armcs.ManagedClusterAgentPoolProfileProperties{
			NodeLabels: map[string]*string{
				nodePoolRoleLabel: ptr(role),
			},
			Tags: map[string]*string{
				tempPoolPurposeTag: ptr(tempPoolPurposeValue),
				tempPoolSourceTag:  ptr(source),
			},
		},
	}
}

func TestTargetFromLeftoverTempPool(t *testing.T) {
	source := "/subscriptions/x/resourceGroups/y/providers/Microsoft.ContainerService/managedClusters/c/agentPools/userswft3"
	temp := mkLeftoverTempPool(tempPoolName("userswft3"), source, "user")

	target, ok, err := targetFromLeftoverTempPool(temp, "user")
	if err != nil {
		t.Fatalf("targetFromLeftoverTempPool() error = %v", err)
	}
	if !ok {
		t.Fatal("targetFromLeftoverTempPool() skipped matching temp pool")
	}
	if target.name != "userswft3" || target.vmssPrefix != poolVMSSPrefix("userswft3") {
		t.Fatalf("target = %#v", target)
	}
}

func TestTargetFromLeftoverTempPoolIgnoresOtherRoles(t *testing.T) {
	source := "/subscriptions/x/resourceGroups/y/providers/Microsoft.ContainerService/managedClusters/c/agentPools/system"
	temp := mkLeftoverTempPool(tempPoolName("system"), source, "system")

	target, ok, err := targetFromLeftoverTempPool(temp, "user")
	if err != nil {
		t.Fatalf("targetFromLeftoverTempPool() error = %v", err)
	}
	if ok || target != nil {
		t.Fatalf("targetFromLeftoverTempPool() = %#v, %t; want skip", target, ok)
	}
}

func TestTargetFromLeftoverTempPoolMalformedForTargetRoleFailsClosed(t *testing.T) {
	cases := []struct {
		name string
		pool *armcs.AgentPool
	}{
		{
			name: "missing source tag",
			pool: &armcs.AgentPool{
				Name: ptr(tempPoolName("userswft3")),
				Properties: &armcs.ManagedClusterAgentPoolProfileProperties{
					NodeLabels: map[string]*string{nodePoolRoleLabel: ptr("user")},
					Tags:       map[string]*string{tempPoolPurposeTag: ptr(tempPoolPurposeValue)},
				},
			},
		},
		{
			name: "malformed source tag",
			pool: mkLeftoverTempPool(tempPoolName("userswft3"), "not-an-agent-pool-id", "user"),
		},
		{
			name: "name does not match deterministic source name",
			pool: mkLeftoverTempPool(tempPoolName("otherpool"), "/subscriptions/x/resourceGroups/y/providers/Microsoft.ContainerService/managedClusters/c/agentPools/userswft3", "user"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := targetFromLeftoverTempPool(tc.pool, "user"); err == nil {
				t.Fatal("expected fail-closed error")
			}
		})
	}
}

func TestBuildTempAgentPool_NilProperties(t *testing.T) {
	if _, err := buildTempAgentPool(&armcs.AgentPool{}, "1.35.4"); err == nil {
		t.Fatal("expected error for nil properties")
	}
}

func TestBuildTempAgentPool_MissingVMSize(t *testing.T) {
	live := mkLiveSystemPool()
	live.Properties.VMSize = nil
	if _, err := buildTempAgentPool(live, "1.35.4"); err == nil {
		t.Fatal("expected error for missing VMSize")
	}
	live.Properties.VMSize = ptr("")
	if _, err := buildTempAgentPool(live, "1.35.4"); err == nil {
		t.Fatal("expected error for empty VMSize")
	}
}

func TestBuildTempAgentPool_MissingCPVersion(t *testing.T) {
	live := mkLiveSystemPool()
	if _, err := buildTempAgentPool(live, ""); err == nil {
		t.Fatal("expected error for empty cpVersion")
	}
}

func TestBuildTempAgentPool_CountFallbacks(t *testing.T) {
	cases := []struct {
		name      string
		count     *int32
		minCount  *int32
		wantCount int32
	}{
		{name: "count_wins", count: ptr(int32(3)), minCount: ptr(int32(5)), wantCount: 3},
		{name: "missing_count_uses_min_count", count: nil, minCount: ptr(int32(4)), wantCount: 4},
		{name: "zero_count_uses_min_count", count: ptr(int32(0)), minCount: ptr(int32(2)), wantCount: 2},
		{name: "missing_count_and_min_count_uses_one", count: nil, minCount: nil, wantCount: 1},
		{name: "zero_count_and_zero_min_count_uses_one", count: ptr(int32(0)), minCount: ptr(int32(0)), wantCount: 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			live := mkLiveSystemPool()
			live.Properties.Count = tc.count
			live.Properties.MinCount = tc.minCount
			body, err := buildTempAgentPool(live, "1.35.4")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := *body.Properties.Count; got != tc.wantCount {
				t.Fatalf("Count=%d want %d", got, tc.wantCount)
			}
		})
	}
}

func TestTempPoolReadyTimeout(t *testing.T) {
	if got := tempPoolReadyTimeout(1); got != tempReadyTOMin*time.Minute {
		t.Errorf("single-node temp timeout=%s want %dm", got, tempReadyTOMin)
	}
	for _, wantReady := range []int{2, 3, 4} {
		if got := tempPoolReadyTimeout(wantReady); got != poolReadyTOMin*time.Minute {
			t.Errorf("multi-node temp timeout for wantReady=%d got %s want %dm", wantReady, got, poolReadyTOMin)
		}
	}
}

func TestBuildTempAgentPool_NoPodSubnet(t *testing.T) {
	live := mkLiveSystemPool()
	live.Properties.PodSubnetID = nil
	body, err := buildTempAgentPool(live, "1.35.4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if body.Properties.PodSubnetID != nil {
		t.Errorf("PodSubnetID should remain nil when live has none")
	}
}

func TestBuildTempAgentPool_DoesNotShareTaintPointer(t *testing.T) {
	// Mutating the live snapshot's taints must not affect the temp body.
	live := mkLiveSystemPool()
	body, _ := buildTempAgentPool(live, "1.35.4")
	*live.Properties.NodeTaints[0] = "hacked"
	if *body.Properties.NodeTaints[0] != "CriticalAddonsOnly=true:NoSchedule" {
		t.Errorf("temp NodeTaints share state with live: %v", body.Properties.NodeTaints)
	}
}

func TestBuildTempAgentPool_DoesNotShareInheritedMapsOrSlices(t *testing.T) {
	live := mkLiveSystemPool()
	body, err := buildTempAgentPool(live, "1.35.4")
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

func TestBuildTempAgentPool_MissingOSDiskSizeGB(t *testing.T) {
	live := mkLiveSystemPool()
	live.Properties.OSDiskSizeGB = nil
	if _, err := buildTempAgentPool(live, "1.35.4"); err == nil {
		t.Fatal("expected error for missing OSDiskSizeGB")
	}
	zero := int32(0)
	live.Properties.OSDiskSizeGB = &zero
	if _, err := buildTempAgentPool(live, "1.35.4"); err == nil {
		t.Fatal("expected error for OSDiskSizeGB == 0")
	}
}

func TestBuildTempAgentPool_InheritsDiskSizeAndOSType(t *testing.T) {
	live := mkLiveSystemPool()
	custom := int32(64)
	live.Properties.OSDiskSizeGB = &custom
	body, err := buildTempAgentPool(live, "1.35.4")
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

func TestCountNRPFailures_FiltersByVMSSWriteOperation(t *testing.T) {
	events := []map[string]any{
		mkActivityEvent("Failed", "Microsoft.Network/networkInterfaces/write",
			"/subscriptions/x/.../networkInterfaces/foo"),
		mkActivityEvent("Failed", "Microsoft.Compute/virtualMachineScaleSets/delete",
			"/subscriptions/x/.../virtualMachineScaleSets/aks-system-1"),
		mkActivityEvent("Failed", "Microsoft.Compute/virtualMachineScaleSets/extensions/write",
			"/subscriptions/x/.../virtualMachineScaleSets/aks-system-1/extensions/foo"),
		mkActivityEvent("Failed", "Microsoft.Compute/virtualMachineScaleSets/write",
			"/subscriptions/x/.../virtualMachineScaleSets/aks-system-1"),
	}
	got := mustCountNRPFailures(t, mustMarshal(t, events), "aks-system-")
	if got != 1 {
		t.Errorf("got %d want 1 (only exact VMSS write failed)", got)
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
		mkActivityEvent("Failed", "Microsoft.Compute/virtualMachineScaleSets/delete", "vmss-E"),
		mkActivityEvent("Failed", "Microsoft.Compute/virtualMachineScaleSets/extensions/write", "vmss-F"),
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
	want, err := time.Parse(time.RFC3339, newTime)
	if err != nil {
		t.Fatalf("parse want time: %v", err)
	}
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

func TestIsNodeSchedulableReady(t *testing.T) {
	ready := mkNode("ready", corev1.NodeCondition{Type: corev1.NodeReady, Status: corev1.ConditionTrue})
	cordoned := ready.DeepCopy()
	cordoned.Spec.Unschedulable = true
	deleting := ready.DeepCopy()
	deletionTime := metav1.Now()
	deleting.DeletionTimestamp = &deletionTime
	notReady := mkNode("not-ready", corev1.NodeCondition{Type: corev1.NodeReady, Status: corev1.ConditionFalse})

	cases := []struct {
		name string
		node *corev1.Node
		want bool
	}{
		{"nil", nil, false},
		{"ready_schedulable", ready, true},
		{"ready_but_cordoned", cordoned, false},
		{"ready_but_deleting", deleting, false},
		{"not_ready", notReady, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isNodeSchedulableReady(tc.node); got != tc.want {
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

func TestIsActivityLogAuthorizationError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain_error", errors.New("boom"), false},
		{"authorization_failed", &azcore.ResponseError{StatusCode: http.StatusForbidden, ErrorCode: "AuthorizationFailed"}, true},
		{"linked_authorization_failed", &azcore.ResponseError{StatusCode: http.StatusForbidden, ErrorCode: "LinkedAuthorizationFailed"}, true},
		{"wrapped_authorization_failed", fmt.Errorf("activity log: %w", &azcore.ResponseError{StatusCode: http.StatusForbidden, ErrorCode: "AuthorizationFailed"}), true},
		{"forbidden_policy", &azcore.ResponseError{StatusCode: http.StatusForbidden, ErrorCode: "RequestDisallowedByPolicy"}, false},
		{"unauthorized", &azcore.ResponseError{StatusCode: http.StatusUnauthorized, ErrorCode: "AuthorizationFailed"}, false},
		{"too_many_requests", &azcore.ResponseError{StatusCode: http.StatusTooManyRequests, ErrorCode: "AuthorizationFailed"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isActivityLogAuthorizationError(tc.err); got != tc.want {
				t.Errorf("got %t want %t", got, tc.want)
			}
		})
	}
}

// =============================================================================
// evalPoolWedge  (per-pool provisioningState == "Failed")
// =============================================================================

func TestEvalPoolWedge(t *testing.T) {
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
			pass, reason := evalPoolWedge(tc.state)
			if pass != tc.want {
				t.Errorf("state=%q: pass=%t want %t (%s)", tc.state, pass, tc.want, reason)
			}
			if !pass && reason == "" {
				t.Errorf("non-pass result must have a reason")
			}
		})
	}
}

// =============================================================================
// runWith orchestration integration tests
// =============================================================================

type mockOrchestrator struct {
	calls       []string
	detectCount int

	ensureClusterFn          func(ctx context.Context) (armcs.ManagedCluster, bool, error)
	bootstrapKubeFn          func(ctx context.Context, mc armcs.ManagedCluster) error
	detectFn                 func(ctx context.Context, n int) (bool, string, error)
	adoptLeftoverTempPoolsFn func(ctx context.Context) error
	adoptLeftoverTempPoolFn  func(ctx context.Context, target nodePoolTarget) error
	snapshotSystemFn         func(ctx context.Context) (*armcs.AgentPool, error)
	maybeAbortLROFn          func(ctx context.Context) (bool, error)
	addTempPoolFn            func(ctx context.Context, target nodePoolTarget, live *armcs.AgentPool) error
	drainPoolFn              func(ctx context.Context, pool string, timeout time.Duration) error
	deletePoolFn             func(ctx context.Context, pool string) error
	recreateSystemFn         func(ctx context.Context, live *armcs.AgentPool) error
	reconcileTagPutFn        func(ctx context.Context) error
}

func (m *mockOrchestrator) record(name string) { m.calls = append(m.calls, name) }

func (m *mockOrchestrator) ensureCluster(ctx context.Context) (armcs.ManagedCluster, bool, error) {
	m.record("ensureCluster")
	if m.ensureClusterFn != nil {
		return m.ensureClusterFn(ctx)
	}
	return armcs.ManagedCluster{}, true, nil
}

func (m *mockOrchestrator) bootstrapKube(ctx context.Context, mc armcs.ManagedCluster) error {
	m.record("bootstrapKube")
	if m.bootstrapKubeFn != nil {
		return m.bootstrapKubeFn(ctx, mc)
	}
	return nil
}

func (m *mockOrchestrator) detect(ctx context.Context) ([]nodePoolTarget, string, error) {
	m.detectCount++
	m.record(fmt.Sprintf("detect:%d", m.detectCount))
	if m.detectFn != nil {
		pass, reason, err := m.detectFn(ctx, m.detectCount)
		if err != nil {
			return nil, reason, err
		}
		if !pass {
			return nil, reason, err
		}
	}
	return []nodePoolTarget{{name: "system", vmssPrefix: poolVMSSPrefix("system"), emptyIPConfig: true}}, "", nil
}

func (m *mockOrchestrator) dumpPreflight(ctx context.Context) error {
	m.record("dumpPreflight")
	return nil
}

func (m *mockOrchestrator) dumpPostflight(ctx context.Context) error {
	m.record("dumpPostflight")
	return nil
}

func (m *mockOrchestrator) adoptLeftoverTempPools(ctx context.Context) error {
	if m.adoptLeftoverTempPoolsFn == nil {
		return nil
	}
	m.record("adoptLeftoverTempPools")
	return m.adoptLeftoverTempPoolsFn(ctx)
}

func (m *mockOrchestrator) adoptLeftoverTempPool(ctx context.Context, target nodePoolTarget) error {
	m.record("adoptLeftoverTempPool:" + target.name)
	if m.adoptLeftoverTempPoolFn != nil {
		return m.adoptLeftoverTempPoolFn(ctx, target)
	}
	return nil
}

func (m *mockOrchestrator) snapshotPool(ctx context.Context, poolName string) (*armcs.AgentPool, error) {
	if poolName == "system" {
		m.record("snapshotSystem")
	} else {
		m.record("snapshotPool:" + poolName)
	}
	if m.snapshotSystemFn != nil {
		return m.snapshotSystemFn(ctx)
	}
	return &armcs.AgentPool{}, nil
}

func (m *mockOrchestrator) maybeAbortLRO(ctx context.Context) (bool, error) {
	m.record("maybeAbortLRO")
	if m.maybeAbortLROFn != nil {
		return m.maybeAbortLROFn(ctx)
	}
	return true, nil
}

func (m *mockOrchestrator) addTempPool(ctx context.Context, target nodePoolTarget, live *armcs.AgentPool) error {
	m.record("addTempPool:" + target.name)
	if m.addTempPoolFn != nil {
		return m.addTempPoolFn(ctx, target, live)
	}
	return nil
}

func (m *mockOrchestrator) drainPool(ctx context.Context, pool string, timeout time.Duration) error {
	m.record("drainPool:" + pool)
	if m.drainPoolFn != nil {
		return m.drainPoolFn(ctx, pool, timeout)
	}
	return nil
}

func (m *mockOrchestrator) deletePool(ctx context.Context, pool string) error {
	m.record("deletePool:" + pool)
	if m.deletePoolFn != nil {
		return m.deletePoolFn(ctx, pool)
	}
	return nil
}

func (m *mockOrchestrator) recreatePool(ctx context.Context, poolName string, live *armcs.AgentPool) error {
	if poolName == "system" {
		m.record("recreateSystem")
	} else {
		m.record("recreatePool:" + poolName)
	}
	if m.recreateSystemFn != nil {
		return m.recreateSystemFn(ctx, live)
	}
	return nil
}

func (m *mockOrchestrator) reconcileTagPut(ctx context.Context) error {
	m.record("reconcileTagPut")
	if m.reconcileTagPutFn != nil {
		return m.reconcileTagPutFn(ctx)
	}
	return nil
}

func TestRunWith(t *testing.T) {
	dummyErr := errors.New("boom")
	tmpSystem := tempPoolName("system")

	fullHappyPath := []string{
		"ensureCluster", "dumpPreflight", "detect:1",
		"bootstrapKube", "dumpPreflight",
		"snapshotSystem", "maybeAbortLRO", "detect:2", "adoptLeftoverTempPool:system", "snapshotSystem",
		"addTempPool:system",
		"drainPool:system", "deletePool:system",
		"recreateSystem",
		"drainPool:" + tmpSystem, "deletePool:" + tmpSystem,
		"reconcileTagPut", "dumpPostflight",
	}

	cases := []struct {
		name      string
		cfg       *config
		setup     func(m *mockOrchestrator)
		wantErr   string
		wantCalls []string
	}{
		{
			name: "greenfield_cluster_not_found",
			cfg:  &config{clusterName: "c", resourceGroup: "rg", subscriptionID: "sub", cpVersion: "1.30.0"},
			setup: func(m *mockOrchestrator) {
				m.ensureClusterFn = func(context.Context) (armcs.ManagedCluster, bool, error) {
					return armcs.ManagedCluster{}, false, nil
				}
			},
			wantCalls: []string{"ensureCluster"},
		},
		{
			name: "ensureCluster_error",
			cfg:  &config{clusterName: "c", resourceGroup: "rg", subscriptionID: "sub", cpVersion: "1.30.0"},
			setup: func(m *mockOrchestrator) {
				m.ensureClusterFn = func(context.Context) (armcs.ManagedCluster, bool, error) {
					return armcs.ManagedCluster{}, false, dummyErr
				}
			},
			wantErr:   "ensure cluster:",
			wantCalls: []string{"ensureCluster"},
		},
		{
			name: "guards_do_not_fire_non_guard1",
			cfg:  &config{clusterName: "c", resourceGroup: "rg", subscriptionID: "sub", cpVersion: "1.30.0"},
			setup: func(m *mockOrchestrator) {
				m.detectFn = func(_ context.Context, _ int) (bool, string, error) {
					return false, "cluster state FAIL: not recoverable", nil
				}
			},
			wantCalls: []string{"ensureCluster", "dumpPreflight", "detect:1"},
		},
		{
			name: "pre_scan_adopts_leftover_before_detection_noop",
			cfg:  &config{clusterName: "c", resourceGroup: "rg", subscriptionID: "sub", cpVersion: "1.30.0"},
			setup: func(m *mockOrchestrator) {
				m.adoptLeftoverTempPoolsFn = func(context.Context) error { return nil }
				m.detectFn = func(_ context.Context, _ int) (bool, string, error) {
					return false, "no selected node pools with role user", nil
				}
			},
			wantCalls: []string{"ensureCluster", "dumpPreflight", "adoptLeftoverTempPools", "detect:1"},
		},
		{
			name: "pre_scan_error_stops_before_detection",
			cfg:  &config{clusterName: "c", resourceGroup: "rg", subscriptionID: "sub", cpVersion: "1.30.0"},
			setup: func(m *mockOrchestrator) {
				m.adoptLeftoverTempPoolsFn = func(context.Context) error { return dummyErr }
			},
			wantErr:   "adopt leftover temp pools:",
			wantCalls: []string{"ensureCluster", "dumpPreflight", "adoptLeftoverTempPools"},
		},
		{
			name: "detect_error",
			cfg:  &config{clusterName: "c", resourceGroup: "rg", subscriptionID: "sub", cpVersion: "1.30.0"},
			setup: func(m *mockOrchestrator) {
				m.detectFn = func(_ context.Context, _ int) (bool, string, error) {
					return false, "", dummyErr
				}
			},
			wantErr:   "detection:",
			wantCalls: []string{"ensureCluster", "dumpPreflight", "detect:1"},
		},
		{
			name:      "dry_run_exits_after_guards",
			cfg:       &config{clusterName: "c", resourceGroup: "rg", subscriptionID: "sub", cpVersion: "1.30.0", dryRun: true},
			wantCalls: []string{"ensureCluster", "dumpPreflight", "detect:1"},
		},
		{
			name:      "cpVersion_empty_rejected",
			cfg:       &config{clusterName: "c", resourceGroup: "rg", subscriptionID: "sub", cpVersion: ""},
			wantErr:   "currentKubernetesVersion empty",
			wantCalls: []string{"ensureCluster", "dumpPreflight", "detect:1"},
		},
		{
			name: "lro_too_young_exits",
			cfg:  &config{clusterName: "c", resourceGroup: "rg", subscriptionID: "sub", cpVersion: "1.30.0"},
			setup: func(m *mockOrchestrator) {
				m.maybeAbortLROFn = func(context.Context) (bool, error) {
					return false, nil
				}
			},
			wantCalls: []string{
				"ensureCluster", "dumpPreflight", "detect:1",
				"bootstrapKube", "dumpPreflight",
				"snapshotSystem", "maybeAbortLRO",
			},
		},
		{
			// SKIP_GUARDS must not turn an empty confirmed-target list
			// into a destructive run. When detection finds no broken
			// pool, targets = []. We must exit no-op before Step 2
			// (maybeAbortLRO) and Step 8 (reconcileTagPut) fire, even
			// with SKIP_GUARDS=true.
			name: "skip_guards_pre_lro_empty_targets_exits_noop",
			cfg:  &config{clusterName: "c", resourceGroup: "rg", subscriptionID: "sub", cpVersion: "1.30.0", skipGuards: true},
			setup: func(m *mockOrchestrator) {
				m.detectFn = func(_ context.Context, _ int) (bool, string, error) {
					// No broken pool detected: the mock returns an empty
					// target list, so there is nothing to recreate.
					return false, "no broken pools detected", nil
				}
			},
			wantCalls: []string{"ensureCluster", "dumpPreflight", "detect:1"},
		},
		{
			// Same protection at the post-LRO recheck gate: detect:1
			// confirms a target so we reach Step 2; detect:2 finds the
			// pool healthy and returns an empty list. With
			// skipGuards=true we must still exit before reconcileTagPut.
			name: "skip_guards_post_lro_empty_targets_exits_noop",
			cfg:  &config{clusterName: "c", resourceGroup: "rg", subscriptionID: "sub", cpVersion: "1.30.0", skipGuards: true},
			setup: func(m *mockOrchestrator) {
				m.detectFn = func(_ context.Context, n int) (bool, string, error) {
					if n == 1 {
						return true, "", nil
					}
					return false, "no broken pools detected after LRO", nil
				}
			},
			wantCalls: []string{
				"ensureCluster", "dumpPreflight", "detect:1",
				"bootstrapKube", "dumpPreflight",
				"snapshotSystem", "maybeAbortLRO", "detect:2",
			},
		},
		{
			name: "guards_fail_after_lro_abort",
			cfg:  &config{clusterName: "c", resourceGroup: "rg", subscriptionID: "sub", cpVersion: "1.30.0"},
			setup: func(m *mockOrchestrator) {
				m.detectFn = func(_ context.Context, n int) (bool, string, error) {
					if n == 1 {
						return true, "", nil
					}
					return false, "system wedge FAIL after LRO", nil
				}
			},
			wantCalls: []string{
				"ensureCluster", "dumpPreflight", "detect:1",
				"bootstrapKube", "dumpPreflight",
				"snapshotSystem", "maybeAbortLRO", "detect:2",
			},
		},
		{
			name:      "full_happy_path",
			cfg:       &config{clusterName: "c", resourceGroup: "rg", subscriptionID: "sub", cpVersion: "1.30.0"},
			wantCalls: fullHappyPath,
		},
		{
			name: "addTempPool_error",
			cfg:  &config{clusterName: "c", resourceGroup: "rg", subscriptionID: "sub", cpVersion: "1.30.0"},
			setup: func(m *mockOrchestrator) {
				m.addTempPoolFn = func(context.Context, nodePoolTarget, *armcs.AgentPool) error { return dummyErr }
			},
			wantErr: "add temp pool " + tmpSystem + " for system:",
			wantCalls: []string{
				"ensureCluster", "dumpPreflight", "detect:1",
				"bootstrapKube", "dumpPreflight",
				"snapshotSystem", "maybeAbortLRO", "detect:2", "adoptLeftoverTempPool:system", "snapshotSystem",
				"addTempPool:system",
			},
		},
		{
			name: "drain_system_error",
			cfg:  &config{clusterName: "c", resourceGroup: "rg", subscriptionID: "sub", cpVersion: "1.30.0"},
			setup: func(m *mockOrchestrator) {
				m.drainPoolFn = func(_ context.Context, pool string, _ time.Duration) error {
					if pool == "system" {
						return dummyErr
					}
					return nil
				}
			},
			wantErr: "drain system:",
			wantCalls: []string{
				"ensureCluster", "dumpPreflight", "detect:1",
				"bootstrapKube", "dumpPreflight",
				"snapshotSystem", "maybeAbortLRO", "detect:2", "adoptLeftoverTempPool:system", "snapshotSystem",
				"addTempPool:system", "drainPool:system",
			},
		},
		{
			name: "delete_system_error",
			cfg:  &config{clusterName: "c", resourceGroup: "rg", subscriptionID: "sub", cpVersion: "1.30.0"},
			setup: func(m *mockOrchestrator) {
				m.deletePoolFn = func(_ context.Context, pool string) error {
					if pool == "system" {
						return dummyErr
					}
					return nil
				}
			},
			wantErr: "delete system:",
		},
		{
			name: "recreate_system_error",
			cfg:  &config{clusterName: "c", resourceGroup: "rg", subscriptionID: "sub", cpVersion: "1.30.0"},
			setup: func(m *mockOrchestrator) {
				m.recreateSystemFn = func(context.Context, *armcs.AgentPool) error { return dummyErr }
			},
			wantErr: "recreate system:",
		},
		{
			name: "temp_drain_warns_but_continues",
			cfg:  &config{clusterName: "c", resourceGroup: "rg", subscriptionID: "sub", cpVersion: "1.30.0"},
			setup: func(m *mockOrchestrator) {
				m.drainPoolFn = func(_ context.Context, pool string, _ time.Duration) error {
					if pool == tmpSystem {
						return dummyErr
					}
					return nil
				}
			},
			wantCalls: fullHappyPath,
		},
		{
			name: "delete_temp_error",
			cfg:  &config{clusterName: "c", resourceGroup: "rg", subscriptionID: "sub", cpVersion: "1.30.0"},
			setup: func(m *mockOrchestrator) {
				m.deletePoolFn = func(_ context.Context, pool string) error {
					if pool == tmpSystem {
						return dummyErr
					}
					return nil
				}
			},
			wantErr: "delete temp pool " + tmpSystem + ":",
		},
		{
			name: "tag_reconcile_error",
			cfg:  &config{clusterName: "c", resourceGroup: "rg", subscriptionID: "sub", cpVersion: "1.30.0"},
			setup: func(m *mockOrchestrator) {
				m.reconcileTagPutFn = func(context.Context) error { return dummyErr }
			},
			wantErr: "tag reconcile:",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := &mockOrchestrator{}
			if tc.setup != nil {
				tc.setup(mock)
			}
			ctx := context.Background()
			err := runWith(ctx, tc.cfg, mock)

			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tc.wantCalls != nil {
				if !reflect.DeepEqual(mock.calls, tc.wantCalls) {
					t.Errorf("calls mismatch:\n  got:  %v\n  want: %v", mock.calls, tc.wantCalls)
				}
			}
		})
	}
}

// =============================================================================
// accelerated-networking guard (AROSLSRE-1172)
// =============================================================================

func TestDecideAccelNetworking(t *testing.T) {
	cases := []struct {
		name                             string
		swiftRequired                    bool
		refAN, refFound, tgtAN, tgtFound bool
		wantPatch                        bool
		wantWant                         bool
	}{
		{name: "reference_unknown_fails_open", refFound: false, tgtAN: false, tgtFound: true, wantPatch: false},
		{name: "target_unknown_fails_open", refAN: true, refFound: true, tgtFound: false, wantPatch: false},
		{name: "match_enabled_no_patch", refAN: true, refFound: true, tgtAN: true, tgtFound: true, wantPatch: false, wantWant: true},
		{name: "match_disabled_no_patch", refAN: false, refFound: true, tgtAN: false, tgtFound: true, wantPatch: false, wantWant: false},
		{name: "mismatch_temp_disabled_patch_to_enabled", refAN: true, refFound: true, tgtAN: false, tgtFound: true, wantPatch: true, wantWant: true},
		{name: "mismatch_temp_enabled_patch_to_disabled", refAN: false, refFound: true, tgtAN: true, tgtFound: true, wantPatch: true, wantWant: false},
		// Swift pools demand AN=true regardless of the reference.
		{name: "swift_target_disabled_patch_to_enabled", swiftRequired: true, tgtAN: false, tgtFound: true, wantPatch: true, wantWant: true},
		{name: "swift_target_enabled_no_patch", swiftRequired: true, tgtAN: true, tgtFound: true, wantPatch: false, wantWant: true},
		{name: "swift_target_unknown_patch_to_enabled", swiftRequired: true, tgtFound: false, wantPatch: true, wantWant: true},
		{name: "swift_ignores_reference_disabled", swiftRequired: true, refAN: false, refFound: true, tgtAN: false, tgtFound: true, wantPatch: true, wantWant: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := decideAccelNetworking(tc.swiftRequired, tc.refAN, tc.refFound, tc.tgtAN, tc.tgtFound)
			if d.patch != tc.wantPatch {
				t.Fatalf("patch = %v, want %v (reason: %s)", d.patch, tc.wantPatch, d.reason)
			}
			if d.want != tc.wantWant {
				t.Fatalf("want = %v, want %v (reason: %s)", d.want, tc.wantWant, d.reason)
			}
			if d.reason == "" {
				t.Fatalf("reason must not be empty")
			}
		})
	}
}

func vmssWithNICs(values ...*bool) *armcompute.VirtualMachineScaleSet {
	nics := make([]*armcompute.VirtualMachineScaleSetNetworkConfiguration, 0, len(values))
	for _, v := range values {
		nics = append(nics, &armcompute.VirtualMachineScaleSetNetworkConfiguration{
			Properties: &armcompute.VirtualMachineScaleSetNetworkConfigurationProperties{
				EnableAcceleratedNetworking: v,
			},
		})
	}
	return &armcompute.VirtualMachineScaleSet{
		Properties: &armcompute.VirtualMachineScaleSetProperties{
			VirtualMachineProfile: &armcompute.VirtualMachineScaleSetVMProfile{
				NetworkProfile: &armcompute.VirtualMachineScaleSetNetworkProfile{
					NetworkInterfaceConfigurations: nics,
				},
			},
		},
	}
}

func TestVMSSAcceleratedNetworking(t *testing.T) {
	cases := []struct {
		name        string
		vmss        *armcompute.VirtualMachineScaleSet
		wantEnabled bool
		wantFound   bool
	}{
		{name: "nil_vmss", vmss: nil, wantFound: false},
		{name: "no_network_profile", vmss: &armcompute.VirtualMachineScaleSet{Properties: &armcompute.VirtualMachineScaleSetProperties{}}, wantFound: false},
		{name: "nic_without_value", vmss: vmssWithNICs(nil), wantFound: false},
		{name: "single_enabled", vmss: vmssWithNICs(ptr(true)), wantEnabled: true, wantFound: true},
		{name: "single_disabled", vmss: vmssWithNICs(ptr(false)), wantEnabled: false, wantFound: true},
		{name: "mixed_one_disabled_and_false", vmss: vmssWithNICs(ptr(false), ptr(true)), wantEnabled: false, wantFound: true},
		{name: "all_enabled", vmss: vmssWithNICs(ptr(true), ptr(true)), wantEnabled: true, wantFound: true},
		{name: "all_disabled", vmss: vmssWithNICs(ptr(false), ptr(false)), wantEnabled: false, wantFound: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enabled, found := vmssAcceleratedNetworking(tc.vmss)
			if enabled != tc.wantEnabled || found != tc.wantFound {
				t.Fatalf("got (enabled=%v found=%v), want (enabled=%v found=%v)", enabled, found, tc.wantEnabled, tc.wantFound)
			}
		})
	}
}

func TestZeroNodePoolBody(t *testing.T) {
	final := &armcs.AgentPool{
		Properties: &armcs.ManagedClusterAgentPoolProfileProperties{
			Count:             ptr(int32(5)),
			EnableAutoScaling: ptr(true),
			MinCount:          ptr(int32(3)),
			MaxCount:          ptr(int32(9)),
			VMSize:            ptr("Standard_D8s_v3"),
		},
	}
	zero, err := zeroNodePoolBody(final)
	if err != nil {
		t.Fatalf("zeroNodePoolBody: %v", err)
	}
	if zero.Properties.Count == nil || *zero.Properties.Count != 0 {
		t.Fatalf("zero Count = %v, want 0", zero.Properties.Count)
	}
	if zero.Properties.EnableAutoScaling == nil || *zero.Properties.EnableAutoScaling {
		t.Fatalf("zero EnableAutoScaling = %v, want false", zero.Properties.EnableAutoScaling)
	}
	if zero.Properties.MinCount != nil || zero.Properties.MaxCount != nil {
		t.Fatalf("zero Min/MaxCount must be nil, got %v/%v", zero.Properties.MinCount, zero.Properties.MaxCount)
	}
	if strDeref(zero.Properties.VMSize) != "Standard_D8s_v3" {
		t.Fatalf("zero VMSize = %q, want preserved", strDeref(zero.Properties.VMSize))
	}
	// input must not be mutated
	if *final.Properties.Count != 5 || !*final.Properties.EnableAutoScaling ||
		*final.Properties.MinCount != 3 || *final.Properties.MaxCount != 9 {
		t.Fatalf("zeroNodePoolBody mutated the input body: %+v", final.Properties)
	}
}

func TestZeroNodePoolBodyNil(t *testing.T) {
	if _, err := zeroNodePoolBody(nil); err == nil {
		t.Fatalf("expected error for nil body")
	}
	if _, err := zeroNodePoolBody(&armcs.AgentPool{}); err == nil {
		t.Fatalf("expected error for nil Properties")
	}
}

// =============================================================================
// VMSS client paths via azcore fake transport
//
// These tests exercise the live-ARM code paths (findPoolVMSS, patchVMSS-
// AccelNetworking, ensurePoolAccelNetworking) offline by wiring the real
// armcompute VMSS client to an in-memory fake server. No cluster or Azure
// credentials are required; the fake server keeps mutable VMSS state so the
// read-after-write semantics of the patch flow are exercised end to end.
// =============================================================================

// fakeVMSSStore is the mutable backing state for the fake VMSS server. GET and
// LIST read from it; BeginCreateOrUpdate (full PUT) and BeginUpdate (scoped
// PATCH) both write to it, so a patch is visible to the subsequent confirmation
// read just like real ARM.
type fakeVMSSStore struct {
	mu      sync.Mutex
	byName  map[string]*armcompute.VirtualMachineScaleSet
	puts    []armcompute.VirtualMachineScaleSet       // captured full-PUT bodies, in order
	updates []armcompute.VirtualMachineScaleSetUpdate // captured scoped-PATCH bodies, in order
}

func newFakeVMSSStore(vmsss ...*armcompute.VirtualMachineScaleSet) *fakeVMSSStore {
	s := &fakeVMSSStore{byName: map[string]*armcompute.VirtualMachineScaleSet{}}
	for _, v := range vmsss {
		s.byName[strDeref(v.Name)] = v
	}
	return s
}

func (s *fakeVMSSStore) list() []*armcompute.VirtualMachineScaleSet {
	out := make([]*armcompute.VirtualMachineScaleSet, 0, len(s.byName))
	for _, v := range s.byName {
		out = append(out, v)
	}
	return out
}

// newFakeVMSSClient builds a real armcompute VMSS client backed by store.
func newFakeVMSSClient(t *testing.T, store *fakeVMSSStore) *armcompute.VirtualMachineScaleSetsClient {
	t.Helper()
	srv := computefake.VirtualMachineScaleSetsServer{
		NewListPager: func(_ string, _ *armcompute.VirtualMachineScaleSetsClientListOptions) (resp azfake.PagerResponder[armcompute.VirtualMachineScaleSetsClientListResponse]) {
			store.mu.Lock()
			defer store.mu.Unlock()
			resp.AddPage(http.StatusOK, armcompute.VirtualMachineScaleSetsClientListResponse{
				VirtualMachineScaleSetListResult: armcompute.VirtualMachineScaleSetListResult{Value: store.list()},
			}, nil)
			return
		},
		Get: func(_ context.Context, _ string, name string, _ *armcompute.VirtualMachineScaleSetsClientGetOptions) (resp azfake.Responder[armcompute.VirtualMachineScaleSetsClientGetResponse], errResp azfake.ErrorResponder) {
			store.mu.Lock()
			defer store.mu.Unlock()
			v, ok := store.byName[name]
			if !ok {
				errResp.SetResponseError(http.StatusNotFound, "ResourceNotFound")
				return
			}
			resp.SetResponse(http.StatusOK, armcompute.VirtualMachineScaleSetsClientGetResponse{VirtualMachineScaleSet: *v}, nil)
			return
		},
		BeginCreateOrUpdate: func(_ context.Context, _ string, name string, params armcompute.VirtualMachineScaleSet, _ *armcompute.VirtualMachineScaleSetsClientBeginCreateOrUpdateOptions) (resp azfake.PollerResponder[armcompute.VirtualMachineScaleSetsClientCreateOrUpdateResponse], errResp azfake.ErrorResponder) {
			store.mu.Lock()
			defer store.mu.Unlock()
			stored := params
			store.byName[name] = &stored
			store.puts = append(store.puts, params)
			resp.SetTerminalResponse(http.StatusOK, armcompute.VirtualMachineScaleSetsClientCreateOrUpdateResponse{VirtualMachineScaleSet: stored}, nil)
			return
		},
		BeginUpdate: func(_ context.Context, _ string, name string, params armcompute.VirtualMachineScaleSetUpdate, _ *armcompute.VirtualMachineScaleSetsClientBeginUpdateOptions) (resp azfake.PollerResponder[armcompute.VirtualMachineScaleSetsClientUpdateResponse], errResp azfake.ErrorResponder) {
			store.mu.Lock()
			defer store.mu.Unlock()
			v, ok := store.byName[name]
			if !ok {
				errResp.SetResponseError(http.StatusNotFound, "ResourceNotFound")
				return
			}
			store.updates = append(store.updates, params)
			// Apply only the accelerated-networking flag from the scoped PATCH
			// body onto the stored NIC configs (matched by position), mirroring
			// ARM's merge of the network profile while leaving everything else
			// untouched.
			if params.Properties != nil && params.Properties.VirtualMachineProfile != nil &&
				params.Properties.VirtualMachineProfile.NetworkProfile != nil &&
				v.Properties != nil && v.Properties.VirtualMachineProfile != nil &&
				v.Properties.VirtualMachineProfile.NetworkProfile != nil {
				upd := params.Properties.VirtualMachineProfile.NetworkProfile.NetworkInterfaceConfigurations
				cur := v.Properties.VirtualMachineProfile.NetworkProfile.NetworkInterfaceConfigurations
				for i := range upd {
					if i >= len(cur) || upd[i] == nil || upd[i].Properties == nil || cur[i] == nil {
						continue
					}
					if cur[i].Properties == nil {
						cur[i].Properties = &armcompute.VirtualMachineScaleSetNetworkConfigurationProperties{}
					}
					cur[i].Properties.EnableAcceleratedNetworking = upd[i].Properties.EnableAcceleratedNetworking
				}
			}
			resp.SetTerminalResponse(http.StatusOK, armcompute.VirtualMachineScaleSetsClientUpdateResponse{VirtualMachineScaleSet: *v}, nil)
			return
		},
	}
	client, err := armcompute.NewVirtualMachineScaleSetsClient(
		"00000000-0000-0000-0000-000000000000",
		&azfake.TokenCredential{},
		&azcorearm.ClientOptions{ClientOptions: azcore.ClientOptions{
			Transport: computefake.NewVirtualMachineScaleSetsServerTransport(&srv),
		}},
	)
	if err != nil {
		t.Fatalf("build fake VMSS client: %v", err)
	}
	return client
}

// namedVMSS builds a VMSS with a name, the aks-managed-poolName tag, and one NIC
// carrying the given accelerated-networking value.
func namedVMSS(name, poolTag string, an *bool) *armcompute.VirtualMachineScaleSet {
	v := vmssWithNICs(an)
	v.Name = ptr(name)
	if poolTag != "" {
		v.Tags = map[string]*string{aksManagedPoolNameTag: ptr(poolTag)}
	}
	return v
}

func newTestClients(store *fakeVMSSStore, t *testing.T) *clients {
	return &clients{
		cfg:  &config{nodeRG: "MC_rg_cluster_region"},
		vmss: newFakeVMSSClient(t, store),
	}
}

func TestFindPoolVMSS(t *testing.T) {
	store := newFakeVMSSStore(
		namedVMSS("aks-userswft3-12345678-vmss", "userswft3", ptr(true)),
		namedVMSS("aks-system-87654321-vmss", "system", ptr(true)),
		// A VMSS whose name matches the aks-<pool>- prefix of "lonely" but
		// carries no matching tag — must NOT be matched (we never guess by
		// name convention; only the authoritative tag counts).
		namedVMSS("aks-lonely-00000000-vmss", "", ptr(false)),
	)
	c := newTestClients(store, t)
	ctx := context.Background()

	t.Run("tag_match", func(t *testing.T) {
		name, v, err := c.findPoolVMSS(ctx, "userswft3")
		if err != nil {
			t.Fatalf("findPoolVMSS: %v", err)
		}
		if name != "aks-userswft3-12345678-vmss" || v == nil {
			t.Fatalf("got name=%q vmss=%v, want tag match", name, v)
		}
	})

	t.Run("name_prefix_not_matched", func(t *testing.T) {
		// "lonely" has a prefix-matching VMSS but no tag — must error.
		if _, _, err := c.findPoolVMSS(ctx, "lonely"); err == nil {
			t.Fatalf("expected error: prefix-only match must not be returned")
		}
	})

	t.Run("not_found", func(t *testing.T) {
		if _, _, err := c.findPoolVMSS(ctx, "doesnotexist"); err == nil {
			t.Fatalf("expected error for missing pool VMSS")
		}
	})
}

func TestPatchVMSSAccelNetworking(t *testing.T) {
	// Two NICs, both disabled; patch must flip both to true and the stored
	// state must reflect it.
	target := namedVMSS("aks-userswft3-12345678-vmss", "userswft3", ptr(false))
	target.Properties.VirtualMachineProfile.NetworkProfile.NetworkInterfaceConfigurations = append(
		target.Properties.VirtualMachineProfile.NetworkProfile.NetworkInterfaceConfigurations,
		&armcompute.VirtualMachineScaleSetNetworkConfiguration{
			Properties: &armcompute.VirtualMachineScaleSetNetworkConfigurationProperties{
				EnableAcceleratedNetworking: ptr(false),
			},
		},
	)
	store := newFakeVMSSStore(target)
	c := newTestClients(store, t)

	if err := c.patchVMSSAccelNetworking(context.Background(), "aks-userswft3-12345678-vmss", true); err != nil {
		t.Fatalf("patchVMSSAccelNetworking: %v", err)
	}
	// Regression guard: the fix must NOT issue a full read-modify-write PUT
	// (which re-sends storageProfile.imageReference and triggers the linked
	// image-gallery 403 LinkedAuthorizationFailed). It must use a scoped PATCH.
	if len(store.puts) != 0 {
		t.Fatalf("expected 0 full PUTs (scoped PATCH only), got %d", len(store.puts))
	}
	if len(store.updates) != 1 {
		t.Fatalf("expected exactly 1 scoped PATCH, got %d", len(store.updates))
	}
	// The PATCH body must carry only the network profile; storageProfile must be
	// omitted so ARM never runs the linked image-gallery auth check.
	updProps := store.updates[0].Properties
	if updProps == nil || updProps.VirtualMachineProfile == nil {
		t.Fatalf("PATCH body missing virtualMachineProfile")
	}
	if updProps.VirtualMachineProfile.StorageProfile != nil {
		t.Fatalf("PATCH body must omit storageProfile to avoid linked image auth")
	}
	if updProps.VirtualMachineProfile.NetworkProfile == nil {
		t.Fatalf("PATCH body missing networkProfile")
	}
	got, found := vmssAcceleratedNetworking(store.byName["aks-userswft3-12345678-vmss"])
	if !found || !got {
		t.Fatalf("after patch accelerated-networking=(enabled=%v found=%v), want enabled", got, found)
	}
	for i, nic := range updProps.VirtualMachineProfile.NetworkProfile.NetworkInterfaceConfigurations {
		if nic.Properties == nil || nic.Properties.EnableAcceleratedNetworking == nil || !*nic.Properties.EnableAcceleratedNetworking {
			t.Fatalf("PATCH body NIC %d not set to accelerated-networking=true", i)
		}
	}
}

func TestPatchVMSSAccelNetworkingNoNICs(t *testing.T) {
	bare := namedVMSS("aks-x-1-vmss", "x", nil)
	bare.Properties.VirtualMachineProfile.NetworkProfile.NetworkInterfaceConfigurations = nil
	store := newFakeVMSSStore(bare)
	c := newTestClients(store, t)
	if err := c.patchVMSSAccelNetworking(context.Background(), "aks-x-1-vmss", true); err == nil {
		t.Fatalf("expected error patching a VMSS with no NIC configurations")
	}
}

func TestEnsurePoolAccelNetworking(t *testing.T) {
	const tgtVMSS = "aks-userswft3-12345678-vmss"
	const refVMSS = "aks-userswft3temp-87654321-vmss"

	t.Run("mismatch_patches_and_confirms", func(t *testing.T) {
		store := newFakeVMSSStore(
			namedVMSS(tgtVMSS, "userswft3", ptr(false)),    // broken: AN disabled
			namedVMSS(refVMSS, "userswft3temp", ptr(true)), // reference: AN enabled
		)
		c := newTestClients(store, t)
		patched, want, err := c.ensurePoolAccelNetworking(context.Background(), "userswft3", "userswft3temp", false)
		if err != nil {
			t.Fatalf("ensurePoolAccelNetworking: %v", err)
		}
		if !patched || !want {
			t.Fatalf("got patched=%v want=%v, expected patched=true want=true", patched, want)
		}
		if got, found := vmssAcceleratedNetworking(store.byName[tgtVMSS]); !found || !got {
			t.Fatalf("target VMSS not corrected: enabled=%v found=%v", got, found)
		}
	})

	t.Run("already_matches_no_patch", func(t *testing.T) {
		store := newFakeVMSSStore(
			namedVMSS(tgtVMSS, "userswft3", ptr(true)),
			namedVMSS(refVMSS, "userswft3temp", ptr(true)),
		)
		c := newTestClients(store, t)
		patched, _, err := c.ensurePoolAccelNetworking(context.Background(), "userswft3", "userswft3temp", false)
		if err != nil {
			t.Fatalf("ensurePoolAccelNetworking: %v", err)
		}
		if patched || len(store.puts) != 0 {
			t.Fatalf("expected no patch when values match (patched=%v puts=%d)", patched, len(store.puts))
		}
	})

	t.Run("reference_unknown_fails_open", func(t *testing.T) {
		store := newFakeVMSSStore(
			namedVMSS(tgtVMSS, "userswft3", ptr(false)),
			namedVMSS(refVMSS, "userswft3temp", nil), // no explicit AN -> unknown
		)
		c := newTestClients(store, t)
		patched, _, err := c.ensurePoolAccelNetworking(context.Background(), "userswft3", "userswft3temp", false)
		if err != nil {
			t.Fatalf("fail-open should not error: %v", err)
		}
		if patched || len(store.puts) != 0 {
			t.Fatalf("expected fail-open no-patch when reference unknown (patched=%v puts=%d)", patched, len(store.puts))
		}
	})
}

// =============================================================================
// Swift pool identification + accelerated-networking precheck
// =============================================================================

func swiftAgentPool(tags map[string]string) *armcs.AgentPool {
	t := map[string]*string{}
	for k, v := range tags {
		t[k] = ptr(v)
	}
	return &armcs.AgentPool{
		Properties: &armcs.ManagedClusterAgentPoolProfileProperties{Tags: t},
	}
}

func TestPoolIsSwift(t *testing.T) {
	cases := []struct {
		name string
		pool *armcs.AgentPool
		want bool
	}{
		{"swift_tag_true", swiftAgentPool(map[string]string{swiftNodepoolTag: "true"}), true},
		{"swift_tag_mixed_case", swiftAgentPool(map[string]string{swiftNodepoolTag: "True"}), true},
		{"swift_tag_false", swiftAgentPool(map[string]string{swiftNodepoolTag: "false"}), false},
		{"swift_tag_absent", swiftAgentPool(map[string]string{"other": "true"}), false},
		{"no_tags", swiftAgentPool(nil), false},
		{"nil_properties", &armcs.AgentPool{}, false},
		{"nil_pool", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := poolIsSwift(tc.pool); got != tc.want {
				t.Fatalf("poolIsSwift = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDetectAccelNetBrokenPools(t *testing.T) {
	store := newFakeVMSSStore(
		// Swift pool with AN disabled -> must be flagged broken.
		namedVMSS("aks-swdisabled-11111111-vmss", "swdisabled", ptr(false)),
		// Swift pool with AN enabled -> healthy, not flagged.
		namedVMSS("aks-swenabled-22222222-vmss", "swenabled", ptr(true)),
		// Swift pool whose VMSS reports no AN setting -> indeterminate, skipped.
		namedVMSS("aks-swunknown-33333333-vmss", "swunknown", nil),
	)
	c := newTestClients(store, t)
	ctx := context.Background()

	swiftPools := []nodePoolTarget{
		{name: "swdisabled"},
		{name: "swenabled"},
		{name: "swunknown"},
		// Swift pool with no backing VMSS yet -> skipped (fail-open).
		{name: "swmissing"},
	}

	broken, err := c.detectAccelNetBrokenPools(ctx, swiftPools)
	if err != nil {
		t.Fatalf("detectAccelNetBrokenPools: %v", err)
	}
	if len(broken) != 1 {
		t.Fatalf("expected exactly 1 broken pool, got %d: %+v", len(broken), broken)
	}
	if broken[0].name != "swdisabled" {
		t.Fatalf("expected swdisabled flagged, got %q", broken[0].name)
	}
	if !broken[0].accelNetBroken {
		t.Fatalf("expected accelNetBroken=true on flagged pool")
	}

	t.Run("empty_input_no_list", func(t *testing.T) {
		got, err := c.detectAccelNetBrokenPools(ctx, nil)
		if err != nil || got != nil {
			t.Fatalf("empty input should return (nil,nil), got (%v,%v)", got, err)
		}
	})
}

// =============================================================================
// empty-ipConfiguration detection (Steve Kuznetsov, MSFT): a pool whose backing
// VMSS has realized instance NICs with an empty ipConfigurations array is broken
// by the NRP null-pointer defect and must be recreated regardless of the
// activity-log storm signal.
// =============================================================================

// nic builds a realized network interface with the given number of
// ipConfigurations (n==0 reproduces the NRP null-pointer signature).
func nic(ipConfigs int) *armnetwork.Interface {
	cfgs := make([]*armnetwork.InterfaceIPConfiguration, 0, ipConfigs)
	for i := 0; i < ipConfigs; i++ {
		cfgs = append(cfgs, &armnetwork.InterfaceIPConfiguration{})
	}
	return &armnetwork.Interface{
		Properties: &armnetwork.InterfacePropertiesFormat{IPConfigurations: cfgs},
	}
}

func TestCountEmptyIPConfigs(t *testing.T) {
	cases := []struct {
		name      string
		nics      []*armnetwork.Interface
		wantTotal int
		wantEmpty int
	}{
		{name: "nil_slice", nics: nil, wantTotal: 0, wantEmpty: 0},
		{name: "all_healthy", nics: []*armnetwork.Interface{nic(1), nic(2)}, wantTotal: 2, wantEmpty: 0},
		{name: "one_empty", nics: []*armnetwork.Interface{nic(1), nic(0)}, wantTotal: 2, wantEmpty: 1},
		{name: "all_empty", nics: []*armnetwork.Interface{nic(0), nic(0)}, wantTotal: 2, wantEmpty: 2},
		{name: "nil_nic_skipped", nics: []*armnetwork.Interface{nil, nic(0)}, wantTotal: 1, wantEmpty: 1},
		{name: "nil_properties_skipped", nics: []*armnetwork.Interface{{Properties: nil}, nic(1)}, wantTotal: 1, wantEmpty: 0},
		// nil (omitted/null) IPConfigurations is indeterminate, NOT the
		// empty-array defect: it is skipped entirely (not counted toward
		// total or empty), matching array_length(null) != 0.
		{name: "nil_ipconfigs_skipped", nics: []*armnetwork.Interface{{Properties: &armnetwork.InterfacePropertiesFormat{IPConfigurations: nil}}, nic(1)}, wantTotal: 1, wantEmpty: 0},
		{name: "only_nil_ipconfigs_indeterminate", nics: []*armnetwork.Interface{{Properties: &armnetwork.InterfacePropertiesFormat{IPConfigurations: nil}}}, wantTotal: 0, wantEmpty: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			total, empty := countEmptyIPConfigs(tc.nics)
			if total != tc.wantTotal || empty != tc.wantEmpty {
				t.Fatalf("got (total=%d empty=%d), want (total=%d empty=%d)", total, empty, tc.wantTotal, tc.wantEmpty)
			}
		})
	}
}

// newFakeNICsClient builds a real armnetwork InterfacesClient whose VMSS-NIC
// list reads from nicsByVMSS (keyed by VMSS name). A VMSS absent from the map
// returns an empty NIC list, mirroring ARM for a scaled-to-zero VMSS.
func newFakeNICsClient(t *testing.T, nicsByVMSS map[string][]*armnetwork.Interface) *armnetwork.InterfacesClient {
	t.Helper()
	srv := networkfake.InterfacesServer{
		NewListVirtualMachineScaleSetNetworkInterfacesPager: func(_ string, vmssName string, _ *armnetwork.InterfacesClientListVirtualMachineScaleSetNetworkInterfacesOptions) (resp azfake.PagerResponder[armnetwork.InterfacesClientListVirtualMachineScaleSetNetworkInterfacesResponse]) {
			resp.AddPage(http.StatusOK, armnetwork.InterfacesClientListVirtualMachineScaleSetNetworkInterfacesResponse{
				InterfaceListResult: armnetwork.InterfaceListResult{Value: nicsByVMSS[vmssName]},
			}, nil)
			return
		},
	}
	client, err := armnetwork.NewInterfacesClient(
		"00000000-0000-0000-0000-000000000000",
		&azfake.TokenCredential{},
		&azcorearm.ClientOptions{ClientOptions: azcore.ClientOptions{
			Transport: networkfake.NewInterfacesServerTransport(&srv),
		}},
	)
	if err != nil {
		t.Fatalf("build fake NICs client: %v", err)
	}
	return client
}

func TestDetectEmptyIPConfigPools(t *testing.T) {
	const (
		brokenVMSS  = "aks-userswft3-12345678-vmss"
		healthyVMSS = "aks-system-87654321-vmss"
		zeroVMSS    = "aks-userswft2-11112222-vmss"
	)
	store := newFakeVMSSStore(
		namedVMSS(brokenVMSS, "userswft3", ptr(true)),
		namedVMSS(healthyVMSS, "system", ptr(true)),
		namedVMSS(zeroVMSS, "userswft2", ptr(true)),
		// "nomvss" intentionally absent: no backing VMSS yet.
	)
	nicsByVMSS := map[string][]*armnetwork.Interface{
		brokenVMSS:  {nic(1), nic(0)}, // one NIC lost its ipConfigurations
		healthyVMSS: {nic(1), nic(1)}, // all NICs healthy
		zeroVMSS:    {},               // no realized NICs yet
	}
	c := &clients{
		cfg:  &config{nodeRG: "MC_rg_cluster_region"},
		vmss: newFakeVMSSClient(t, store),
		nics: newFakeNICsClient(t, nicsByVMSS),
	}

	pools := []nodePoolTarget{
		{name: "userswft3"}, {name: "system"}, {name: "userswft2"}, {name: "nomvss"},
	}
	broken, err := c.detectEmptyIPConfigPools(context.Background(), pools)
	if err != nil {
		t.Fatalf("detectEmptyIPConfigPools: %v", err)
	}
	if len(broken) != 1 {
		t.Fatalf("expected exactly 1 broken pool, got %d: %+v", len(broken), broken)
	}
	if broken[0].name != "userswft3" || !broken[0].emptyIPConfig {
		t.Fatalf("got %+v, want pool=userswft3 emptyIPConfig=true", broken[0])
	}
}

func TestDetectEmptyIPConfigPools_EmptyInput(t *testing.T) {
	c := &clients{cfg: &config{nodeRG: "rg"}}
	got, err := c.detectEmptyIPConfigPools(context.Background(), nil)
	if err != nil || got != nil {
		t.Fatalf("empty input should return (nil,nil), got (%v,%v)", got, err)
	}
}
