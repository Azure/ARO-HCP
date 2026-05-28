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

package slotmanager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
)

func TestResolveLeasedSlot(t *testing.T) {
	t.Parallel()

	opts := &AcquireOptions{
		completedAcquireOptions: &completedAcquireOptions{
			PoolEnvironment: "dev",
		},
	}
	pool := slots.Pool{
		SubscriptionName:        "dev-subscription",
		Region:                  "westus3",
		ResourceType:            "aro-hcp-dev-westus3-slot",
		SlotCount:               2,
		IdentityContainerPrefix: "aro-hcp-msi-container-dev",
		IdentityContainerCount:  1,
	}

	slot, err := opts.ResolveLeasedSlot(pool, "aro-hcp-dev-westus3-slot-01")
	if err != nil {
		t.Fatalf("expected leased slot resolution to succeed: %v", err)
	}
	if slot.SlotIndex != 1 {
		t.Fatalf("expected slot index 1, got %d", slot.SlotIndex)
	}
}

func TestResolveLeasedSlotRejectsResourceOutsideSelectedPool(t *testing.T) {
	t.Parallel()

	opts := &AcquireOptions{
		completedAcquireOptions: &completedAcquireOptions{
			PoolEnvironment: "dev",
		},
	}
	pool := slots.Pool{
		SubscriptionName:        "dev-subscription",
		Region:                  "westus3",
		ResourceType:            "aro-hcp-dev-westus3-slot",
		SlotCount:               2,
		IdentityContainerPrefix: "aro-hcp-msi-container-dev",
		IdentityContainerCount:  1,
	}

	_, err := opts.ResolveLeasedSlot(pool, "aro-hcp-prod-uksouth-slot-00")
	if err == nil {
		t.Fatal("expected leased slot resolution to fail for a resource outside the selected pool")
	}
	if !strings.Contains(err.Error(), "not part of selected pool") {
		t.Fatalf("expected selected pool mismatch error, got %v", err)
	}
}

func TestAcquireCompleteRegionModeMatrix(t *testing.T) {
	fixedRegion := "westus3"
	alternateRegion := "eastus2"

	type testCase struct {
		regionMode         string
		allowedLocations   string
		override           string
		wantRuntimeRegion  string
		wantPoolRegion     string
		wantErrorSubstring string
	}

	cases := []testCase{
		{
			regionMode:        slots.RegionModeFixed,
			allowedLocations:  "",
			override:          "",
			wantRuntimeRegion: fixedRegion,
			wantPoolRegion:    fixedRegion,
		},
		{
			regionMode:        slots.RegionModeFixed,
			allowedLocations:  fixedRegion,
			override:          "",
			wantRuntimeRegion: fixedRegion,
			wantPoolRegion:    fixedRegion,
		},
		{
			regionMode:        slots.RegionModeFixed,
			allowedLocations:  strings.Join([]string{fixedRegion, alternateRegion}, ","),
			override:          "",
			wantRuntimeRegion: fixedRegion,
			wantPoolRegion:    fixedRegion,
		},
		{
			regionMode:         slots.RegionModeFixed,
			allowedLocations:   alternateRegion,
			override:           "",
			wantErrorSubstring: "no pool found",
		},
		{
			regionMode:        slots.RegionModeFixed,
			allowedLocations:  fixedRegion,
			override:          fixedRegion,
			wantRuntimeRegion: fixedRegion,
			wantPoolRegion:    fixedRegion,
		},
		{
			regionMode:         slots.RegionModeFixed,
			allowedLocations:   fixedRegion,
			override:           alternateRegion,
			wantErrorSubstring: "no pool found",
		},
		{
			regionMode:        slots.RegionModeRuntimeSelected,
			allowedLocations:  "",
			override:          "",
			wantRuntimeRegion: fixedRegion,
			wantPoolRegion:    fixedRegion,
		},
		{
			regionMode:        slots.RegionModeRuntimeSelected,
			allowedLocations:  fixedRegion,
			override:          "",
			wantRuntimeRegion: fixedRegion,
			wantPoolRegion:    fixedRegion,
		},
		{
			regionMode:        slots.RegionModeRuntimeSelected,
			allowedLocations:  alternateRegion,
			override:          "",
			wantRuntimeRegion: fixedRegion,
			wantPoolRegion:    fixedRegion,
		},
		{
			regionMode:        slots.RegionModeRuntimeSelected,
			allowedLocations:  strings.Join([]string{fixedRegion, alternateRegion}, ","),
			override:          "",
			wantRuntimeRegion: fixedRegion,
			wantPoolRegion:    fixedRegion,
		},
		{
			regionMode:        slots.RegionModeRuntimeSelected,
			allowedLocations:  "",
			override:          alternateRegion,
			wantRuntimeRegion: alternateRegion,
			wantPoolRegion:    fixedRegion,
		},
		{
			regionMode:        slots.RegionModeRuntimeSelected,
			allowedLocations:  fixedRegion,
			override:          alternateRegion,
			wantRuntimeRegion: alternateRegion,
			wantPoolRegion:    fixedRegion,
		},
	}

	for _, tc := range cases {
		testName := fmt.Sprintf(
			"regionMode=%s/allowedLocations=%q/override=%q",
			tc.regionMode,
			tc.allowedLocations,
			tc.override,
		)
		t.Run(testName, func(t *testing.T) {
			clusterProfileDir := writeAcquireTestClusterProfile(t, "ARO HCP E2E Hosted Clusters (EA Subscription)")
			catalogPath := writeAcquireTestCatalog(t, tc.regionMode, fixedRegion)

			t.Setenv("CLUSTER_PROFILE_DIR", clusterProfileDir)
			t.Setenv("ARO_HCP_DEPLOY_ENV", "ci01")
			t.Setenv("SHARED_DIR", t.TempDir())
			t.Setenv("LEASE_PROXY_SERVER_URL", "http://lease-proxy.example.com")
			t.Setenv("ALLOWED_LOCATIONS", tc.allowedLocations)
			t.Setenv("MULTISTAGE_PARAM_OVERRIDE_LOCATION", tc.override)

			opts := DefaultAcquireOptions()
			opts.CatalogPath = catalogPath

			validated, err := opts.Validate()
			if err != nil {
				t.Fatalf("expected options validation to succeed: %v", err)
			}

			completed, err := validated.Complete(context.Background())
			if tc.wantErrorSubstring != "" {
				if err == nil {
					t.Fatalf("expected completion to fail with %q", tc.wantErrorSubstring)
				}
				if !strings.Contains(err.Error(), tc.wantErrorSubstring) {
					t.Fatalf("expected completion error to contain %q, got %v", tc.wantErrorSubstring, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("expected completion to succeed: %v", err)
			}

			if got := len(completed.CandidatePools); got != 1 {
				t.Fatalf("expected exactly one candidate pool, got %d", got)
			}
			if got := completed.runtimeRegionForPool(completed.CandidatePools[0]); got != tc.wantRuntimeRegion {
				t.Fatalf("expected runtime region %q, got %q", tc.wantRuntimeRegion, got)
			}
			if completed.CandidatePools[0].Region != tc.wantPoolRegion {
				t.Fatalf("expected selected pool region %q, got %q", tc.wantPoolRegion, completed.CandidatePools[0].Region)
			}
		})
	}
}

func TestDefaultAcquireOptionsLeaseWaitDefaults(t *testing.T) {
	t.Parallel()

	opts := DefaultAcquireOptions()
	if opts.LeaseProxyTimeout != slots.DefaultLeaseProxyTimeout {
		t.Fatalf("expected default lease proxy timeout %s, got %s", slots.DefaultLeaseProxyTimeout, opts.LeaseProxyTimeout)
	}
	if opts.LeaseWaitInterval != DefaultLeaseWaitInterval {
		t.Fatalf("expected default lease wait interval %s, got %s", DefaultLeaseWaitInterval, opts.LeaseWaitInterval)
	}
	if opts.MaxWaitForLease != DefaultMaxWaitForLease {
		t.Fatalf("expected default max wait for lease %s, got %s", DefaultMaxWaitForLease, opts.MaxWaitForLease)
	}
}

func TestDefaultAcquireOptionsSelectorDefaults(t *testing.T) {
	t.Setenv("ALLOWED_SUBSCRIPTIONS", "dev-sub-a, dev-sub-b, dev-sub-a")
	t.Setenv("ALLOWED_LOCATIONS", "centralus, eastus2")

	opts := DefaultAcquireOptions()

	if got, want := opts.AllowedSubscriptions, []string{"dev-sub-a", "dev-sub-b"}; !equalStrings(got, want) {
		t.Fatalf("unexpected allowed subscriptions: got %v want %v", got, want)
	}
	if got, want := opts.AllowedLocations, []string{"centralus", "eastus2"}; !equalStrings(got, want) {
		t.Fatalf("unexpected allowed locations: got %v want %v", got, want)
	}
	if opts.SelectedLocation != "" {
		t.Fatalf("expected empty selected location by default, got %q", opts.SelectedLocation)
	}
}

func TestDefaultAcquireOptionsDoesNotUseLegacyLocationForSelection(t *testing.T) {
	t.Setenv("LOCATION", "westus3")

	opts := DefaultAcquireOptions()

	if got := len(opts.AllowedLocations); got != 0 {
		t.Fatalf("expected legacy LOCATION to be ignored for acquire-side selection, got %v", opts.AllowedLocations)
	}
}

func TestDefaultAcquireOptionsOverrideSuppressesAllowedLocationDefault(t *testing.T) {
	t.Setenv("ALLOWED_LOCATIONS", "centralus, eastus2")
	t.Setenv("MULTISTAGE_PARAM_OVERRIDE_LOCATION", "westus3")

	opts := DefaultAcquireOptions()

	if opts.SelectedLocation != "westus3" {
		t.Fatalf("expected selected location %q, got %q", "westus3", opts.SelectedLocation)
	}
	if got := len(opts.AllowedLocations); got != 0 {
		t.Fatalf("expected allowed locations to be ignored when override is set, got %v", opts.AllowedLocations)
	}
}

func TestAcquireRunFixedModeFallsBackWithinRequestedRegion(t *testing.T) {
	t.Parallel()

	clusterProfileDir := writeAcquireTestClusterProfiles(t, "dev-sub-b")
	catalogPath := writeAcquireTestCatalogFromYAML(t, `version: 1
environments:
  dev:
    deploy_envs: [prow, ci01]
    pools:
      - subscription_name: dev-sub-a
        region: centralus
        region_mode: fixed
        resource_type: aro-hcp-dev-centralus-a-slot
        slot_count: 1
        identity_container_prefix: aro-hcp-msi-container-dev-a
        identity_container_count: 2
      - subscription_name: dev-sub-b
        region: centralus
        region_mode: fixed
        resource_type: aro-hcp-dev-centralus-b-slot
        slot_count: 1
        identity_container_prefix: aro-hcp-msi-container-dev-b
        identity_container_count: 2
      - subscription_name: dev-sub-c
        region: canadacentral
        region_mode: fixed
        resource_type: aro-hcp-dev-canadacentral-c-slot
        slot_count: 1
        identity_container_prefix: aro-hcp-msi-container-dev-c
        identity_container_count: 2
`)

	server, acquireCalls, releaseCalls := newTestLeaseProxyServer(t, map[string][]leaseProxyReply{
		"aro-hcp-dev-centralus-a-slot": {
			unavailableAcquireReply("aro-hcp-dev-centralus-a-slot"),
		},
		"aro-hcp-dev-centralus-b-slot": {
			successAcquireReply("aro-hcp-dev-centralus-b-slot-00"),
		},
	})
	defer server.Close()

	sharedDir := t.TempDir()
	err := Acquire(context.Background(), &RawAcquireOptions{
		ClusterProfileDir:   clusterProfileDir,
		DeployEnv:           "ci01",
		AllowedLocations:    []string{"centralus"},
		SharedDir:           sharedDir,
		CatalogPath:         catalogPath,
		LeaseProxyServerURL: server.URL,
		LeaseProxyTimeout:   50 * time.Millisecond,
		MaxWaitForLease:     DefaultMaxWaitForLease,
		LeaseWaitInterval:   DefaultLeaseWaitInterval,
	})
	if err != nil {
		t.Fatalf("expected acquire to succeed: %v", err)
	}

	if got, want := *acquireCalls, []string{"aro-hcp-dev-centralus-a-slot", "aro-hcp-dev-centralus-b-slot"}; !equalStrings(got, want) {
		t.Fatalf("unexpected acquire call order: got %v want %v", got, want)
	}
	if got := *releaseCalls; len(got) != 0 {
		t.Fatalf("expected no release calls, got %v", got)
	}

	state, err := slots.LoadAcquiredSlotState(sharedDir)
	if err != nil {
		t.Fatalf("expected acquired slot state to load: %v", err)
	}
	if state.Slot.ResourceType != "aro-hcp-dev-centralus-b-slot" {
		t.Fatalf("expected fallback pool resource type, got %q", state.Slot.ResourceType)
	}
	if state.RuntimeRegion != "centralus" {
		t.Fatalf("expected runtime region %q, got %q", "centralus", state.RuntimeRegion)
	}

	envFile, err := slots.EnvFile(sharedDir)
	if err != nil {
		t.Fatalf("expected env file path: %v", err)
	}
	envContents, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("expected env file read to succeed: %v", err)
	}
	if !strings.Contains(string(envContents), `export CUSTOMER_SUBSCRIPTION='dev-sub-b'`) {
		t.Fatalf("expected winning subscription in env file, got:\n%s", string(envContents))
	}
}

func TestAcquireRunFallsBackWhenCandidatePoolTimesOut(t *testing.T) {
	t.Parallel()

	clusterProfileDir := writeAcquireTestClusterProfiles(t, "dev-sub-b")
	catalogPath := writeAcquireTestCatalogFromYAML(t, `version: 1
environments:
  dev:
    deploy_envs: [ci01]
    pools:
      - subscription_name: dev-sub-a
        region: centralus
        region_mode: fixed
        resource_type: aro-hcp-dev-centralus-a-slot
        slot_count: 1
        identity_container_prefix: aro-hcp-msi-container-dev-a
        identity_container_count: 2
      - subscription_name: dev-sub-b
        region: centralus
        region_mode: fixed
        resource_type: aro-hcp-dev-centralus-b-slot
        slot_count: 1
        identity_container_prefix: aro-hcp-msi-container-dev-b
        identity_container_count: 2
`)

	server, acquireCalls, releaseCalls := newTestLeaseProxyServer(t, map[string][]leaseProxyReply{
		"aro-hcp-dev-centralus-a-slot": {
			delayedLeaseProxyReply(200*time.Millisecond, successAcquireReply("aro-hcp-dev-centralus-a-slot-00")),
		},
		"aro-hcp-dev-centralus-b-slot": {
			successAcquireReply("aro-hcp-dev-centralus-b-slot-00"),
		},
	})
	defer server.Close()

	sharedDir := t.TempDir()
	err := Acquire(context.Background(), &RawAcquireOptions{
		ClusterProfileDir:   clusterProfileDir,
		DeployEnv:           "ci01",
		AllowedLocations:    []string{"centralus"},
		SharedDir:           sharedDir,
		CatalogPath:         catalogPath,
		LeaseProxyServerURL: server.URL,
		LeaseProxyTimeout:   50 * time.Millisecond,
		MaxWaitForLease:     DefaultMaxWaitForLease,
		LeaseWaitInterval:   DefaultLeaseWaitInterval,
	})
	if err != nil {
		t.Fatalf("expected acquire to fall back after timeout: %v", err)
	}

	if got, want := *acquireCalls, []string{"aro-hcp-dev-centralus-a-slot", "aro-hcp-dev-centralus-b-slot"}; !equalStrings(got, want) {
		t.Fatalf("unexpected acquire call order: got %v want %v", got, want)
	}
	if got := *releaseCalls; len(got) != 0 {
		t.Fatalf("expected no release calls, got %v", got)
	}

	state, err := slots.LoadAcquiredSlotState(sharedDir)
	if err != nil {
		t.Fatalf("expected acquired slot state to load: %v", err)
	}
	if state.Slot.ResourceType != "aro-hcp-dev-centralus-b-slot" {
		t.Fatalf("expected fallback pool resource type, got %q", state.Slot.ResourceType)
	}
}

func TestAcquireRunRuntimeSelectedFallsBackAcrossSubscriptions(t *testing.T) {
	t.Parallel()

	clusterProfileDir := writeAcquireTestClusterProfiles(t, "prod-sub-b")
	catalogPath := writeAcquireTestCatalogFromYAML(t, `version: 1
environments:
  prod:
    deploy_envs: [prod]
    pools:
      - subscription_name: prod-sub-a
        region: uksouth
        region_mode: runtime-selected
        resource_type: aro-hcp-prod-shard0-slot
        slot_count: 1
        identity_container_prefix: aro-hcp-msi-container-prod-a
        identity_container_count: 2
      - subscription_name: prod-sub-b
        region: uksouth
        region_mode: runtime-selected
        resource_type: aro-hcp-prod-shard1-slot
        slot_count: 1
        identity_container_prefix: aro-hcp-msi-container-prod-b
        identity_container_count: 2
`)

	server, acquireCalls, _ := newTestLeaseProxyServer(t, map[string][]leaseProxyReply{
		"aro-hcp-prod-shard0-slot": {
			unavailableAcquireReply("aro-hcp-prod-shard0-slot"),
		},
		"aro-hcp-prod-shard1-slot": {
			successAcquireReply("aro-hcp-prod-shard1-slot-00"),
		},
	})
	defer server.Close()

	sharedDir := t.TempDir()
	err := Acquire(context.Background(), &RawAcquireOptions{
		ClusterProfileDir:   clusterProfileDir,
		DeployEnv:           "prod",
		SelectedLocation:    "eastus2",
		SharedDir:           sharedDir,
		CatalogPath:         catalogPath,
		LeaseProxyServerURL: server.URL,
		LeaseProxyTimeout:   50 * time.Millisecond,
		MaxWaitForLease:     DefaultMaxWaitForLease,
		LeaseWaitInterval:   DefaultLeaseWaitInterval,
	})
	if err != nil {
		t.Fatalf("expected acquire to succeed: %v", err)
	}

	if got, want := *acquireCalls, []string{"aro-hcp-prod-shard0-slot", "aro-hcp-prod-shard1-slot"}; !equalStrings(got, want) {
		t.Fatalf("unexpected acquire call order: got %v want %v", got, want)
	}

	state, err := slots.LoadAcquiredSlotState(sharedDir)
	if err != nil {
		t.Fatalf("expected acquired slot state to load: %v", err)
	}
	if state.Slot.ResourceType != "aro-hcp-prod-shard1-slot" {
		t.Fatalf("expected fallback pool resource type, got %q", state.Slot.ResourceType)
	}
	if state.RuntimeRegion != "eastus2" {
		t.Fatalf("expected runtime region %q, got %q", "eastus2", state.RuntimeRegion)
	}
	if state.Slot.Region != "uksouth" {
		t.Fatalf("expected slot catalog region %q, got %q", "uksouth", state.Slot.Region)
	}
}

func TestAcquireRunStopsOnHardFailure(t *testing.T) {
	t.Parallel()

	clusterProfileDir := writeAcquireTestClusterProfiles(t, "dev-sub-b")
	catalogPath := writeAcquireTestCatalogFromYAML(t, `version: 1
environments:
  dev:
    deploy_envs: [ci01]
    pools:
      - subscription_name: dev-sub-a
        region: centralus
        region_mode: fixed
        resource_type: aro-hcp-dev-centralus-a-slot
        slot_count: 1
        identity_container_prefix: aro-hcp-msi-container-dev-a
        identity_container_count: 2
      - subscription_name: dev-sub-b
        region: centralus
        region_mode: fixed
        resource_type: aro-hcp-dev-centralus-b-slot
        slot_count: 1
        identity_container_prefix: aro-hcp-msi-container-dev-b
        identity_container_count: 2
`)

	server, acquireCalls, _ := newTestLeaseProxyServer(t, map[string][]leaseProxyReply{
		"aro-hcp-dev-centralus-a-slot": {
			{statusCode: http.StatusNotFound, body: "Failed to acquire lease \"aro-hcp-dev-centralus-a-slot\": resource type not found\n"},
		},
		"aro-hcp-dev-centralus-b-slot": {
			successAcquireReply("aro-hcp-dev-centralus-b-slot-00"),
		},
	})
	defer server.Close()

	sharedDir := t.TempDir()
	err := Acquire(context.Background(), &RawAcquireOptions{
		ClusterProfileDir:   clusterProfileDir,
		DeployEnv:           "ci01",
		AllowedLocations:    []string{"centralus"},
		SharedDir:           sharedDir,
		CatalogPath:         catalogPath,
		LeaseProxyServerURL: server.URL,
		LeaseProxyTimeout:   5 * time.Second,
		MaxWaitForLease:     DefaultMaxWaitForLease,
		LeaseWaitInterval:   DefaultLeaseWaitInterval,
	})
	if err == nil {
		t.Fatal("expected acquire to fail on hard lease-proxy error")
	}
	if got, want := *acquireCalls, []string{"aro-hcp-dev-centralus-a-slot"}; !equalStrings(got, want) {
		t.Fatalf("unexpected acquire call order: got %v want %v", got, want)
	}
	if _, err := slots.LoadAcquiredSlotState(sharedDir); err == nil {
		t.Fatal("expected no acquired slot state to be written on hard failure")
	}
}

func TestAcquireRunRetriesAfterFullUnavailablePassAcrossPools(t *testing.T) {
	t.Parallel()

	clusterProfileDir := writeAcquireTestClusterProfiles(t, "dev-sub-b")
	catalogPath := writeAcquireTestCatalogFromYAML(t, `version: 1
environments:
  dev:
    deploy_envs: [ci01]
    pools:
      - subscription_name: dev-sub-a
        region: centralus
        region_mode: fixed
        resource_type: aro-hcp-dev-centralus-a-slot
        slot_count: 1
        identity_container_prefix: aro-hcp-msi-container-dev-a
        identity_container_count: 2
      - subscription_name: dev-sub-b
        region: centralus
        region_mode: fixed
        resource_type: aro-hcp-dev-centralus-b-slot
        slot_count: 1
        identity_container_prefix: aro-hcp-msi-container-dev-b
        identity_container_count: 2
`)

	server, acquireCalls, _ := newTestLeaseProxyServer(t, map[string][]leaseProxyReply{
		"aro-hcp-dev-centralus-a-slot": repeatLeaseProxyReply(unavailableAcquireReply("aro-hcp-dev-centralus-a-slot"), 2),
		"aro-hcp-dev-centralus-b-slot": {
			unavailableAcquireReply("aro-hcp-dev-centralus-b-slot"),
			successAcquireReply("aro-hcp-dev-centralus-b-slot-00"),
		},
	})
	defer server.Close()

	completed := completeAcquireForTest(t, &RawAcquireOptions{
		ClusterProfileDir:   clusterProfileDir,
		DeployEnv:           "ci01",
		AllowedLocations:    []string{"centralus"},
		SharedDir:           t.TempDir(),
		CatalogPath:         catalogPath,
		LeaseProxyServerURL: server.URL,
		LeaseProxyTimeout:   50 * time.Millisecond,
		MaxWaitForLease:     2 * time.Minute,
		LeaseWaitInterval:   1 * time.Minute,
	})
	clock := newFakeClock(time.Unix(0, 0))
	completed.Now = clock.Now
	completed.Sleep = clock.Sleep

	if err := completed.Run(context.Background()); err != nil {
		t.Fatalf("expected acquire run to succeed after a retry pass: %v", err)
	}
	if got, want := *acquireCalls, []string{
		"aro-hcp-dev-centralus-a-slot",
		"aro-hcp-dev-centralus-b-slot",
		"aro-hcp-dev-centralus-a-slot",
		"aro-hcp-dev-centralus-b-slot",
	}; !equalStrings(got, want) {
		t.Fatalf("unexpected acquire call order: got %v want %v", got, want)
	}
	if got, want := clock.slept, []time.Duration{1 * time.Minute}; !equalDurations(got, want) {
		t.Fatalf("unexpected sleep durations: got %v want %v", got, want)
	}
}

func TestAcquireRunRetriesAfterFullUnavailablePassAfterRetryableServerFailure(t *testing.T) {
	t.Parallel()

	clusterProfileDir := writeAcquireTestClusterProfiles(t, "dev-sub-a")
	catalogPath := writeAcquireTestCatalogFromYAML(t, `version: 1
environments:
  dev:
    deploy_envs: [ci01]
    pools:
      - subscription_name: dev-sub-a
        region: centralus
        region_mode: fixed
        resource_type: aro-hcp-dev-centralus-a-slot
        slot_count: 1
        identity_container_prefix: aro-hcp-msi-container-dev-a
        identity_container_count: 2
`)

	server, acquireCalls, _ := newTestLeaseProxyServer(t, map[string][]leaseProxyReply{
		"aro-hcp-dev-centralus-a-slot": {
			{statusCode: http.StatusServiceUnavailable},
			successAcquireReply("aro-hcp-dev-centralus-a-slot-00"),
		},
	})
	defer server.Close()

	completed := completeAcquireForTest(t, &RawAcquireOptions{
		ClusterProfileDir:   clusterProfileDir,
		DeployEnv:           "ci01",
		AllowedLocations:    []string{"centralus"},
		SharedDir:           t.TempDir(),
		CatalogPath:         catalogPath,
		LeaseProxyServerURL: server.URL,
		LeaseProxyTimeout:   50 * time.Millisecond,
		MaxWaitForLease:     2 * time.Minute,
		LeaseWaitInterval:   1 * time.Minute,
	})
	clock := newFakeClock(time.Unix(0, 0))
	completed.Now = clock.Now
	completed.Sleep = clock.Sleep

	if err := completed.Run(context.Background()); err != nil {
		t.Fatalf("expected acquire run to succeed after an unavailable pass retry: %v", err)
	}
	if got, want := *acquireCalls, []string{
		"aro-hcp-dev-centralus-a-slot",
		"aro-hcp-dev-centralus-a-slot",
	}; !equalStrings(got, want) {
		t.Fatalf("unexpected acquire call order: got %v want %v", got, want)
	}
	if got, want := clock.slept, []time.Duration{1 * time.Minute}; !equalDurations(got, want) {
		t.Fatalf("unexpected sleep durations: got %v want %v", got, want)
	}
}

func TestAcquireRunStopsAfterMaxWaitForLease(t *testing.T) {
	t.Parallel()

	clusterProfileDir := writeAcquireTestClusterProfiles(t, "dev-sub-a")
	catalogPath := writeAcquireTestCatalogFromYAML(t, `version: 1
environments:
  dev:
    deploy_envs: [ci01]
    pools:
      - subscription_name: dev-sub-a
        region: centralus
        region_mode: fixed
        resource_type: aro-hcp-dev-centralus-a-slot
        slot_count: 1
        identity_container_prefix: aro-hcp-msi-container-dev-a
        identity_container_count: 2
`)

	server, acquireCalls, _ := newTestLeaseProxyServer(t, map[string][]leaseProxyReply{
		"aro-hcp-dev-centralus-a-slot": repeatLeaseProxyReply(unavailableAcquireReply("aro-hcp-dev-centralus-a-slot"), 4),
	})
	defer server.Close()

	completed := completeAcquireForTest(t, &RawAcquireOptions{
		ClusterProfileDir:   clusterProfileDir,
		DeployEnv:           "ci01",
		AllowedLocations:    []string{"centralus"},
		SharedDir:           t.TempDir(),
		CatalogPath:         catalogPath,
		LeaseProxyServerURL: server.URL,
		LeaseProxyTimeout:   50 * time.Millisecond,
		MaxWaitForLease:     3 * time.Minute,
		LeaseWaitInterval:   1 * time.Minute,
	})
	clock := newFakeClock(time.Unix(0, 0))
	completed.Now = clock.Now
	completed.Sleep = clock.Sleep

	err := completed.Run(context.Background())
	if err == nil {
		t.Fatal("expected acquire run to fail after max wait budget is exhausted")
	}
	if !strings.Contains(err.Error(), `yielded an immediate lease for 3m0s across 4 full pass(es)`) {
		t.Fatalf("expected bounded no-immediate-lease error, got %v", err)
	}
	if got, want := *acquireCalls, []string{
		"aro-hcp-dev-centralus-a-slot",
		"aro-hcp-dev-centralus-a-slot",
		"aro-hcp-dev-centralus-a-slot",
		"aro-hcp-dev-centralus-a-slot",
	}; !equalStrings(got, want) {
		t.Fatalf("unexpected acquire call order: got %v want %v", got, want)
	}
	if got, want := clock.slept, []time.Duration{1 * time.Minute, 1 * time.Minute, 1 * time.Minute}; !equalDurations(got, want) {
		t.Fatalf("unexpected sleep durations: got %v want %v", got, want)
	}
}

func TestAcquireRunInfiniteWaitRespectsContextCancellation(t *testing.T) {
	t.Parallel()

	clusterProfileDir := writeAcquireTestClusterProfiles(t, "dev-sub-a")
	catalogPath := writeAcquireTestCatalogFromYAML(t, `version: 1
environments:
  dev:
    deploy_envs: [ci01]
    pools:
      - subscription_name: dev-sub-a
        region: centralus
        region_mode: fixed
        resource_type: aro-hcp-dev-centralus-a-slot
        slot_count: 1
        identity_container_prefix: aro-hcp-msi-container-dev-a
        identity_container_count: 2
`)

	server, acquireCalls, _ := newTestLeaseProxyServer(t, map[string][]leaseProxyReply{
		"aro-hcp-dev-centralus-a-slot": {
			unavailableAcquireReply("aro-hcp-dev-centralus-a-slot"),
		},
	})
	defer server.Close()

	completed := completeAcquireForTest(t, &RawAcquireOptions{
		ClusterProfileDir:   clusterProfileDir,
		DeployEnv:           "ci01",
		AllowedLocations:    []string{"centralus"},
		SharedDir:           t.TempDir(),
		CatalogPath:         catalogPath,
		LeaseProxyServerURL: server.URL,
		LeaseProxyTimeout:   50 * time.Millisecond,
		MaxWaitForLease:     0,
		LeaseWaitInterval:   1 * time.Minute,
	})

	ctx, cancel := context.WithCancel(context.Background())
	completed.Sleep = func(ctx context.Context, _ time.Duration) error {
		cancel()
		return ctx.Err()
	}

	err := completed.Run(ctx)
	if err == nil {
		t.Fatal("expected acquire run to stop when the parent context is cancelled")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if got, want := *acquireCalls, []string{"aro-hcp-dev-centralus-a-slot"}; !equalStrings(got, want) {
		t.Fatalf("unexpected acquire call order: got %v want %v", got, want)
	}
}

func TestAcquireRunReleasesLeaseOnSubscriptionResolutionFailure(t *testing.T) {
	t.Parallel()

	// Cluster profile deliberately does NOT contain the subscription name
	// used by the pool. This makes VerifyCustomerSubscriptionName fail
	// after the lease proxy has already granted a lease.
	clusterProfileDir := writeAcquireTestClusterProfiles(t, "some-other-subscription")
	catalogPath := writeAcquireTestCatalogFromYAML(t, `version: 1
environments:
  dev:
    deploy_envs: [ci01]
    pools:
      - subscription_name: dev-sub-a
        region: centralus
        region_mode: fixed
        resource_type: aro-hcp-dev-centralus-a-slot
        slot_count: 1
        identity_container_prefix: aro-hcp-msi-container-dev-a
        identity_container_count: 2
`)

	server, acquireCalls, releaseCalls := newTestLeaseProxyServer(t, map[string][]leaseProxyReply{
		"aro-hcp-dev-centralus-a-slot": {
			successAcquireReply("aro-hcp-dev-centralus-a-slot-00"),
		},
	})
	defer server.Close()

	sharedDir := t.TempDir()
	err := Acquire(context.Background(), &RawAcquireOptions{
		ClusterProfileDir:   clusterProfileDir,
		DeployEnv:           "ci01",
		AllowedLocations:    []string{"centralus"},
		SharedDir:           sharedDir,
		CatalogPath:         catalogPath,
		LeaseProxyServerURL: server.URL,
		LeaseProxyTimeout:   5 * time.Second,
		MaxWaitForLease:     DefaultMaxWaitForLease,
		LeaseWaitInterval:   DefaultLeaseWaitInterval,
	})
	if err == nil {
		t.Fatal("expected acquire to fail when customer subscription cannot be resolved")
	}
	if !strings.Contains(err.Error(), "no customer subscription name file matched") {
		t.Fatalf("expected subscription resolution error, got %v", err)
	}

	if got, want := *acquireCalls, []string{"aro-hcp-dev-centralus-a-slot"}; !equalStrings(got, want) {
		t.Fatalf("unexpected acquire call order: got %v want %v", got, want)
	}
	if got, want := *releaseCalls, []string{"aro-hcp-dev-centralus-a-slot-00"}; !equalStrings(got, want) {
		t.Fatalf("expected lease to be released after post-acquire failure: got %v want %v", got, want)
	}

	if _, err := slots.LoadAcquiredSlotState(sharedDir); err == nil {
		t.Fatal("expected no acquired slot state to be written on post-acquire failure")
	}
}

func TestAcquireRunKeepsLeaseWhenEnvFileWriteFailsAfterStateWrite(t *testing.T) {
	t.Parallel()

	clusterProfileDir := writeAcquireTestClusterProfiles(t, "dev-sub-a")
	catalogPath := writeAcquireTestCatalogFromYAML(t, `version: 1
environments:
  dev:
    deploy_envs: [ci01]
    pools:
      - subscription_name: dev-sub-a
        region: centralus
        region_mode: fixed
        resource_type: aro-hcp-dev-centralus-a-slot
        slot_count: 1
        identity_container_prefix: aro-hcp-msi-container-dev-a
        identity_container_count: 2
`)

	server, acquireCalls, releaseCalls := newTestLeaseProxyServer(t, map[string][]leaseProxyReply{
		"aro-hcp-dev-centralus-a-slot": {
			successAcquireReply("aro-hcp-dev-centralus-a-slot-00"),
		},
	})
	defer server.Close()

	sharedDir := t.TempDir()
	envFile, err := slots.EnvFile(sharedDir)
	if err != nil {
		t.Fatalf("expected env file path resolution to succeed: %v", err)
	}
	if err := os.Mkdir(envFile, 0o755); err != nil {
		t.Fatalf("expected env file path directory creation to succeed: %v", err)
	}

	err = Acquire(context.Background(), &RawAcquireOptions{
		ClusterProfileDir:   clusterProfileDir,
		DeployEnv:           "ci01",
		AllowedLocations:    []string{"centralus"},
		SharedDir:           sharedDir,
		CatalogPath:         catalogPath,
		LeaseProxyServerURL: server.URL,
		LeaseProxyTimeout:   5 * time.Second,
		MaxWaitForLease:     DefaultMaxWaitForLease,
		LeaseWaitInterval:   DefaultLeaseWaitInterval,
	})
	if err == nil {
		t.Fatal("expected acquire to fail when the env file cannot be written")
	}
	if !strings.Contains(err.Error(), "failed to write env file") {
		t.Fatalf("expected env file write error, got %v", err)
	}

	if got, want := *acquireCalls, []string{"aro-hcp-dev-centralus-a-slot"}; !equalStrings(got, want) {
		t.Fatalf("unexpected acquire call order: got %v want %v", got, want)
	}
	if got := *releaseCalls; len(got) != 0 {
		t.Fatalf("expected lease cleanup to be left to the release step once state exists, got %v", got)
	}

	state, err := slots.LoadAcquiredSlotState(sharedDir)
	if err != nil {
		t.Fatalf("expected acquired slot state to remain on disk: %v", err)
	}
	if state.LeasedResourceName != "aro-hcp-dev-centralus-a-slot-00" {
		t.Fatalf("unexpected leased resource name %q", state.LeasedResourceName)
	}
}

func writeAcquireTestCatalog(t *testing.T, regionMode, region string) string {
	t.Helper()

	return writeAcquireTestCatalogFromYAML(t, fmt.Sprintf(`version: 1
environments:
  dev:
    deploy_envs: [prow, ci01]
    pools:
      - subscription_name: "ARO HCP E2E Hosted Clusters (EA Subscription)"
        region: %s
        region_mode: %s
        resource_type: aro-hcp-dev-shard0-slot
        slot_count: 2
        identity_container_prefix: aro-hcp-msi-container-dev
        identity_container_count: 2
`, region, regionMode))
}

func writeAcquireTestCatalogFromYAML(t *testing.T, catalog string) string {
	t.Helper()

	catalogDir := t.TempDir()
	catalogPath := filepath.Join(catalogDir, "e2e-slots.yaml")
	if err := os.WriteFile(catalogPath, []byte(catalog), 0o644); err != nil {
		t.Fatalf("expected catalog write to succeed: %v", err)
	}
	return catalogPath
}

func writeAcquireTestClusterProfile(t *testing.T, subscriptionName string) string {
	t.Helper()
	return writeAcquireTestClusterProfiles(t, subscriptionName)
}

func writeAcquireTestClusterProfiles(t *testing.T, subscriptionNames ...string) string {
	t.Helper()

	clusterProfileDir := t.TempDir()
	for i, subscriptionName := range subscriptionNames {
		fileName := fmt.Sprintf("customer-shard%d-subscription-name", i)
		if err := os.WriteFile(filepath.Join(clusterProfileDir, fileName), []byte(subscriptionName), 0o644); err != nil {
			t.Fatalf("expected cluster profile write to succeed: %v", err)
		}
	}
	return clusterProfileDir
}

func completeAcquireForTest(t *testing.T, raw *RawAcquireOptions) *AcquireOptions {
	t.Helper()

	validated, err := raw.Validate()
	if err != nil {
		t.Fatalf("expected acquire options validation to succeed: %v", err)
	}
	completed, err := validated.Complete(context.Background())
	if err != nil {
		t.Fatalf("expected acquire options completion to succeed: %v", err)
	}
	return completed
}

type leaseProxyReply struct {
	statusCode int
	body       string
	delay      time.Duration
}

func unavailableAcquireReply(resourceType string) leaseProxyReply {
	return leaseProxyReply{
		statusCode: http.StatusInternalServerError,
		body:       fmt.Sprintf("Failed to acquire lease %q: resources not found\n", resourceType),
	}
}

func successAcquireReply(resourceName string) leaseProxyReply {
	body, err := json.Marshal(map[string]any{"names": []string{resourceName}})
	if err != nil {
		panic(err)
	}
	return leaseProxyReply{
		statusCode: http.StatusOK,
		body:       string(body),
	}
}

func delayedLeaseProxyReply(delay time.Duration, reply leaseProxyReply) leaseProxyReply {
	reply.delay = delay
	return reply
}

func repeatLeaseProxyReply(reply leaseProxyReply, count int) []leaseProxyReply {
	replies := make([]leaseProxyReply, 0, count)
	for i := 0; i < count; i++ {
		replies = append(replies, reply)
	}
	return replies
}

func newTestLeaseProxyServer(t *testing.T, acquireReplies map[string][]leaseProxyReply) (*httptest.Server, *[]string, *[]string) {
	t.Helper()

	var mu sync.Mutex
	acquireCalls := []string{}
	releaseCalls := []string{}
	acquireIndexes := map[string]int{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/lease/acquire":
			resourceType := r.URL.Query().Get("type")

			mu.Lock()
			acquireCalls = append(acquireCalls, resourceType)
			replyIndex := acquireIndexes[resourceType]
			acquireIndexes[resourceType] = replyIndex + 1
			replies := acquireReplies[resourceType]
			mu.Unlock()

			if replyIndex >= len(replies) {
				t.Fatalf("unexpected acquire request %d for resource type %q", replyIndex+1, resourceType)
			}

			reply := replies[replyIndex]
			if reply.delay > 0 {
				time.Sleep(reply.delay)
			}
			w.WriteHeader(reply.statusCode)
			_, _ = w.Write([]byte(reply.body))
		case "/lease/release":
			var requestBody struct {
				Names []string `json:"names"`
			}
			if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
				t.Fatalf("expected release request body to decode: %v", err)
			}

			mu.Lock()
			releaseCalls = append(releaseCalls, requestBody.Names...)
			mu.Unlock()

			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))

	return server, &acquireCalls, &releaseCalls
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func equalDurations(got, want []time.Duration) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

type fakeClock struct {
	current time.Time
	slept   []time.Duration
}

func newFakeClock(start time.Time) *fakeClock {
	return &fakeClock{current: start}
}

func (c *fakeClock) Now() time.Time {
	return c.current
}

func (c *fakeClock) Sleep(ctx context.Context, duration time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.slept = append(c.slept, duration)
	c.current = c.current.Add(duration)
	return nil
}
