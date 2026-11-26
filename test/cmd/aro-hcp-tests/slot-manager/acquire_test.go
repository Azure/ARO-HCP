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
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/acquire"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets/infrastructureidentities"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/catalog"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/lease"
	"github.com/Azure/ARO-HCP/test/pkg/leaseenv"
)

func TestAcquireAdmissionFlagAndEnvironment(t *testing.T) {
	for _, test := range []struct {
		name  string
		env   string
		flags []string
		want  []string
	}{
		{name: "enabled by default"},
		{name: "environment", env: " e2e_identities, e2e_identities\ninfrastructure_identities ", want: []string{"e2e_identities", "infrastructure_identities"}},
		{name: "comma-separated flag", flags: []string{"--disable-asset-admission=e2e_identities,infrastructure_identities"}, want: []string{"e2e_identities", "infrastructure_identities"}},
		{name: "flag overrides environment", env: "infrastructure_identities", flags: []string{"--disable-asset-admission=e2e_identities"}, want: []string{"e2e_identities"}},
		{name: "empty flag restores admission", env: "e2e_identities", flags: []string{"--disable-asset-admission="}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("ARO_HCP_DISABLE_ASSET_ADMISSION", test.env)
			options := DefaultAcquireOptions()
			command := &cobra.Command{}
			if err := BindAcquireOptions(options, command); err != nil {
				t.Fatal(err)
			}
			if err := command.ParseFlags(test.flags); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(options.DisabledAssetAdmission, test.want) {
				t.Fatalf("unexpected admission opt-out: got %v want %v", options.DisabledAssetAdmission, test.want)
			}
		})
	}
}

func TestAcquireAdmissionTimeoutFlagAndEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name, env string
		flags     []string
		want      time.Duration
		wantError bool
	}{
		{name: "default", want: 10 * time.Minute},
		{name: "environment", env: " 25m ", want: 25 * time.Minute},
		{name: "flag", flags: []string{"--admission-timeout=30m"}, want: 30 * time.Minute},
		{name: "flag overrides environment", env: "25m", flags: []string{"--admission-timeout=12m"}, want: 12 * time.Minute},
		{name: "flag overrides invalid environment", env: "bad", flags: []string{"--admission-timeout=12m"}, want: 12 * time.Minute},
		{name: "empty flag resets default", env: "25m", flags: []string{"--admission-timeout="}, want: 10 * time.Minute},
		{name: "zero", flags: []string{"--admission-timeout=0s"}, wantError: true},
		{name: "negative", flags: []string{"--admission-timeout=-1m"}, wantError: true},
		{name: "missing unit", flags: []string{"--admission-timeout=20"}, wantError: true},
		{name: "invalid environment", env: "bad", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ARO_HCP_ADMISSION_TIMEOUT", tc.env)
			options := DefaultAcquireOptions()
			options.ClusterProfileDir = "unused-profile"
			options.Environment = "dev"
			options.SharedDir = t.TempDir()
			options.LeaseProxyServerURL = "http://unused.invalid"
			command := &cobra.Command{}
			if err := BindAcquireOptions(options, command); err != nil {
				t.Fatal(err)
			}
			if err := command.ParseFlags(tc.flags); err != nil {
				t.Fatal(err)
			}
			validated, err := options.Validate()
			if tc.wantError {
				if err == nil || !strings.Contains(err.Error(), "--admission-timeout") {
					t.Fatalf("expected timeout validation failure before leasing, got %v", err)
				}
				return
			}
			if err != nil || validated.admissionTimeout != tc.want {
				t.Fatalf("expected timeout %v, got options=%+v error=%v", tc.want, validated, err)
			}
		})
	}
}

func TestAcquireRunRejectsUnsupportedInfrastructureBundleCountBeforeLease(t *testing.T) {
	t.Parallel()
	server, acquireCalls, _ := newTestLeaseProxyServer(t, map[string][]leaseProxyReply{
		"aro-hcp-dev-slot": {successAcquireReply("aro-hcp-dev-slot-00")},
	})
	defer server.Close()

	registry, err := assets.NewRegistry(infrastructureidentities.NewHandler())
	if err != nil {
		t.Fatal(err)
	}
	options := &AcquireOptions{completedAcquireOptions: &completedAcquireOptions{
		Plan: &acquire.Plan{CandidatePools: []catalog.Pool{{
			Name: "dev-slot", ResourceType: "aro-hcp-dev-slot",
			SlotAssets: catalog.SlotAssets{
				InfrastructureIdentities: &catalog.LeasedAsset{Allocation: catalog.AllocationLeased, AssetPool: "bundles", UnitsPerSlot: 2},
			},
		}}},
		RunConfig: acquire.RunConfig{
			AssetRegistry:     registry,
			LeaseProxyURL:     server.URL,
			LeaseProxyTimeout: time.Second,
		},
	}}
	err = options.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "does not accept demanded asset") {
		t.Fatalf("expected unsupported bundle count rejection, got %v", err)
	}
	if len(*acquireCalls) != 0 {
		t.Fatalf("unsupported bundle count reached lease proxy: %v", *acquireCalls)
	}
}

func TestAcquireCompleteRegionModeMatrix(t *testing.T) {
	fixedRegion := "westus3"
	alternateRegion := "eastus2"

	type testCase struct {
		regionMode         catalog.RegionMode
		allowedLocations   string
		override           string
		wantRuntimeRegion  string
		wantPoolRegion     string
		wantErrorSubstring string
	}

	cases := []testCase{
		{
			regionMode:        catalog.RegionModeFixed,
			allowedLocations:  "",
			override:          "",
			wantRuntimeRegion: fixedRegion,
			wantPoolRegion:    fixedRegion,
		},
		{
			regionMode:        catalog.RegionModeFixed,
			allowedLocations:  fixedRegion,
			override:          "",
			wantRuntimeRegion: fixedRegion,
			wantPoolRegion:    fixedRegion,
		},
		{
			regionMode:        catalog.RegionModeFixed,
			allowedLocations:  strings.Join([]string{fixedRegion, alternateRegion}, ","),
			override:          "",
			wantRuntimeRegion: fixedRegion,
			wantPoolRegion:    fixedRegion,
		},
		{
			regionMode:         catalog.RegionModeFixed,
			allowedLocations:   alternateRegion,
			override:           "",
			wantErrorSubstring: "no pool found",
		},
		{
			regionMode:        catalog.RegionModeFixed,
			allowedLocations:  fixedRegion,
			override:          fixedRegion,
			wantRuntimeRegion: fixedRegion,
			wantPoolRegion:    fixedRegion,
		},
		{
			regionMode:         catalog.RegionModeFixed,
			allowedLocations:   fixedRegion,
			override:           alternateRegion,
			wantErrorSubstring: "no pool found",
		},
		{
			regionMode:        catalog.RegionModeRuntimeSelected,
			allowedLocations:  "",
			override:          "",
			wantRuntimeRegion: fixedRegion,
			wantPoolRegion:    fixedRegion,
		},
		{
			regionMode:        catalog.RegionModeRuntimeSelected,
			allowedLocations:  fixedRegion,
			override:          "",
			wantRuntimeRegion: fixedRegion,
			wantPoolRegion:    fixedRegion,
		},
		{
			regionMode:        catalog.RegionModeRuntimeSelected,
			allowedLocations:  alternateRegion,
			override:          "",
			wantRuntimeRegion: fixedRegion,
			wantPoolRegion:    fixedRegion,
		},
		{
			regionMode:        catalog.RegionModeRuntimeSelected,
			allowedLocations:  strings.Join([]string{fixedRegion, alternateRegion}, ","),
			override:          "",
			wantRuntimeRegion: fixedRegion,
			wantPoolRegion:    fixedRegion,
		},
		{
			regionMode:        catalog.RegionModeRuntimeSelected,
			allowedLocations:  "",
			override:          alternateRegion,
			wantRuntimeRegion: alternateRegion,
			wantPoolRegion:    fixedRegion,
		},
		{
			regionMode:        catalog.RegionModeRuntimeSelected,
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
			t.Setenv(leaseenv.RuntimeDeployEnvironment, "ci01")
			t.Setenv("SHARED_DIR", t.TempDir())
			t.Setenv("LEASE_PROXY_SERVER_URL", "http://lease-proxy.example.com")
			t.Setenv("ALLOWED_LOCATIONS", tc.allowedLocations)
			t.Setenv("MULTISTAGE_PARAM_OVERRIDE_LOCATION", tc.override)

			opts := DefaultAcquireOptions()
			opts.CatalogPath = catalogPath
			opts.SetupAzureGlobalLogger = noopSetupAzureGlobalLogger

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
			if got := completed.RuntimeRegionForPool(completed.CandidatePools[0]); got != tc.wantRuntimeRegion {
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
	if opts.LeaseProxyTimeout != lease.DefaultLeaseProxyTimeout {
		t.Fatalf("expected default lease proxy timeout %s, got %s", lease.DefaultLeaseProxyTimeout, opts.LeaseProxyTimeout)
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
	t.Setenv("LOCATION_WEIGHTS", "westus3=1, centralus=2")
	t.Setenv("BUILD_ID", "12345")

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
	if opts.LocationWeights != "westus3=1, centralus=2" {
		t.Fatalf("expected location weights from environment, got %q", opts.LocationWeights)
	}
	if opts.BuildID != "12345" {
		t.Fatalf("expected build ID from environment, got %q", opts.BuildID)
	}
}

func TestAcquireCompleteWeightedRequiresInputsWithoutOverride(t *testing.T) {
	t.Parallel()

	clusterProfileDir := writeAcquireTestClusterProfile(t, "dev-sub")
	catalogPath := writeWeightedAcquireTestCatalog(t)
	baseOptions := func() *RawAcquireOptions {
		return &RawAcquireOptions{
			ClusterProfileDir:   clusterProfileDir,
			DeployEnv:           "ci01",
			SharedDir:           t.TempDir(),
			CatalogPath:         catalogPath,
			LeaseProxyServerURL: "http://lease-proxy.example.com",
			LeaseProxyTimeout:   lease.DefaultLeaseProxyTimeout,
			MaxWaitForLease:     DefaultMaxWaitForLease,
			LeaseWaitInterval:   DefaultLeaseWaitInterval,
		}
	}

	opts := baseOptions()
	opts.BuildID = "123"
	if _, err := completeAcquireOptions(opts); err == nil || !strings.Contains(err.Error(), "LOCATION_WEIGHTS") {
		t.Fatalf("expected missing weights error, got %v", err)
	}

	opts = baseOptions()
	opts.LocationWeights = "westus3=1,centralus=1,canadacentral=1"
	if _, err := completeAcquireOptions(opts); err == nil || !strings.Contains(err.Error(), "BUILD_ID") {
		t.Fatalf("expected missing BUILD_ID error, got %v", err)
	}

	opts = baseOptions()
	opts.SelectedLocation = "westus3"
	opts.LocationWeights = "invalid"
	completed, err := completeAcquireOptions(opts)
	if err != nil {
		t.Fatalf("expected override to bypass weighted inputs: %v", err)
	}
	if completed.RegionSelection.RuntimeRegion != "westus3" {
		t.Fatalf("expected override runtime region %q, got %q", "westus3", completed.RegionSelection.RuntimeRegion)
	}
}

func TestEffectiveClusterProfileDirs(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		dirs []string
		dir  string
		want []string
	}{
		{
			name: "dirs normalized: trimmed, deduped, empties dropped",
			dirs: []string{" /var/run/a ", "", "/var/run/b", "/var/run/a", "  "},
			dir:  "/var/run/fallback",
			want: []string{"/var/run/a", "/var/run/b"},
		},
		{
			name: "dirs that normalize to empty fall back to single dir",
			dirs: []string{"", "  "},
			dir:  " /var/run/fallback ",
			want: []string{"/var/run/fallback"},
		},
		{
			name: "single dir fallback is trimmed",
			dir:  "  /var/run/only  ",
			want: []string{"/var/run/only"},
		},
		{
			name: "nothing supplied yields nil",
			want: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			opts := &RawAcquireOptions{ClusterProfileDirs: tc.dirs, ClusterProfileDir: tc.dir}
			if got := opts.effectiveClusterProfileDirs(); !equalStrings(got, tc.want) {
				t.Fatalf("unexpected effective cluster profile dirs: got %v want %v", got, tc.want)
			}
		})
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
	catalogPath := writeAcquireTestCatalogFromYAML(t, `version: 2
environments:
  dev:
    deployment_environment: {name: ci01}
    pools:
      - name: aro-hcp-dev-centralus-a-slot
        subscriptions: {e2e: dev-sub-a}
        region: centralus
        region_mode: fixed
        resource_type: aro-hcp-dev-centralus-a-slot
        slot_count: 1
        slot_assets:
          e2e_identities:
            allocation: dedicated
            resource_group_prefix: aro-hcp-msi-container-dev-a
            resource_group_count: 2
      - name: aro-hcp-dev-centralus-b-slot
        subscriptions: {e2e: dev-sub-b}
        region: centralus
        region_mode: fixed
        resource_type: aro-hcp-dev-centralus-b-slot
        slot_count: 1
        slot_assets:
          e2e_identities:
            allocation: dedicated
            resource_group_prefix: aro-hcp-msi-container-dev-b
            resource_group_count: 2
      - name: aro-hcp-dev-canadacentral-c-slot
        subscriptions: {e2e: dev-sub-c}
        region: canadacentral
        region_mode: fixed
        resource_type: aro-hcp-dev-canadacentral-c-slot
        slot_count: 1
        slot_assets:
          e2e_identities:
            allocation: dedicated
            resource_group_prefix: aro-hcp-msi-container-dev-c
            resource_group_count: 2
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
	err := acquireForTest(t, &RawAcquireOptions{
		ClusterProfileDir:   clusterProfileDir,
		DeployEnv:           "ci01",
		AllowedLocations:    []string{"centralus"},
		SharedDir:           sharedDir,
		CatalogPath:         catalogPath,
		LeaseProxyServerURL: server.URL,
		LeaseProxyTimeout:   50 * time.Millisecond,
		MaxWaitForLease:     DefaultMaxWaitForLease,
		LeaseWaitInterval:   DefaultLeaseWaitInterval,
		Now:                 staticNow(time.Unix(0, 0)),
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

	state, err := lease.LoadAcquiredSlotState(sharedDir)
	if err != nil {
		t.Fatalf("expected acquired slot state to load: %v", err)
	}
	if state.Slot.ResourceType != "aro-hcp-dev-centralus-b-slot" {
		t.Fatalf("expected fallback pool resource type, got %q", state.Slot.ResourceType)
	}
	if state.RuntimeRegion != "centralus" {
		t.Fatalf("expected runtime region %q, got %q", "centralus", state.RuntimeRegion)
	}

	envFile, err := lease.EnvFile(sharedDir)
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
	catalogPath := writeAcquireTestCatalogFromYAML(t, `version: 2
environments:
  dev:
    deployment_environment: {name: ci01}
    pools:
      - name: aro-hcp-dev-centralus-a-slot
        subscriptions: {e2e: dev-sub-a}
        region: centralus
        region_mode: fixed
        resource_type: aro-hcp-dev-centralus-a-slot
        slot_count: 1
        slot_assets:
          e2e_identities:
            allocation: dedicated
            resource_group_prefix: aro-hcp-msi-container-dev-a
            resource_group_count: 2
      - name: aro-hcp-dev-centralus-b-slot
        subscriptions: {e2e: dev-sub-b}
        region: centralus
        region_mode: fixed
        resource_type: aro-hcp-dev-centralus-b-slot
        slot_count: 1
        slot_assets:
          e2e_identities:
            allocation: dedicated
            resource_group_prefix: aro-hcp-msi-container-dev-b
            resource_group_count: 2
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
	err := acquireForTest(t, &RawAcquireOptions{
		ClusterProfileDir:   clusterProfileDir,
		DeployEnv:           "ci01",
		AllowedLocations:    []string{"centralus"},
		SharedDir:           sharedDir,
		CatalogPath:         catalogPath,
		LeaseProxyServerURL: server.URL,
		LeaseProxyTimeout:   50 * time.Millisecond,
		MaxWaitForLease:     DefaultMaxWaitForLease,
		LeaseWaitInterval:   DefaultLeaseWaitInterval,
		Now:                 staticNow(time.Unix(0, 0)),
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

	state, err := lease.LoadAcquiredSlotState(sharedDir)
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
	catalogPath := writeAcquireTestCatalogFromYAML(t, `version: 2
environments:
  prod:
    deployment_environment: {name: prod}
    pools:
      - name: aro-hcp-prod-shard0-slot
        subscriptions: {e2e: prod-sub-a}
        region: uksouth
        region_mode: runtime-selected
        resource_type: aro-hcp-prod-shard0-slot
        slot_count: 1
        slot_assets:
          e2e_identities:
            allocation: dedicated
            resource_group_prefix: aro-hcp-msi-container-prod-a
            resource_group_count: 2
      - name: aro-hcp-prod-shard1-slot
        subscriptions: {e2e: prod-sub-b}
        region: uksouth
        region_mode: runtime-selected
        resource_type: aro-hcp-prod-shard1-slot
        slot_count: 1
        slot_assets:
          e2e_identities:
            allocation: dedicated
            resource_group_prefix: aro-hcp-msi-container-prod-b
            resource_group_count: 2
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
	err := acquireForTest(t, &RawAcquireOptions{
		ClusterProfileDir:   clusterProfileDir,
		DeployEnv:           "prod",
		SelectedLocation:    "eastus2",
		SharedDir:           sharedDir,
		CatalogPath:         catalogPath,
		LeaseProxyServerURL: server.URL,
		LeaseProxyTimeout:   50 * time.Millisecond,
		MaxWaitForLease:     DefaultMaxWaitForLease,
		LeaseWaitInterval:   DefaultLeaseWaitInterval,
		Now:                 staticNow(time.Unix(0, 0)),
	})
	if err != nil {
		t.Fatalf("expected acquire to succeed: %v", err)
	}

	if got, want := *acquireCalls, []string{"aro-hcp-prod-shard0-slot", "aro-hcp-prod-shard1-slot"}; !equalStrings(got, want) {
		t.Fatalf("unexpected acquire call order: got %v want %v", got, want)
	}

	state, err := lease.LoadAcquiredSlotState(sharedDir)
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

func TestAcquireRunWeightedSelectsRegionAndFallsBackAcrossSubscriptions(t *testing.T) {
	t.Parallel()

	clusterProfileDir := writeAcquireTestClusterProfiles(t, "dev-sub-b")
	catalogPath := writeAcquireTestCatalogFromYAML(t, `version: 2
environments:
  dev:
    deployment_environment: {name: ci01}
    pools:
      - name: aro-hcp-dev-shard0-slot
        subscriptions: {e2e: dev-sub-a}
        region_mode: weighted
        regions: [westus3, centralus, canadacentral]
        resource_type: aro-hcp-dev-shard0-slot
        slot_count: 1
        slot_assets:
          e2e_identities:
            allocation: dedicated
            provisioning_region: westus3
            resource_group_prefix: aro-hcp-msi-container-dev-a
            resource_group_count: 2
      - name: aro-hcp-dev-shard1-slot
        subscriptions: {e2e: dev-sub-b}
        region_mode: weighted
        regions: [westus3, centralus, canadacentral]
        resource_type: aro-hcp-dev-shard1-slot
        slot_count: 1
        slot_assets:
          e2e_identities:
            allocation: dedicated
            provisioning_region: westus3
            resource_group_prefix: aro-hcp-msi-container-dev-b
            resource_group_count: 2
`)

	server, acquireCalls, _ := newTestLeaseProxyServer(t, map[string][]leaseProxyReply{
		"aro-hcp-dev-shard0-slot": {
			unavailableAcquireReply("aro-hcp-dev-shard0-slot"),
		},
		"aro-hcp-dev-shard1-slot": {
			successAcquireReply("aro-hcp-dev-shard1-slot-00"),
		},
	})
	defer server.Close()

	sharedDir := t.TempDir()
	err := acquireForTest(t, &RawAcquireOptions{
		ClusterProfileDir:   clusterProfileDir,
		DeployEnv:           "ci01",
		AllowedLocations:    []string{"eastus2"},
		LocationWeights:     "westus3=1,centralus=1,canadacentral=1",
		BuildID:             "0",
		SharedDir:           sharedDir,
		CatalogPath:         catalogPath,
		LeaseProxyServerURL: server.URL,
		LeaseProxyTimeout:   50 * time.Millisecond,
		MaxWaitForLease:     DefaultMaxWaitForLease,
		LeaseWaitInterval:   DefaultLeaseWaitInterval,
		Now:                 staticNow(time.Unix(0, 0)),
	})
	if err != nil {
		t.Fatalf("expected weighted acquire to succeed: %v", err)
	}

	if got, want := *acquireCalls, []string{"aro-hcp-dev-shard0-slot", "aro-hcp-dev-shard1-slot"}; !equalStrings(got, want) {
		t.Fatalf("unexpected acquire call order: got %v want %v", got, want)
	}

	state, err := lease.LoadAcquiredSlotState(sharedDir)
	if err != nil {
		t.Fatalf("expected acquired slot state to load: %v", err)
	}
	if state.RuntimeRegion != "canadacentral" {
		t.Fatalf("expected weighted runtime region %q, got %q", "canadacentral", state.RuntimeRegion)
	}
	if state.Slot.ResourceType != "aro-hcp-dev-shard1-slot" {
		t.Fatalf("expected fallback subscription pool, got %q", state.Slot.ResourceType)
	}

	envFile, err := lease.EnvFile(sharedDir)
	if err != nil {
		t.Fatalf("expected env file path: %v", err)
	}
	envContents, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("expected env file read to succeed: %v", err)
	}
	if !strings.Contains(string(envContents), `export SELECTED_LOCATION='canadacentral'`) {
		t.Fatalf("expected selected weighted location in env file, got:\n%s", envContents)
	}
}

func TestAcquireRunStopsOnHardFailure(t *testing.T) {
	t.Parallel()

	clusterProfileDir := writeAcquireTestClusterProfiles(t, "dev-sub-b")
	catalogPath := writeAcquireTestCatalogFromYAML(t, `version: 2
environments:
  dev:
    deployment_environment: {name: ci01}
    pools:
      - name: aro-hcp-dev-centralus-a-slot
        subscriptions: {e2e: dev-sub-a}
        region: centralus
        region_mode: fixed
        resource_type: aro-hcp-dev-centralus-a-slot
        slot_count: 1
        slot_assets:
          e2e_identities:
            allocation: dedicated
            resource_group_prefix: aro-hcp-msi-container-dev-a
            resource_group_count: 2
      - name: aro-hcp-dev-centralus-b-slot
        subscriptions: {e2e: dev-sub-b}
        region: centralus
        region_mode: fixed
        resource_type: aro-hcp-dev-centralus-b-slot
        slot_count: 1
        slot_assets:
          e2e_identities:
            allocation: dedicated
            resource_group_prefix: aro-hcp-msi-container-dev-b
            resource_group_count: 2
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
	err := acquireForTest(t, &RawAcquireOptions{
		ClusterProfileDir:   clusterProfileDir,
		DeployEnv:           "ci01",
		AllowedLocations:    []string{"centralus"},
		SharedDir:           sharedDir,
		CatalogPath:         catalogPath,
		LeaseProxyServerURL: server.URL,
		LeaseProxyTimeout:   5 * time.Second,
		MaxWaitForLease:     DefaultMaxWaitForLease,
		LeaseWaitInterval:   DefaultLeaseWaitInterval,
		Now:                 staticNow(time.Unix(0, 0)),
	})
	if err == nil {
		t.Fatal("expected acquire to fail on hard lease-proxy error")
	}
	if got, want := *acquireCalls, []string{"aro-hcp-dev-centralus-a-slot"}; !equalStrings(got, want) {
		t.Fatalf("unexpected acquire call order: got %v want %v", got, want)
	}
	if _, err := lease.LoadAcquiredSlotState(sharedDir); err == nil {
		t.Fatal("expected no acquired slot state to be written on hard failure")
	}
}

func TestAcquireRunRetriesAfterFullUnavailablePassAcrossPools(t *testing.T) {
	t.Parallel()

	clusterProfileDir := writeAcquireTestClusterProfiles(t, "dev-sub-b")
	catalogPath := writeAcquireTestCatalogFromYAML(t, `version: 2
environments:
  dev:
    deployment_environment: {name: ci01}
    pools:
      - name: aro-hcp-dev-centralus-a-slot
        subscriptions: {e2e: dev-sub-a}
        region: centralus
        region_mode: fixed
        resource_type: aro-hcp-dev-centralus-a-slot
        slot_count: 1
        slot_assets:
          e2e_identities:
            allocation: dedicated
            resource_group_prefix: aro-hcp-msi-container-dev-a
            resource_group_count: 2
      - name: aro-hcp-dev-centralus-b-slot
        subscriptions: {e2e: dev-sub-b}
        region: centralus
        region_mode: fixed
        resource_type: aro-hcp-dev-centralus-b-slot
        slot_count: 1
        slot_assets:
          e2e_identities:
            allocation: dedicated
            resource_group_prefix: aro-hcp-msi-container-dev-b
            resource_group_count: 2
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

func TestAcquireRunRotatesCandidatePoolStartingPoint(t *testing.T) {
	t.Parallel()

	clusterProfileDir := writeAcquireTestClusterProfiles(t, "dev-sub-a")
	catalogPath := writeAcquireTestCatalogFromYAML(t, `version: 2
environments:
  dev:
    deployment_environment: {name: ci01}
    pools:
      - name: aro-hcp-dev-centralus-a-slot
        subscriptions: {e2e: dev-sub-a}
        region: centralus
        region_mode: fixed
        resource_type: aro-hcp-dev-centralus-a-slot
        slot_count: 1
        slot_assets:
          e2e_identities:
            allocation: dedicated
            resource_group_prefix: aro-hcp-msi-container-dev-a
            resource_group_count: 2
      - name: aro-hcp-dev-centralus-b-slot
        subscriptions: {e2e: dev-sub-b}
        region: centralus
        region_mode: fixed
        resource_type: aro-hcp-dev-centralus-b-slot
        slot_count: 1
        slot_assets:
          e2e_identities:
            allocation: dedicated
            resource_group_prefix: aro-hcp-msi-container-dev-b
            resource_group_count: 2
`)

	server, acquireCalls, _ := newTestLeaseProxyServer(t, map[string][]leaseProxyReply{
		"aro-hcp-dev-centralus-b-slot": {
			unavailableAcquireReply("aro-hcp-dev-centralus-b-slot"),
		},
		"aro-hcp-dev-centralus-a-slot": {
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
		MaxWaitForLease:     DefaultMaxWaitForLease,
		LeaseWaitInterval:   DefaultLeaseWaitInterval,
	})
	completed.Now = staticNow(time.Unix(0, 1))

	if err := completed.Run(context.Background()); err != nil {
		t.Fatalf("expected acquire run to succeed with rotated starting pool: %v", err)
	}
	if got, want := *acquireCalls, []string{
		"aro-hcp-dev-centralus-b-slot",
		"aro-hcp-dev-centralus-a-slot",
	}; !equalStrings(got, want) {
		t.Fatalf("unexpected acquire call order: got %v want %v", got, want)
	}

	state, err := lease.LoadAcquiredSlotState(completed.SharedDir)
	if err != nil {
		t.Fatalf("expected acquired slot state to load: %v", err)
	}
	if state.Slot.ResourceType != "aro-hcp-dev-centralus-a-slot" {
		t.Fatalf("expected fallback to preserve rotated order, got %q", state.Slot.ResourceType)
	}
}

func TestAcquireRunRetriesAfterFullUnavailablePassAfterRetryableServerFailure(t *testing.T) {
	t.Parallel()

	clusterProfileDir := writeAcquireTestClusterProfiles(t, "dev-sub-a")
	catalogPath := writeAcquireTestCatalogFromYAML(t, `version: 2
environments:
  dev:
    deployment_environment: {name: ci01}
    pools:
      - name: aro-hcp-dev-centralus-a-slot
        subscriptions: {e2e: dev-sub-a}
        region: centralus
        region_mode: fixed
        resource_type: aro-hcp-dev-centralus-a-slot
        slot_count: 1
        slot_assets:
          e2e_identities:
            allocation: dedicated
            resource_group_prefix: aro-hcp-msi-container-dev-a
            resource_group_count: 2
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
	catalogPath := writeAcquireTestCatalogFromYAML(t, `version: 2
environments:
  dev:
    deployment_environment: {name: ci01}
    pools:
      - name: aro-hcp-dev-centralus-a-slot
        subscriptions: {e2e: dev-sub-a}
        region: centralus
        region_mode: fixed
        resource_type: aro-hcp-dev-centralus-a-slot
        slot_count: 1
        slot_assets:
          e2e_identities:
            allocation: dedicated
            resource_group_prefix: aro-hcp-msi-container-dev-a
            resource_group_count: 2
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
	catalogPath := writeAcquireTestCatalogFromYAML(t, `version: 2
environments:
  dev:
    deployment_environment: {name: ci01}
    pools:
      - name: aro-hcp-dev-centralus-a-slot
        subscriptions: {e2e: dev-sub-a}
        region: centralus
        region_mode: fixed
        resource_type: aro-hcp-dev-centralus-a-slot
        slot_count: 1
        slot_assets:
          e2e_identities:
            allocation: dedicated
            resource_group_prefix: aro-hcp-msi-container-dev-a
            resource_group_count: 2
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
	catalogPath := writeAcquireTestCatalogFromYAML(t, `version: 2
environments:
  dev:
    deployment_environment: {name: ci01}
    pools:
      - name: aro-hcp-dev-centralus-a-slot
        subscriptions: {e2e: dev-sub-a}
        region: centralus
        region_mode: fixed
        resource_type: aro-hcp-dev-centralus-a-slot
        slot_count: 1
        slot_assets:
          e2e_identities:
            allocation: dedicated
            resource_group_prefix: aro-hcp-msi-container-dev-a
            resource_group_count: 2
`)

	server, acquireCalls, releaseCalls := newTestLeaseProxyServer(t, map[string][]leaseProxyReply{
		"aro-hcp-dev-centralus-a-slot": {
			successAcquireReply("aro-hcp-dev-centralus-a-slot-00"),
		},
	})
	defer server.Close()

	sharedDir := t.TempDir()
	err := acquireForTest(t, &RawAcquireOptions{
		ClusterProfileDir:   clusterProfileDir,
		DeployEnv:           "ci01",
		AllowedLocations:    []string{"centralus"},
		SharedDir:           sharedDir,
		CatalogPath:         catalogPath,
		LeaseProxyServerURL: server.URL,
		LeaseProxyTimeout:   5 * time.Second,
		MaxWaitForLease:     DefaultMaxWaitForLease,
		LeaseWaitInterval:   DefaultLeaseWaitInterval,
		Now:                 staticNow(time.Unix(0, 0)),
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

	if _, err := lease.LoadAcquiredSlotState(sharedDir); err == nil {
		t.Fatal("expected no acquired slot state to be written on post-acquire failure")
	}
}

func TestAcquireRunDefaultsSubscriptionResolverAndRollsBack(t *testing.T) {
	t.Parallel()

	// The cluster profile names the pool's subscription but carries no
	// credentials, so the production resolver fails after the lease is granted.
	// Without a default resolver this path would dereference a nil function.
	clusterProfileDir := writeAcquireTestClusterProfile(t, "dev-sub-a")
	catalogPath := writeAcquireTestCatalogFromYAML(t, `version: 2
environments:
  dev:
    deployment_environment: {name: ci01}
    pools:
      - name: aro-hcp-dev-centralus-a-slot
        subscriptions: {e2e: dev-sub-a}
        region: centralus
        region_mode: fixed
        resource_type: aro-hcp-dev-centralus-a-slot
        slot_count: 1
        slot_assets:
          e2e_identities:
            allocation: dedicated
            resource_group_prefix: aro-hcp-msi-container-dev-a
            resource_group_count: 2
`)
	server, _, releaseCalls := newTestLeaseProxyServer(t, map[string][]leaseProxyReply{
		"aro-hcp-dev-centralus-a-slot": {successAcquireReply("aro-hcp-dev-centralus-a-slot-00")},
	})
	defer server.Close()

	sharedDir := t.TempDir()
	options := completeAcquireForTest(t, &RawAcquireOptions{
		ClusterProfileDir:   clusterProfileDir,
		DeployEnv:           "ci01",
		AllowedLocations:    []string{"centralus"},
		SharedDir:           sharedDir,
		CatalogPath:         catalogPath,
		LeaseProxyServerURL: server.URL,
		LeaseProxyTimeout:   5 * time.Second,
		MaxWaitForLease:     DefaultMaxWaitForLease,
		LeaseWaitInterval:   DefaultLeaseWaitInterval,
		Now:                 staticNow(time.Unix(0, 0)),
	})
	options.ResolveSubscriptions = nil

	if err := options.Run(context.Background()); err == nil {
		t.Fatal("expected the production subscription resolver to fail without credentials")
	}
	if got, want := *releaseCalls, []string{"aro-hcp-dev-centralus-a-slot-00"}; !equalStrings(got, want) {
		t.Fatalf("expected lease to be released after resolver failure: got %v want %v", got, want)
	}
	if _, err := lease.LoadAcquiredSlotState(sharedDir); err == nil {
		t.Fatal("expected no acquired slot state after rollback")
	}
}

func TestAcquireRunRollsBackWhenEnvFileWriteFailsAfterStateWrite(t *testing.T) {
	t.Parallel()

	clusterProfileDir := writeAcquireTestClusterProfiles(t, "dev-sub-a")
	catalogPath := writeAcquireTestCatalogFromYAML(t, `version: 2
environments:
  dev:
    deployment_environment: {name: ci01}
    pools:
      - name: aro-hcp-dev-centralus-a-slot
        subscriptions: {e2e: dev-sub-a}
        region: centralus
        region_mode: fixed
        resource_type: aro-hcp-dev-centralus-a-slot
        slot_count: 1
        slot_assets:
          e2e_identities:
            allocation: dedicated
            resource_group_prefix: aro-hcp-msi-container-dev-a
            resource_group_count: 2
`)

	server, acquireCalls, releaseCalls := newTestLeaseProxyServer(t, map[string][]leaseProxyReply{
		"aro-hcp-dev-centralus-a-slot": {
			successAcquireReply("aro-hcp-dev-centralus-a-slot-00"),
		},
	})
	defer server.Close()

	sharedDir := t.TempDir()
	envFile, err := lease.EnvFile(sharedDir)
	if err != nil {
		t.Fatalf("expected env file path resolution to succeed: %v", err)
	}
	options := completeAcquireForTest(t, &RawAcquireOptions{
		ClusterProfileDir:   clusterProfileDir,
		DeployEnv:           "ci01",
		AllowedLocations:    []string{"centralus"},
		SharedDir:           sharedDir,
		CatalogPath:         catalogPath,
		LeaseProxyServerURL: server.URL,
		LeaseProxyTimeout:   5 * time.Second,
		MaxWaitForLease:     DefaultMaxWaitForLease,
		LeaseWaitInterval:   DefaultLeaseWaitInterval,
		Now:                 staticNow(time.Unix(0, 0)),
	})
	calls := []string{}
	options.AssetRegistry, err = assets.NewRegistry(&lifecycleHandler{
		kind: catalog.KindE2EIdentities, calls: &calls,
		before: func(phase string, _ assets.LeaseRequest) {
			if phase != "publish" {
				return
			}
			if _, err := lease.LoadAcquiredSlotState(sharedDir); err != nil {
				t.Fatalf("expected durable journal before publication: %v", err)
			}
			if err := os.Mkdir(envFile, 0o755); err != nil {
				t.Fatal(err)
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = options.Run(context.Background())
	if err == nil {
		t.Fatal("expected acquire to fail when the env file cannot be written")
	}
	if !strings.Contains(err.Error(), "failed to write env file") {
		t.Fatalf("expected env file write error, got %v", err)
	}

	if got, want := *acquireCalls, []string{"aro-hcp-dev-centralus-a-slot"}; !equalStrings(got, want) {
		t.Fatalf("unexpected acquire call order: got %v want %v", got, want)
	}
	if got, want := *releaseCalls, []string{"aro-hcp-dev-centralus-a-slot-00"}; !equalStrings(got, want) {
		t.Fatalf("expected journaled rollback after publication failure, got %v", got)
	}

	if _, err := lease.LoadAcquiredSlotState(sharedDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected completed rollback to remove journal: %v", err)
	}
}

func writeAcquireTestCatalog(t *testing.T, regionMode catalog.RegionMode, region string) string {
	t.Helper()

	return writeAcquireTestCatalogFromYAML(t, fmt.Sprintf(`version: 2
environments:
  dev:
    deployment_environment: {name: ci01}
    pools:
      - name: aro-hcp-dev-shard0-slot
        subscriptions: {e2e: "ARO HCP E2E Hosted Clusters (EA Subscription)"}
        region: %s
        region_mode: %s
        resource_type: aro-hcp-dev-shard0-slot
        slot_count: 2
        slot_assets:
          e2e_identities:
            allocation: dedicated
            resource_group_prefix: aro-hcp-msi-container-dev
            resource_group_count: 2
`, region, regionMode))
}

func writeWeightedAcquireTestCatalog(t *testing.T) string {
	t.Helper()

	return writeAcquireTestCatalogFromYAML(t, `version: 2
environments:
  dev:
    deployment_environment: {name: ci01}
    pools:
      - name: aro-hcp-dev-shard0-slot
        subscriptions: {e2e: dev-sub}
        region_mode: weighted
        regions: [westus3, centralus, canadacentral]
        resource_type: aro-hcp-dev-shard0-slot
        slot_count: 2
        slot_assets:
          e2e_identities:
            allocation: dedicated
            provisioning_region: westus3
            resource_group_prefix: aro-hcp-msi-container-dev
            resource_group_count: 2
`)
}

func writeAcquireTestCatalogFromYAML(t *testing.T, slotCatalog string) string {
	t.Helper()

	catalogDir := t.TempDir()
	catalogPath := filepath.Join(catalogDir, "e2e-slots.yaml")
	if err := os.WriteFile(catalogPath, []byte(slotCatalog), 0o644); err != nil {
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

	completed, err := completeAcquireOptions(raw)
	if err != nil {
		t.Fatalf("expected acquire options completion to succeed: %v", err)
	}
	return completed
}

// noopCloser is a no-op io.Closer, so test doubles can return a real,
// closeable value instead of a nil interface.
type noopCloser struct{}

func (noopCloser) Close() error { return nil }

// noopSetupAzureGlobalLogger stands in for setupAzureGlobalLogger in tests so they
// never touch the filesystem or the Azure SDK's process-wide, non-goroutine-safe
// log.SetListener - both of which would otherwise be hit on every test that
// completes RawAcquireOptions, including ones running under t.Parallel.
func noopSetupAzureGlobalLogger(string) (io.Closer, error) {
	return noopCloser{}, nil
}

func completeAcquireOptions(raw *RawAcquireOptions) (*AcquireOptions, error) {
	calls := []string{}
	registry, err := assets.NewRegistry(&lifecycleHandler{kind: catalog.KindE2EIdentities, calls: &calls})
	if err != nil {
		return nil, err
	}
	raw.AssetRegistry = registry
	raw.ResolveSubscriptions = func(_ context.Context, _, _, e2e, _ string) (catalog.ResolvedSubscriptions, error) {
		return catalog.ResolvedSubscriptions{E2E: catalog.ResolvedSubscription{Name: e2e, ID: "e2e-id"}}, nil
	}
	// SetupAzureGlobalLogger is a no-op in tests, so the path is never read or
	// written. It only needs to satisfy Validate's non-empty requirement.
	if raw.AzureGlobalLoggerLogPath == "" {
		raw.AzureGlobalLoggerLogPath = "azure-global-logger-test.log"
	}
	raw.SetupAzureGlobalLogger = noopSetupAzureGlobalLogger
	validated, err := raw.Validate()
	if err != nil {
		return nil, err
	}
	return validated.Complete(context.Background())
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

func staticNow(at time.Time) func() time.Time {
	return func() time.Time {
		return at
	}
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

func acquireForTest(t *testing.T, raw *RawAcquireOptions) error {
	t.Helper()
	options, err := completeAcquireOptions(raw)
	if err != nil {
		return err
	}
	return options.Run(context.Background())
}
