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
	"testing"

	"github.com/Azure/ARO-HCP/dev-infrastructure/scripts/internal/poolnaming"
)

func TestExpectedPoolNames(t *testing.T) {
	tests := []struct {
		name     string
		baseName string
		count    int
		mode     string
		zones    []string
		want     []string
	}{
		{
			name:     "zonal enabled 2 zones 2 pools",
			baseName: "infrasd4ds5",
			count:    2,
			mode:     "Enabled",
			zones:    []string{"1", "2"},
			want:     []string{"infrasd4ds51", "infrasd4ds52"},
		},
		{
			name:     "zonal auto 2 zones 2 pools",
			baseName: "infrasd4ds5",
			count:    2,
			mode:     "Auto",
			zones:    []string{"1", "2"},
			want:     []string{"infrasd4ds51", "infrasd4ds52"},
		},
		{
			name:     "non-zonal disabled 2 pools",
			baseName: "infra",
			count:    2,
			mode:     "Disabled",
			zones:    []string{},
			want:     []string{"infranz1", "infranz2"},
		},
		{
			name:     "auto no zones falls back to non-zonal",
			baseName: "infra",
			count:    2,
			mode:     "Auto",
			zones:    []string{},
			want:     []string{"infranz1", "infranz2"},
		},
		{
			// Mirrors the e2e svc cluster: POOL_ZONES is empty in config so
			// run.go resolves the region's availability zones ([1,2,3]) before
			// calling Expected; with count 1 this yields the single zonal pool
			// "infra1" (matching ARM), not "infranz1".
			name:     "auto with region zones resolved yields zonal infra1",
			baseName: "infra",
			count:    1,
			mode:     "Auto",
			zones:    []string{"1", "2", "3"},
			want:     []string{"infra1"},
		},
		{
			name:     "more pools than zones spills to non-zonal",
			baseName: "infra",
			count:    3,
			mode:     "Enabled",
			zones:    []string{"1", "2"},
			want:     []string{"infra1", "infra2", "infranz1"},
		},
		{
			name:     "single pool non-zonal",
			baseName: "infra",
			count:    1,
			mode:     "Disabled",
			zones:    []string{},
			want:     []string{"infranz1"},
		},
		{
			name:     "stg uksouth mgmt observed config infrasd4ds5 count 2",
			baseName: "infrasd4ds5",
			count:    2,
			mode:     "Enabled",
			zones:    []string{"1", "2"},
			want:     []string{"infrasd4ds51", "infrasd4ds52"},
		},
		{
			// pool.bicep applies take(pool.name, 12); names longer than 12 chars
			// must be truncated to match ARM/Kubernetes reality.
			name:     "long base name truncated to 12 chars",
			baseName: "verylongbase",
			count:    1,
			mode:     "Enabled",
			zones:    []string{"1"},
			want:     []string{"verylongbase"}, // "verylongbase1" truncated to 12
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := poolnaming.Expected(tt.baseName, tt.count, tt.mode, tt.zones)
			if len(got) != len(tt.want) {
				t.Fatalf("poolnaming.Expected() len=%d, want %d\n  got:  %v\n  want: %v",
					len(got), len(tt.want), got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("poolnaming.Expected()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestRetainedPools(t *testing.T) {
	tests := []struct {
		name   string
		live   []string
		extras []string
		want   []string
	}{
		{
			name:   "deletes some, retains rest",
			live:   []string{"infra1", "infra2", "infra3"},
			extras: []string{"infra2"},
			want:   []string{"infra1", "infra3"},
		},
		{
			name:   "deletes all leaves none (must be refused by caller)",
			live:   []string{"infra1", "infra2"},
			extras: []string{"infra1", "infra2"},
			want:   nil,
		},
		{
			name:   "no extras retains all",
			live:   []string{"infra1"},
			extras: nil,
			want:   []string{"infra1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := retainedPools(tt.live, tt.extras)
			if len(got) != len(tt.want) {
				t.Fatalf("retainedPools() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("retainedPools()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestParseEnvConfig_RequiredFields(t *testing.T) {
	base := map[string]string{
		"CLUSTER_NAME":        "test-cluster",
		"RESOURCE_GROUP":      "test-rg",
		"SUBSCRIPTION_ID":     "test-sub",
		"POOL_TAG":            "infra",
		"POOL_BASE_NAME":      "infrasd4ds5",
		"POOL_COUNT":          "2",
		"ZONE_REDUNDANT_MODE": "Enabled",
		"POOL_ZONES":          "1,2",
	}

	env := func(k string) string { return base[k] }

	cfg, err := parseEnvConfig(env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.clusterName != "test-cluster" {
		t.Errorf("clusterName = %q, want test-cluster", cfg.clusterName)
	}
	if cfg.poolCount != 2 {
		t.Errorf("poolCount = %d, want 2", cfg.poolCount)
	}
	// DRY_RUN defaults to true
	if !cfg.dryRun {
		t.Error("dryRun should default to true")
	}
	if cfg.poolMinCount != defaultMinCount {
		t.Errorf("poolMinCount = %d, want %d", cfg.poolMinCount, defaultMinCount)
	}
}

func TestParseEnvConfig_DryRunFalse(t *testing.T) {
	base := map[string]string{
		"CLUSTER_NAME":        "test-cluster",
		"RESOURCE_GROUP":      "test-rg",
		"SUBSCRIPTION_ID":     "test-sub",
		"POOL_TAG":            "infra",
		"POOL_BASE_NAME":      "infra",
		"POOL_COUNT":          "1",
		"ZONE_REDUNDANT_MODE": "Disabled",
		"DRY_RUN":             "false",
	}
	env := func(k string) string { return base[k] }
	cfg, err := parseEnvConfig(env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.dryRun {
		t.Error("dryRun should be false when DRY_RUN=false")
	}
}

func TestParseEnvConfig_MissingRequired(t *testing.T) {
	env := func(k string) string { return "" }
	_, err := parseEnvConfig(env)
	if err == nil {
		t.Error("expected error for missing required fields")
	}
}

func TestParseEnvConfig_InvalidMode(t *testing.T) {
	base := map[string]string{
		"CLUSTER_NAME":        "c",
		"RESOURCE_GROUP":      "rg",
		"SUBSCRIPTION_ID":     "sub",
		"POOL_TAG":            "infra",
		"POOL_BASE_NAME":      "infra",
		"POOL_COUNT":          "1",
		"ZONE_REDUNDANT_MODE": "bogus",
	}
	env := func(k string) string { return base[k] }
	_, err := parseEnvConfig(env)
	if err == nil {
		t.Error("expected error for invalid ZONE_REDUNDANT_MODE")
	}
}
