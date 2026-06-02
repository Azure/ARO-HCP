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
	if c.threshold != defaultThreshold {
		t.Errorf("threshold=%d want default %d", c.threshold, defaultThreshold)
	}
	if c.windowMin != defaultWindowMin {
		t.Errorf("windowMin=%d want default %d", c.windowMin, defaultWindowMin)
	}
	if c.forcedEvidenceTimeoutMin != defaultForcedEvidenceTimeoutMin {
		t.Errorf("forcedEvidenceTimeoutMin=%d want default %d", c.forcedEvidenceTimeoutMin, defaultForcedEvidenceTimeoutMin)
	}
	if c.forcedEvidenceThreshold != defaultForcedEvidenceThreshold {
		t.Errorf("forcedEvidenceThreshold=%d want default %d", c.forcedEvidenceThreshold, defaultForcedEvidenceThreshold)
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
		"NODEPOOL_TAG":        "user",
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

func TestParseEnvConfig_CustomForcedEvidence(t *testing.T) {
	env := envFromMap(map[string]string{
		"CLUSTER_NAME":                "c",
		"RESOURCE_GROUP":              "rg",
		"SUBSCRIPTION_ID":             "sub",
		"NODEPOOL_TAG":                "user",
		"FORCED_EVIDENCE_TIMEOUT_MIN": "45",
		"FORCED_EVIDENCE_THRESHOLD":   "7",
	})
	c, err := parseEnvConfig(env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.forcedEvidenceTimeoutMin != 45 || c.forcedEvidenceThreshold != 7 {
		t.Errorf("forcedEvidenceTimeoutMin=%d forcedEvidenceThreshold=%d", c.forcedEvidenceTimeoutMin, c.forcedEvidenceThreshold)
	}
}

func TestParseEnvConfig_InvalidForcedEvidence(t *testing.T) {
	cases := []struct {
		name string
		key  string
		v    string
	}{
		{"timeout_non_numeric", "FORCED_EVIDENCE_TIMEOUT_MIN", "abc"},
		{"timeout_zero", "FORCED_EVIDENCE_TIMEOUT_MIN", "0"},
		{"timeout_negative", "FORCED_EVIDENCE_TIMEOUT_MIN", "-1"},
		{"threshold_non_numeric", "FORCED_EVIDENCE_THRESHOLD", "xyz"},
		{"threshold_zero", "FORCED_EVIDENCE_THRESHOLD", "0"},
		{"threshold_negative", "FORCED_EVIDENCE_THRESHOLD", "-2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := envFromMap(map[string]string{
				"CLUSTER_NAME":    "c",
				"RESOURCE_GROUP":  "rg",
				"SUBSCRIPTION_ID": "sub",
				"NODEPOOL_TAG":    "user",
				tc.key:            tc.v,
			})
			if _, err := parseEnvConfig(env); err == nil {
				t.Errorf("expected error for %s=%q", tc.key, tc.v)
			}
		})
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
				"NODEPOOL_TAG":       "user",
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
				"NODEPOOL_TAG":        "user",
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
// evalNRPStorm..4
// =============================================================================

func TestEvalNRPStorm(t *testing.T) {
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
			pass, reason := evalNRPStorm(tc.failures, tc.threshold)
			if pass != tc.wantPass {
				t.Errorf("failures=%d threshold=%d: pass=%t want %t (%s)", tc.failures, tc.threshold, pass, tc.wantPass, reason)
			}
			if !pass && reason == "" {
				t.Errorf("non-pass result must have a reason")
			}
		})
	}
}

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

func TestAgentPoolForScaleUpTrigger(t *testing.T) {
	live := mkLiveSystemPool()
	count := int32(3)
	minCount := int32(3)
	maxCount := int32(6)
	autoscale := true
	live.Properties.Count = &count
	live.Properties.MinCount = &minCount
	live.Properties.MaxCount = &maxCount
	live.Properties.EnableAutoScaling = &autoscale

	body, err := agentPoolForScaleUpTrigger(live, "1.35.4")
	if err != nil {
		t.Fatalf("agentPoolForScaleUpTrigger() error = %v", err)
	}
	if got := *body.Properties.Count; got != 4 {
		t.Errorf("Count=%d want 4", got)
	}
	if got := *body.Properties.MinCount; got != 4 {
		t.Errorf("MinCount=%d want 4", got)
	}
	if got := *body.Properties.MaxCount; got != 6 {
		t.Errorf("MaxCount=%d want 6", got)
	}
}

func TestAgentPoolForScaleUpTriggerRaisesMaxCount(t *testing.T) {
	live := mkLiveSystemPool()
	count := int32(3)
	minCount := int32(3)
	maxCount := int32(3)
	autoscale := true
	live.Properties.Count = &count
	live.Properties.MinCount = &minCount
	live.Properties.MaxCount = &maxCount
	live.Properties.EnableAutoScaling = &autoscale

	body, err := agentPoolForScaleUpTrigger(live, "1.35.4")
	if err != nil {
		t.Fatalf("agentPoolForScaleUpTrigger() error = %v", err)
	}
	if got := *body.Properties.MinCount; got != 4 {
		t.Errorf("MinCount=%d want 4", got)
	}
	if got := *body.Properties.MaxCount; got != 4 {
		t.Errorf("MaxCount=%d want 4", got)
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
	if p.Count == nil || *p.Count != 1 {
		t.Errorf("temp Count should be 1, got %v", p.Count)
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
	preflightChecksFn        func(ctx context.Context) error
	snapshotSystemFn         func(ctx context.Context) (*armcs.AgentPool, error)
	maybeAbortLROFn          func(ctx context.Context) (bool, error)
	addTempPoolFn            func(ctx context.Context, target nodePoolTarget, live *armcs.AgentPool) error
	drainPoolFn              func(ctx context.Context, pool string, timeout time.Duration) error
	deletePoolFn             func(ctx context.Context, pool string) error
	recreateSystemFn         func(ctx context.Context, live *armcs.AgentPool) error
	reconcileTagPutFn        func(ctx context.Context) error
	triggerSystemReconcileFn func(ctx context.Context, live *armcs.AgentPool) error
	pollForNRPEvidenceFn     func(ctx context.Context, timeout time.Duration, pollInterval time.Duration, windowMin int, threshold int) (int, error)
	abortSystemReconcileFn   func(ctx context.Context) error
	restorePoolSpecFn        func(ctx context.Context, target nodePoolTarget, live *armcs.AgentPool) error
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
			if strings.Contains(reason, "NRP-KVS storm FAIL") {
				return []nodePoolTarget{{name: "system", vmssPrefix: poolVMSSPrefix("system"), suspected: true}}, reason, nil
			}
			return nil, reason, err
		}
	}
	return []nodePoolTarget{{name: "system", vmssPrefix: poolVMSSPrefix("system"), nrpFailures: 10}}, "", nil
}

func (m *mockOrchestrator) dumpPreflight(ctx context.Context) error {
	m.record("dumpPreflight")
	return nil
}

func (m *mockOrchestrator) dumpPostflight(ctx context.Context) error {
	m.record("dumpPostflight")
	return nil
}

func (m *mockOrchestrator) preflightChecks(ctx context.Context, targets []nodePoolTarget) error {
	m.record("preflightChecks")
	if m.preflightChecksFn != nil {
		return m.preflightChecksFn(ctx)
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

func (m *mockOrchestrator) triggerPoolReconcile(ctx context.Context, target nodePoolTarget, live *armcs.AgentPool) error {
	if target.name == "system" {
		m.record("triggerSystemReconcile")
	} else {
		m.record("triggerPoolReconcile:" + target.name)
	}
	if m.triggerSystemReconcileFn != nil {
		return m.triggerSystemReconcileFn(ctx, live)
	}
	return nil
}

func (m *mockOrchestrator) pollForNRPEvidence(ctx context.Context, target nodePoolTarget, timeout time.Duration, pollInterval time.Duration, windowMin int, threshold int) (int, error) {
	m.record("pollForNRPEvidence")
	if m.pollForNRPEvidenceFn != nil {
		return m.pollForNRPEvidenceFn(ctx, timeout, pollInterval, windowMin, threshold)
	}
	return threshold, nil
}

func (m *mockOrchestrator) abortPoolReconcile(ctx context.Context, poolName string) error {
	if poolName == "system" {
		m.record("abortSystemReconcile")
	} else {
		m.record("abortPoolReconcile:" + poolName)
	}
	if m.abortSystemReconcileFn != nil {
		return m.abortSystemReconcileFn(ctx)
	}
	return nil
}

func (m *mockOrchestrator) restorePoolSpec(ctx context.Context, target nodePoolTarget, live *armcs.AgentPool) error {
	m.record("restorePoolSpec:" + target.name)
	if m.restorePoolSpecFn != nil {
		return m.restorePoolSpecFn(ctx, target, live)
	}
	return nil
}

func TestRunWith(t *testing.T) {
	dummyErr := errors.New("boom")
	tmpSystem := tempPoolName("system")

	fullHappyPath := []string{
		"ensureCluster", "dumpPreflight", "detect:1",
		"bootstrapKube", "dumpPreflight", "preflightChecks",
		"snapshotSystem", "maybeAbortLRO", "detect:2", "snapshotSystem",
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
			name: "guard1_fail_dry_run_skips_forced_evidence",
			cfg:  &config{clusterName: "c", resourceGroup: "rg", subscriptionID: "sub", cpVersion: "1.30.0", dryRun: true},
			setup: func(m *mockOrchestrator) {
				m.detectFn = func(_ context.Context, _ int) (bool, string, error) {
					return false, "NRP-KVS storm FAIL: only 0 NRP failures < 10", nil
				}
			},
			wantCalls: []string{"ensureCluster", "dumpPreflight", "detect:1"},
		},
		{
			name: "guard1_fail_forced_evidence_inconclusive",
			cfg:  &config{clusterName: "c", resourceGroup: "rg", subscriptionID: "sub", cpVersion: "1.30.0", threshold: 10, forcedEvidenceTimeoutMin: 20, forcedEvidenceThreshold: 3},
			setup: func(m *mockOrchestrator) {
				m.detectFn = func(_ context.Context, _ int) (bool, string, error) {
					return false, "NRP-KVS storm FAIL: only 0 NRP failures < 10", nil
				}
				m.pollForNRPEvidenceFn = func(context.Context, time.Duration, time.Duration, int, int) (int, error) {
					return 2, nil
				}
			},
			wantCalls: []string{
				"ensureCluster", "dumpPreflight", "detect:1",
				"snapshotSystem", "triggerSystemReconcile", "pollForNRPEvidence", "abortSystemReconcile", "restorePoolSpec:system",
			},
		},
		{
			name: "guard1_fail_forced_evidence_confirms_nrp",
			cfg:  &config{clusterName: "c", resourceGroup: "rg", subscriptionID: "sub", cpVersion: "1.30.0", threshold: 10, forcedEvidenceTimeoutMin: 20, forcedEvidenceThreshold: 3},
			setup: func(m *mockOrchestrator) {
				m.detectFn = func(_ context.Context, n int) (bool, string, error) {
					if n == 1 {
						return false, "NRP-KVS storm FAIL: only 0 NRP failures < 10", nil
					}
					return true, "", nil
				}
				m.pollForNRPEvidenceFn = func(context.Context, time.Duration, time.Duration, int, int) (int, error) {
					return 12, nil
				}
			},
			wantCalls: []string{
				"ensureCluster", "dumpPreflight", "detect:1",
				"snapshotSystem", "triggerSystemReconcile", "pollForNRPEvidence", "abortSystemReconcile", "restorePoolSpec:system",
				"bootstrapKube", "dumpPreflight", "preflightChecks",
				"snapshotSystem", "maybeAbortLRO", "detect:2", "snapshotSystem",
				"addTempPool:system",
				"drainPool:system", "deletePool:system",
				"recreateSystem",
				"drainPool:" + tmpSystem, "deletePool:" + tmpSystem,
				"reconcileTagPut", "dumpPostflight",
			},
		},
		{
			name: "guard1_fail_trigger_failure_exits_noop",
			cfg:  &config{clusterName: "c", resourceGroup: "rg", subscriptionID: "sub", cpVersion: "1.30.0", threshold: 10},
			setup: func(m *mockOrchestrator) {
				m.detectFn = func(_ context.Context, _ int) (bool, string, error) {
					return false, "NRP-KVS storm FAIL: only 0 NRP failures < 10", nil
				}
				m.triggerSystemReconcileFn = func(context.Context, *armcs.AgentPool) error {
					return errors.New("conflict")
				}
			},
			wantCalls: []string{
				"ensureCluster", "dumpPreflight", "detect:1",
				"snapshotSystem", "triggerSystemReconcile",
			},
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
				"bootstrapKube", "dumpPreflight", "preflightChecks",
				"snapshotSystem", "maybeAbortLRO",
			},
		},
		{
			// SKIP_GUARDS must not turn an empty confirmed-target list
			// into a destructive run. Even though SKIP_GUARDS=true, the
			// forced-evidence path is skipped (gated by !skipGuards), so
			// targets = confirmed = []. We must exit no-op before Step 2
			// (maybeAbortLRO) and Step 8 (reconcileTagPut) fire.
			name: "skip_guards_pre_lro_empty_targets_exits_noop",
			cfg:  &config{clusterName: "c", resourceGroup: "rg", subscriptionID: "sub", cpVersion: "1.30.0", skipGuards: true},
			setup: func(m *mockOrchestrator) {
				m.detectFn = func(_ context.Context, _ int) (bool, string, error) {
					// NRP-KVS storm FAIL injects a suspected target via the
					// mock, but with skipGuards=true the forced-evidence
					// probe is skipped, so confirmed stays empty.
					return false, "NRP-KVS storm FAIL: only 0 NRP failures < 10", nil
				}
			},
			wantCalls: []string{"ensureCluster", "dumpPreflight", "detect:1"},
		},
		{
			// Same protection at the post-LRO recheck gate: detect:1
			// confirms a target so we reach Step 2; detect:2 returns
			// only a suspected target which gets filtered out. With
			// skipGuards=true we must still exit before reconcileTagPut.
			name: "skip_guards_post_lro_empty_targets_exits_noop",
			cfg:  &config{clusterName: "c", resourceGroup: "rg", subscriptionID: "sub", cpVersion: "1.30.0", skipGuards: true},
			setup: func(m *mockOrchestrator) {
				m.detectFn = func(_ context.Context, n int) (bool, string, error) {
					if n == 1 {
						return true, "", nil
					}
					return false, "NRP-KVS storm FAIL after LRO", nil
				}
			},
			wantCalls: []string{
				"ensureCluster", "dumpPreflight", "detect:1",
				"bootstrapKube", "dumpPreflight", "preflightChecks",
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
				"bootstrapKube", "dumpPreflight", "preflightChecks",
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
				"bootstrapKube", "dumpPreflight", "preflightChecks",
				"snapshotSystem", "maybeAbortLRO", "detect:2", "snapshotSystem",
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
				"bootstrapKube", "dumpPreflight", "preflightChecks",
				"snapshotSystem", "maybeAbortLRO", "detect:2", "snapshotSystem",
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
