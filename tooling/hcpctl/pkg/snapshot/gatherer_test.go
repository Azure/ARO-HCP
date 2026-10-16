// Copyright 2025 Microsoft Corporation
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

package snapshot

import (
	"context"
	"strings"
	"testing"
)

// TestResolveSeedCluster covers the branches of resolveSeedCluster that do not hit
// Kusto: an explicit cluster id (with and without a supplied subscription), a
// subscription that disagrees with the id, a malformed id, and the no-seed no-op.
// The subscription+resource-group resolution path is exercised through
// TestResolveClusterFromResourceGroup instead, as it requires a live client.
func TestResolveSeedCluster(t *testing.T) {
	const (
		id  = "/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/rg-a/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/c1"
		sub = "11111111-1111-1111-1111-111111111111"
	)
	g := &Gatherer{} // nil client: only the explicit/empty branches are exercised
	ctx := context.Background()

	tests := []struct {
		name       string
		input      GatherInput
		wantID     string
		wantSub    string
		wantErrSub string // substring; "" means expect success
	}{
		{
			name:    "explicit id derives subscription from id",
			input:   GatherInput{SeedClusterResourceID: id},
			wantID:  id,
			wantSub: sub,
		},
		{
			name:    "explicit id with matching subscription",
			input:   GatherInput{SeedClusterResourceID: id, SeedSubscriptionID: sub},
			wantID:  id,
			wantSub: sub,
		},
		{
			name:       "explicit id with mismatched subscription errors",
			input:      GatherInput{SeedClusterResourceID: id, SeedSubscriptionID: "22222222-2222-2222-2222-222222222222"},
			wantErrSub: "does not match",
		},
		{
			name:       "malformed explicit id errors",
			input:      GatherInput{SeedClusterResourceID: "not-an-arm-id"},
			wantErrSub: "invalid SeedClusterResourceID",
		},
		{
			name:  "no seed is a no-op",
			input: GatherInput{},
			// empty id + sub: fall back to request seeding
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotID, gotSub, err := g.resolveSeedCluster(ctx, tt.input, queryData{})
			if tt.wantErrSub != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErrSub)
				}
				if !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("error %q does not contain %q", err, tt.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotID != tt.wantID || gotSub != tt.wantSub {
				t.Errorf("got (%q, %q), want (%q, %q)", gotID, gotSub, tt.wantID, tt.wantSub)
			}
		})
	}
}

// TestSelectCosmosDiscoveryData covers the cluster-selection precedence that drives
// Cosmos/per-resource discovery: identity mode pins to the seed regardless of
// request-derived clusters (guarding against a co-located HCP in the same resource
// group), while request mode picks the first request carrying both a cluster id and
// a subscription.
func TestSelectCosmosDiscoveryData(t *testing.T) {
	seed := queryData{ClusterResourceID: "seed-cluster", SubscriptionID: "seed-sub", ServiceDatabase: "ServiceLogs"}

	t.Run("identity mode always uses the seed, ignoring request-derived clusters", func(t *testing.T) {
		reqs := []queryData{{ClusterResourceID: "other-cluster", SubscriptionID: "other-sub"}}
		got := selectCosmosDiscoveryData(seedModeIdentity, seed, reqs)
		if got.ClusterResourceID != "seed-cluster" || got.SubscriptionID != "seed-sub" {
			t.Errorf("got (%q, %q), want the seed cluster", got.ClusterResourceID, got.SubscriptionID)
		}
	})

	t.Run("request mode picks the first request with cluster and subscription", func(t *testing.T) {
		reqs := []queryData{
			{ClusterResourceID: "", SubscriptionID: "s0"},   // no cluster → skipped
			{ClusterResourceID: "c1", SubscriptionID: ""},   // no subscription → skipped
			{ClusterResourceID: "c2", SubscriptionID: "s2"}, // chosen
			{ClusterResourceID: "c3", SubscriptionID: "s3"}, // ignored
		}
		got := selectCosmosDiscoveryData(seedModeRequest, seed, reqs)
		if got.ClusterResourceID != "c2" || got.SubscriptionID != "s2" {
			t.Errorf("got (%q, %q), want (c2, s2)", got.ClusterResourceID, got.SubscriptionID)
		}
		if got.ServiceDatabase != "ServiceLogs" {
			t.Errorf("other seed fields should be preserved, got ServiceDatabase %q", got.ServiceDatabase)
		}
	})

	t.Run("request mode with no qualifying request returns zero value", func(t *testing.T) {
		got := selectCosmosDiscoveryData(seedModeRequest, seed, nil)
		if got.ClusterResourceID != "" {
			t.Errorf("got %q, want empty (no cluster resolved)", got.ClusterResourceID)
		}
	})
}

// TestContextQueriesFor verifies the frontend request query is omitted in identity
// mode (the run is not request-anchored) while alert context queries always run.
func TestContextQueriesFor(t *testing.T) {
	has := func(qs []querySpec, name string) bool {
		for _, q := range qs {
			if q.queryName == name {
				return true
			}
		}
		return false
	}

	t.Run("request mode includes frontendRequests", func(t *testing.T) {
		got := contextQueriesFor(seedModeRequest)
		if len(got) != len(contextQueries) {
			t.Errorf("got %d queries, want all %d", len(got), len(contextQueries))
		}
		if !has(got, "frontendRequests") {
			t.Error("request mode should include the frontendRequests query")
		}
	})

	t.Run("identity mode omits frontendRequests but keeps alerts", func(t *testing.T) {
		got := contextQueriesFor(seedModeIdentity)
		if len(got) != len(contextQueries)-1 {
			t.Errorf("got %d queries, want %d (frontendRequests dropped)", len(got), len(contextQueries)-1)
		}
		if has(got, "frontendRequests") {
			t.Error("identity mode should omit the frontendRequests query")
		}
		if len(got) == 0 {
			t.Error("identity mode should still run alert context queries")
		}
	})
}

// TestSeedModeSkipsRequestDiscovery locks the request-discovery gate to the mode.
func TestSeedModeSkipsRequestDiscovery(t *testing.T) {
	if !seedModeIdentity.skipsRequestDiscovery() {
		t.Error("identity mode should skip request discovery")
	}
	if seedModeRequest.skipsRequestDiscovery() {
		t.Error("request mode should run request discovery")
	}
}

// TestResolveClusterFromResourceGroup covers the three resolution outcomes of the
// subscription+resource-group identity seed: zero rows (wrong scope / outside
// window), exactly one (resolved), and more than one (ambiguous).
func TestResolveClusterFromResourceGroup(t *testing.T) {
	const (
		sub = "00000000-0000-0000-0000-000000000000"
		rg  = "rg-test"
		id1 = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg-test/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/c1"
		id2 = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg-test/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/c2"
	)

	row := func(v string) resultRow {
		return resultRow{columns: []string{"clusterResourceID"}, values: []string{v}}
	}

	tests := []struct {
		name string
		rows []resultRow
		want string // expected resolved id; "" means expect an error
		// wantErrContains/Absent are only consulted when want == "".
		wantErrContains []string
		wantErrAbsent   []string
	}{
		{
			name: "single row resolves",
			rows: []resultRow{row(id1)},
			want: id1,
		},
		{
			name: "empty rows are ignored, resolving remaining single",
			rows: []resultRow{row(""), row(id1)},
			want: id1,
		},
		{
			name:            "zero rows errors naming sub and rg",
			rows:            nil,
			wantErrContains: []string{sub, rg, "at or before the window end"},
			// The lookup ignores the window start, so the message must not claim the
			// window was searched; it reports the window end instead.
			wantErrAbsent: []string{"within the time window"},
		},
		{
			name:            "multiple rows errors listing candidates",
			rows:            []resultRow{row(id1), row(id2)},
			wantErrContains: []string{id1, id2, "--cluster-resource-id"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveClusterFromResourceGroup(tt.rows, sub, rg)
			if tt.want == "" {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				for _, s := range tt.wantErrContains {
					if !strings.Contains(err.Error(), s) {
						t.Errorf("error %q should contain %q", err, s)
					}
				}
				for _, s := range tt.wantErrAbsent {
					if strings.Contains(err.Error(), s) {
						t.Errorf("error %q should not contain %q", err, s)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
