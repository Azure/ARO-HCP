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
	"strings"
	"testing"
	"time"
)

// Shared fixtures for the query-template render tests. The cluster id is lowercased
// to match how validate() normalizes it before it reaches the templates.
const (
	tplClusterURI = "https://example.kusto.example.com"
	tplSub        = "00000000-0000-0000-0000-000000000000"
	tplRG         = "rg-test"
	tplClusterID  = "/subscriptions/00000000-0000-0000-0000-000000000000/resourcegroups/rg-test/providers/microsoft.redhatopenshift/hcpopenshiftclusters/c1"
)

// baseRenderData returns query data with every field the seed-mode templates read
// populated. Individual cases only vary SeedFromIdentity; templates ignore the fields
// they do not reference.
func baseRenderData() queryData {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	return queryData{
		ClusterURI:                  tplClusterURI,
		ServiceDatabase:             "ServiceLogs",
		FullStartTime:               now,
		FullEndTime:                 now.Add(time.Hour),
		PhaseStartTime:              now,
		PhaseEndTime:                now.Add(time.Hour),
		SubscriptionID:              tplSub,
		ResourceGroup:               tplRG,
		ClusterID:                   "cs-cluster-id",
		ResourceID:                  tplClusterID,
		ClusterResourceID:           tplClusterID,
		ServiceProviderResourceType: "microsoft.redhatopenshift/hcpopenshiftclusters",
	}
}

// TestQueryTemplateSeedMode guards the SeedFromIdentity fork across every template that
// branches on it. cosmosResourceSnapshots is a change feed, so identity-seeded runs scan
// history up to the window end (`timestamp <=`) and collapse each resource to its latest
// non-deleted snapshot (`arg_max(content._ts)` + `isempty(deletionTimestamp)`), while
// request-seeded runs keep the e2e-validated window-scoped behavior (`between` +
// `take_any`/`distinct`, no identity collapse). The bundleMap partial is exercised
// through the maestro/transitions query that pulls it in.
func TestQueryTemplateSeedMode(t *testing.T) {
	// descendantMatch is the path-segment-aware cluster filter fragment: the cluster id
	// followed by a slash, so '/c1' does not match a sibling '/c10'.
	descendantMatch := "startswith '" + tplClusterID + "/'"
	// unboundedMatch is the old, non-segment-aware prefix that must no longer appear.
	unboundedMatch := "startswith '" + tplClusterID + "'"

	tests := []struct {
		name     string
		path     string
		identity bool
		contains []string
		absent   []string
	}{
		{
			// clusterByResourceGroup has no fork: it is only ever the identity-resolution
			// query, so it is always window-independent with the latest-snapshot collapse.
			name:     "clusterByResourceGroup resolution",
			path:     "queries/backend/clusterByResourceGroup/query.kql",
			contains: []string{tplSub, "hcpopenshiftclusters", "timestamp <=", "arg_max(tolong(content._ts)", "isempty(deletionTimestamp)"},
			absent:   []string{"between"},
		},
		{
			name:     "cosmosResourceDiscovery identity",
			path:     "queries/backend/cosmosResourceDiscovery/query.kql",
			identity: true,
			contains: []string{"timestamp <=", descendantMatch, "arg_max(tolong(content._ts)", "isempty(deletionTimestamp)"},
			absent:   []string{"between", unboundedMatch},
		},
		{
			name:     "cosmosResourceDiscovery request",
			path:     "queries/backend/cosmosResourceDiscovery/query.kql",
			contains: []string{"between"},
			absent:   []string{"arg_max"},
		},
		{
			name:     "resourceInternalId identity",
			path:     "queries/backend/resourceInternalId/query.kql",
			identity: true,
			contains: []string{"timestamp <=", "arg_max(tolong(content._ts)", "isempty(deletionTimestamp)"},
			absent:   []string{"between"},
		},
		{
			name:     "resourceInternalId request",
			path:     "queries/backend/resourceInternalId/query.kql",
			contains: []string{"between"},
			absent:   []string{"arg_max"},
		},
		{
			name:     "hostedClusterMetadata identity",
			path:     "queries/hypershift/hostedClusterMetadata/query.kql",
			identity: true,
			contains: []string{"timestamp <=", "arg_max(tolong(content._ts)", "isempty(deletionTimestamp)"},
			absent:   []string{"between"},
		},
		{
			name:     "hostedClusterMetadata request",
			path:     "queries/hypershift/hostedClusterMetadata/query.kql",
			contains: []string{"between", "take_any(content) by etag"},
			absent:   []string{"arg_max"},
		},
		{
			// bundleMap is a shared partial reading the change feed in its backend block;
			// exercised via maestro/transitions. Its backend block honors the same fork.
			// (The query's own containerLogs scan keeps `between` in both modes, so that
			// substring is not asserted here.)
			name:     "bundleMap backend identity",
			path:     "queries/maestro/transitions/query.kql",
			identity: true,
			contains: []string{"| where timestamp <= ", "arg_max(tolong(content._ts), content, deletionTimestamp)", "isempty(deletionTimestamp)", descendantMatch},
			absent:   []string{"take_any(content) by etag"},
		},
		{
			name:     "bundleMap backend request",
			path:     "queries/maestro/transitions/query.kql",
			contains: []string{"take_any(content) by etag"},
			absent:   []string{"arg_max(tolong(content._ts), content, deletionTimestamp)"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := baseRenderData()
			data.SeedFromIdentity = tt.identity
			rendered, err := renderQuery(tt.path, data)
			if err != nil {
				t.Fatalf("renderQuery(%s) failed: %v", tt.path, err)
			}
			if strings.TrimSpace(rendered) == "" {
				t.Fatalf("renderQuery(%s) produced empty output", tt.path)
			}
			for _, want := range tt.contains {
				if !strings.Contains(rendered, want) {
					t.Errorf("rendered query should contain %q, got:\n%s", want, rendered)
				}
			}
			for _, notWant := range tt.absent {
				if strings.Contains(rendered, notWant) {
					t.Errorf("rendered query should not contain %q, got:\n%s", notWant, rendered)
				}
			}
		})
	}
}
